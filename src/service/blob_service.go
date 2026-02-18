package service

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"

	"go.uber.org/zap"

	"git.coldforge.xyz/coldforge/cloistr-blossom/db"
	"git.coldforge.xyz/coldforge/cloistr-blossom/internal/encryption"
	"git.coldforge.xyz/coldforge/cloistr-blossom/internal/storage"
	"git.coldforge.xyz/coldforge/cloistr-blossom/src/core"
)

type blobService struct {
	db         *sql.DB
	queries    *db.Queries
	storage    storage.StorageBackend
	encryptor  *encryption.Encryptor
	cdnBaseUrl string
	log        *zap.Logger
}

func NewBlobService(
	db *sql.DB,
	queries *db.Queries,
	storageBackend storage.StorageBackend,
	encryptor *encryption.Encryptor,
	cdnBaseUrl string,
	log *zap.Logger,
) (core.BlobStorage, error) {
	return &blobService{
		db:         db,
		queries:    queries,
		storage:    storageBackend,
		encryptor:  encryptor,
		cdnBaseUrl: cdnBaseUrl,
		log:        log,
	}, nil
}

func (r *blobService) Save(
	ctx context.Context,
	pubkey string,
	sha256 string,
	url string,
	size int64,
	mimeType string,
	blob []byte,
	created int64,
	encryptionMode core.EncryptionMode,
) (*core.Blob, error) {
	var dataToStore []byte
	var encryptedDEK sql.NullString
	var encryptionNonce sql.NullString
	var originalSize sql.NullInt64
	finalEncryptionMode := string(encryptionMode)

	// Handle encryption based on mode
	switch encryptionMode {
	case core.EncryptionModeServer:
		// Server-side encryption requested
		if r.encryptor != nil && r.encryptor.IsEnabled() {
			encrypted, err := r.encryptor.Encrypt(blob)
			if err != nil {
				r.log.Error("failed to encrypt blob", zap.Error(err))
				return nil, err
			}
			dataToStore = encrypted.Ciphertext
			encryptedDEK = sql.NullString{String: encrypted.EncryptedDEK, Valid: true}
			encryptionNonce = sql.NullString{String: encrypted.Nonce, Valid: true}
			originalSize = sql.NullInt64{Int64: encrypted.OriginalSize, Valid: true}
			finalEncryptionMode = "server"
		} else {
			// Encryption requested but not available, store plaintext
			r.log.Warn("encryption requested but not enabled, storing plaintext")
			dataToStore = blob
			finalEncryptionMode = "none"
		}

	case core.EncryptionModeE2E:
		// Client already encrypted, store as-is
		dataToStore = blob
		originalSize = sql.NullInt64{Int64: size, Valid: true}
		finalEncryptionMode = "e2e"

	case core.EncryptionModeNone:
		// Plaintext storage - but check if server-side encryption is the default
		if r.encryptor != nil && r.encryptor.IsEnabled() {
			// Encrypt by default when encryption is enabled
			encrypted, err := r.encryptor.Encrypt(blob)
			if err != nil {
				r.log.Error("failed to encrypt blob", zap.Error(err))
				return nil, err
			}
			dataToStore = encrypted.Ciphertext
			encryptedDEK = sql.NullString{String: encrypted.EncryptedDEK, Valid: true}
			encryptionNonce = sql.NullString{String: encrypted.Nonce, Valid: true}
			originalSize = sql.NullInt64{Int64: encrypted.OriginalSize, Valid: true}
			finalEncryptionMode = "server"
		} else {
			dataToStore = blob
			finalEncryptionMode = "none"
		}

	default:
		dataToStore = blob
		finalEncryptionMode = "none"
	}

	// Store blob data in the storage backend
	if r.storage != nil {
		if err := r.storage.Put(ctx, sha256, bytes.NewReader(dataToStore), int64(len(dataToStore))); err != nil {
			return nil, err
		}
	}

	// Store metadata in the database
	_, err := r.queries.InsertBlob(
		ctx,
		db.InsertBlobParams{
			Pubkey:          pubkey,
			Hash:            sha256,
			Type:            mimeType,
			Size:            int64(len(dataToStore)),
			Blob:            dataToStore,
			Created:         created,
			EncryptionMode:  finalEncryptionMode,
			EncryptedDek:    encryptedDEK,
			EncryptionNonce: encryptionNonce,
			OriginalSize:    originalSize,
		},
	)
	if err != nil {
		// If DB insert fails, try to clean up storage
		if r.storage != nil {
			_ = r.storage.Delete(ctx, sha256)
		}
		return nil, err
	}

	// Return original size for encrypted blobs
	returnSize := size
	if originalSize.Valid {
		returnSize = originalSize.Int64
	}

	return &core.Blob{
		Url:            url,
		Sha256:         sha256,
		Size:           returnSize,
		Type:           mimeType,
		Uploaded:       created,
		EncryptionMode: core.EncryptionMode(finalEncryptionMode),
		NIP94: &core.NIP94FileMetadata{
			Url:            url,
			MimeType:       mimeType,
			OriginalSha256: sha256,
			Sha256:         sha256,
		},
	}, nil
}

func (r *blobService) Exists(ctx context.Context, sha256 string) (bool, error) {
	_, err := r.queries.GetBlobFromHash(ctx, sha256)

	return err == nil, err
}

func (r *blobService) GetFromHash(ctx context.Context, sha256 string) (*core.Blob, error) {
	dbBlob, err := r.queries.GetBlobFromHash(ctx, sha256)
	if err != nil {
		return nil, err
	}

	blob := r.dbBlobIntoBlobDescriptor(dbBlob)

	// If we have a storage backend and the DB blob data is empty, fetch from storage
	if r.storage != nil && len(dbBlob.Blob) == 0 {
		reader, err := r.storage.Get(ctx, sha256)
		if err == nil {
			defer reader.Close()
			data, err := io.ReadAll(reader)
			if err == nil {
				blob.Blob = data
			}
		}
	}

	// Decrypt if server-side encrypted
	if dbBlob.EncryptionMode == "server" && r.encryptor != nil && r.encryptor.IsEnabled() {
		encryptedBlob := &encryption.EncryptedBlob{
			Ciphertext:     blob.Blob,
			EncryptedDEK:   dbBlob.EncryptedDek.String,
			Nonce:          dbBlob.EncryptionNonce.String,
			EncryptionMode: encryption.ModeServer,
		}

		decrypted, err := r.encryptor.Decrypt(encryptedBlob)
		if err != nil {
			r.log.Error("failed to decrypt blob", zap.String("hash", sha256), zap.Error(err))
			return nil, err
		}
		blob.Blob = decrypted

		// Use original size
		if dbBlob.OriginalSize.Valid {
			blob.Size = dbBlob.OriginalSize.Int64
		}
	}

	return blob, nil
}

func (r *blobService) GetFromPubkey(ctx context.Context, pubkey string) ([]*core.Blob, error) {
	dbBlobs, err := r.queries.GetBlobsFromPubkey(ctx, pubkey)
	if err != nil {
		return nil, err
	}

	blobs := make([]*core.Blob, len(dbBlobs))
	for i := range dbBlobs {
		blobs[i] = r.dbBlobIntoBlobDescriptor(dbBlobs[i])
	}

	return blobs, nil
}

func (r *blobService) GetFromPubkeyWithFilter(ctx context.Context, pubkey string, filter *core.BlobFilter) (*core.BlobListResult, error) {
	// Build dynamic query with filters
	baseQuery := `SELECT pubkey, hash, type, size, created, encryption_mode, encrypted_dek, encryption_nonce, original_size FROM blobs WHERE pubkey = $1`
	countQuery := `SELECT COUNT(*) FROM blobs WHERE pubkey = $1`
	args := []interface{}{pubkey}
	argIndex := 2

	// Apply type prefix filter
	if filter != nil && filter.TypePrefix != "" {
		baseQuery += fmt.Sprintf(" AND type LIKE $%d", argIndex)
		countQuery += fmt.Sprintf(" AND type LIKE $%d", argIndex)
		args = append(args, filter.TypePrefix+"%")
		argIndex++
	}

	// Apply since filter
	if filter != nil && filter.Since > 0 {
		baseQuery += fmt.Sprintf(" AND created >= $%d", argIndex)
		countQuery += fmt.Sprintf(" AND created >= $%d", argIndex)
		args = append(args, filter.Since)
		argIndex++
	}

	// Apply until filter
	if filter != nil && filter.Until > 0 {
		baseQuery += fmt.Sprintf(" AND created <= $%d", argIndex)
		countQuery += fmt.Sprintf(" AND created <= $%d", argIndex)
		args = append(args, filter.Until)
		argIndex++
	}

	// Get total count before pagination
	var total int64
	if err := r.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("count blobs: %w", err)
	}

	// Apply sorting
	if filter != nil && filter.SortDesc {
		baseQuery += " ORDER BY created DESC"
	} else {
		baseQuery += " ORDER BY created ASC"
	}

	// Apply pagination
	if filter != nil && filter.Limit > 0 {
		baseQuery += fmt.Sprintf(" LIMIT $%d", argIndex)
		args = append(args, filter.Limit)
		argIndex++

		if filter.Offset > 0 {
			baseQuery += fmt.Sprintf(" OFFSET $%d", argIndex)
			args = append(args, filter.Offset)
		}
	}

	// Execute query
	rows, err := r.db.QueryContext(ctx, baseQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("query blobs: %w", err)
	}
	defer rows.Close()

	var blobs []*core.Blob
	for rows.Next() {
		var dbBlob db.Blob
		if err := rows.Scan(
			&dbBlob.Pubkey,
			&dbBlob.Hash,
			&dbBlob.Type,
			&dbBlob.Size,
			&dbBlob.Created,
			&dbBlob.EncryptionMode,
			&dbBlob.EncryptedDek,
			&dbBlob.EncryptionNonce,
			&dbBlob.OriginalSize,
		); err != nil {
			return nil, fmt.Errorf("scan blob: %w", err)
		}
		blobs = append(blobs, r.dbBlobIntoBlobDescriptor(dbBlob))
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate blobs: %w", err)
	}

	return &core.BlobListResult{
		Blobs: blobs,
		Total: total,
	}, nil
}

func (r *blobService) DeleteFromHash(ctx context.Context, sha256 string) error {
	// Delete from database first
	if err := r.queries.DeleteBlobFromHash(ctx, sha256); err != nil {
		return err
	}

	// Delete from storage backend
	if r.storage != nil {
		if err := r.storage.Delete(ctx, sha256); err != nil {
			r.log.Warn("failed to delete blob from storage backend",
				zap.String("hash", sha256),
				zap.Error(err))
			// Don't return error - DB is source of truth
		}
	}

	return nil
}

func (r *blobService) IsEncryptionEnabled() bool {
	return r.encryptor != nil && r.encryptor.IsEnabled()
}

func (r *blobService) dbBlobIntoBlobDescriptor(blob db.Blob) *core.Blob {
	url := r.cdnBaseUrl + "/" + blob.Hash

	// Use original size for encrypted blobs
	size := blob.Size
	if blob.OriginalSize.Valid {
		size = blob.OriginalSize.Int64
	}

	return &core.Blob{
		Pubkey:         blob.Pubkey,
		Url:            url,
		Sha256:         blob.Hash,
		Size:           size,
		Type:           blob.Type,
		Blob:           blob.Blob,
		Uploaded:       blob.Created,
		EncryptionMode: core.EncryptionMode(blob.EncryptionMode),
		NIP94: &core.NIP94FileMetadata{
			Url:            url,
			MimeType:       blob.Type,
			OriginalSha256: blob.Hash,
			Sha256:         blob.Hash,
		},
	}
}
