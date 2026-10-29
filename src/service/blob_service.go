package service

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strings"

	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/db"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/encryption"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/storage"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
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
			RefCount:        1, // Initial reference count
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

func (r *blobService) GetDescriptor(ctx context.Context, sha256 string) (*core.Blob, error) {
	dbBlob, err := r.queries.GetBlobFromHash(ctx, sha256)
	if err != nil {
		return nil, err
	}
	blob := r.dbBlobIntoBlobDescriptor(dbBlob)
	blob.Blob = nil
	// Report the length GetFromHash serves: plaintext for server-side
	// encryption, the stored bytes otherwise (e2e blobs are served as stored).
	if dbBlob.EncryptionMode != "server" {
		blob.Size = dbBlob.Size
	}
	return blob, nil
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
	// Build dynamic query with filters using blob_references for dedup support
	// Uses COUNT(*) OVER() window function to get total count in a single query (50% fewer DB round trips)
	baseQuery := `SELECT b.pubkey, b.hash, b.type, b.size, b.created, b.encryption_mode, b.encrypted_dek, b.encryption_nonce, b.original_size,
		COUNT(*) OVER() as total_count
		FROM blobs b
		INNER JOIN blob_references br ON b.hash = br.hash
		WHERE br.pubkey = $1`
	args := []interface{}{pubkey}
	argIndex := 2

	// Apply type prefix filter
	if filter != nil && filter.TypePrefix != "" {
		baseQuery += fmt.Sprintf(" AND b.type LIKE $%d", argIndex)
		args = append(args, filter.TypePrefix+"%")
		argIndex++
	}

	// Apply since filter
	if filter != nil && filter.Since > 0 {
		baseQuery += fmt.Sprintf(" AND b.created >= $%d", argIndex)
		args = append(args, filter.Since)
		argIndex++
	}

	// Apply until filter
	if filter != nil && filter.Until > 0 {
		baseQuery += fmt.Sprintf(" AND b.created <= $%d", argIndex)
		args = append(args, filter.Until)
		argIndex++
	}

	// Apply sorting
	if filter != nil && filter.SortDesc {
		baseQuery += " ORDER BY b.created DESC"
	} else {
		baseQuery += " ORDER BY b.created ASC"
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

	// Execute single query that returns both data and count
	rows, err := r.db.QueryContext(ctx, baseQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("query blobs: %w", err)
	}
	defer rows.Close()

	var blobs []*core.Blob
	var total int64
	for rows.Next() {
		var dbBlob db.Blob
		var rowTotal int64
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
			&rowTotal,
		); err != nil {
			return nil, fmt.Errorf("scan blob: %w", err)
		}
		// Total count is the same for all rows, just take it from first row
		if total == 0 {
			total = rowTotal
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

// SearchBlobs searches across all blobs on the server with optional filtering
// (MIME type prefix, created time range, size range, uploader) and pagination.
// It mirrors GetFromPubkeyWithFilter but is not scoped to a single pubkey, so it
// is intended for administrative/moderation use rather than public enumeration.
func (r *blobService) SearchBlobs(ctx context.Context, filter *core.BlobFilter) (*core.BlobListResult, error) {
	// When a pubkey is supplied, join blob_references so dedup'd uploads are
	// attributed to the requesting uploader; otherwise scan the canonical
	// blobs table directly.
	baseQuery := `SELECT b.pubkey, b.hash, b.type, b.size, b.created, b.encryption_mode, b.encrypted_dek, b.encryption_nonce, b.original_size,
		COUNT(*) OVER() as total_count
		FROM blobs b`
	args := []interface{}{}
	argIndex := 1
	conditions := []string{}

	if filter != nil && filter.Pubkey != "" {
		baseQuery += " INNER JOIN blob_references br ON b.hash = br.hash"
		conditions = append(conditions, fmt.Sprintf("br.pubkey = $%d", argIndex))
		args = append(args, filter.Pubkey)
		argIndex++
	}

	if filter != nil && filter.TypePrefix != "" {
		conditions = append(conditions, fmt.Sprintf("b.type LIKE $%d", argIndex))
		args = append(args, filter.TypePrefix+"%")
		argIndex++
	}
	if filter != nil && filter.Since > 0 {
		conditions = append(conditions, fmt.Sprintf("b.created >= $%d", argIndex))
		args = append(args, filter.Since)
		argIndex++
	}
	if filter != nil && filter.Until > 0 {
		conditions = append(conditions, fmt.Sprintf("b.created <= $%d", argIndex))
		args = append(args, filter.Until)
		argIndex++
	}
	if filter != nil && filter.MinSize > 0 {
		conditions = append(conditions, fmt.Sprintf("b.size >= $%d", argIndex))
		args = append(args, filter.MinSize)
		argIndex++
	}
	if filter != nil && filter.MaxSize > 0 {
		conditions = append(conditions, fmt.Sprintf("b.size <= $%d", argIndex))
		args = append(args, filter.MaxSize)
		argIndex++
	}

	if len(conditions) > 0 {
		baseQuery += " WHERE " + strings.Join(conditions, " AND ")
	}

	if filter != nil && filter.SortDesc {
		baseQuery += " ORDER BY b.created DESC"
	} else {
		baseQuery += " ORDER BY b.created ASC"
	}

	if filter != nil && filter.Limit > 0 {
		baseQuery += fmt.Sprintf(" LIMIT $%d", argIndex)
		args = append(args, filter.Limit)
		argIndex++

		if filter.Offset > 0 {
			baseQuery += fmt.Sprintf(" OFFSET $%d", argIndex)
			args = append(args, filter.Offset)
		}
	}

	rows, err := r.db.QueryContext(ctx, baseQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("search blobs: %w", err)
	}
	defer rows.Close()

	// Initialize to an empty (non-nil) slice so an empty result serializes as
	// "blobs":[] rather than "blobs":null.
	blobs := make([]*core.Blob, 0)
	var total int64
	for rows.Next() {
		var dbBlob db.Blob
		var rowTotal int64
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
			&rowTotal,
		); err != nil {
			return nil, fmt.Errorf("scan blob: %w", err)
		}
		// COUNT(*) OVER() is identical on every row.
		total = rowTotal
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

// SaveWithDedup saves a blob with content-addressable deduplication.
// If the blob already exists, it creates a reference without re-storing data.
func (r *blobService) SaveWithDedup(
	ctx context.Context,
	pubkey string,
	sha256 string,
	url string,
	size int64,
	mimeType string,
	blob []byte,
	created int64,
	encryptionMode core.EncryptionMode,
) (*core.Blob, bool, error) {
	// Check if blob already exists
	existingBlob, err := r.queries.GetBlobFromHash(ctx, sha256)
	if err == nil {
		// Blob exists - check if user already has a reference.
		//
		// The error is no longer discarded. It used to be, and that turned a
		// broken query into a confusing failure two steps downstream: on
		// SQLite the reserved-word alias in HasBlobReference made this return
		// (false, syntax error), the false sent every re-save into the insert
		// below, and the insert's ON CONFLICT DO NOTHING then surfaced as
		// "create reference: sql: no rows in result set" -- a message that
		// says nothing about the actual cause.
		hasRef, refErr := r.HasReference(ctx, pubkey, sha256)
		if refErr != nil {
			r.log.Error("failed to check blob reference", zap.Error(refErr))
			return nil, false, fmt.Errorf("check reference: %w", refErr)
		}
		if hasRef {
			// User already has this blob - return existing, nothing to charge
			return r.dbBlobIntoBlobDescriptor(existingBlob), false, nil
		}

		// Create reference for this user, under the blob row's lock so it
		// cannot interleave with the removal of the last other reference.
		added, gone, err := r.addReference(ctx, pubkey, sha256, created)
		if err != nil {
			r.log.Error("failed to create blob reference", zap.Error(err))
			return nil, false, fmt.Errorf("create reference: %w", err)
		}
		if !gone {
			if added {
				r.log.Info("blob deduplicated - created reference",
					zap.String("pubkey", pubkey),
					zap.String("hash", sha256))
			}
			// added is false when the reference already existed (a concurrent
			// save by this key), which is the state this call wants.
			return r.dbBlobIntoBlobDescriptor(existingBlob), added, nil
		}
		// The last owner removed the blob since we looked it up: store it
		// again below.
	}

	// Blob doesn't exist - save normally
	savedBlob, err := r.Save(ctx, pubkey, sha256, url, size, mimeType, blob, created, encryptionMode)
	if err != nil {
		return nil, false, err
	}

	// Create blob reference for the original uploader
	_, err = r.queries.CreateBlobReference(ctx, db.CreateBlobReferenceParams{
		Pubkey:  pubkey,
		Hash:    sha256,
		Created: created,
	})
	// As above, no rows means the reference was already there, which is the
	// desired end state rather than a problem worth logging.
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		// Fail the upload: without a reference the key would be charged for
		// a blob it can never delete. The stored blob is ownerless and the GC
		// reconcile reclaims it.
		r.log.Error("failed to create blob reference for original uploader", zap.Error(err))
		return nil, false, fmt.Errorf("create reference: %w", err)
	}

	r.log.Info("new blob saved with dedup tracking",
		zap.String("pubkey", pubkey),
		zap.String("hash", sha256))

	// No rows: a concurrent save by this key created the reference first and
	// is the one that charges for it.
	return savedBlob, !errors.Is(err, sql.ErrNoRows), nil
}

// HasReference checks if a user has a reference to a blob.
func (r *blobService) HasReference(ctx context.Context, pubkey string, sha256 string) (bool, error) {
	exists, err := r.queries.HasBlobReference(ctx, db.HasBlobReferenceParams{
		Pubkey: pubkey,
		Hash:   sha256,
	})
	if err != nil {
		return false, err
	}
	return exists, nil
}

// ListReferenceOwners returns every pubkey holding a reference to a blob.
func (r *blobService) ListReferenceOwners(ctx context.Context, sha256 string) ([]string, error) {
	refs, err := r.queries.GetBlobReferencesByHash(ctx, sha256)
	if err != nil {
		return nil, err
	}
	owners := make([]string, 0, len(refs))
	for _, ref := range refs {
		owners = append(owners, ref.Pubkey)
	}
	return owners, nil
}

// DeleteReference removes a user's reference to a blob.
// If this was the last reference, the actual blob is deleted.
func (r *blobService) DeleteReference(ctx context.Context, pubkey string, sha256 string) (bool, error) {
	// One transaction, taking the blob row's lock first (the same order as
	// addReference, so the two serialize and cannot deadlock):
	//   - the reference DELETE decides whether this call removed anything, so
	//     concurrent deletes credit once;
	//   - a ref_count error aborts the whole removal instead of being logged
	//     and leaving a wrong count behind;
	//   - the blob row goes only if no reference row remains, re-checked by
	//     the database (DeleteOwnerlessBlob), never because ref_count hit 0.
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()
	q := r.queries.WithTx(tx)

	if _, err := q.LockBlobRow(ctx, sha256); err != nil {
		return false, fmt.Errorf("lock blob: %w", err)
	}
	removed, err := q.DeleteBlobReference(ctx, db.DeleteBlobReferenceParams{
		Pubkey: pubkey,
		Hash:   sha256,
	})
	if err != nil {
		return false, fmt.Errorf("delete reference: %w", err)
	}
	if removed == 0 {
		return false, core.ErrReferenceNotFound
	}
	if _, err := q.DecrementBlobRefCount(ctx, sha256); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("decrement ref count: %w", err)
	}
	_, err = q.DeleteOwnerlessBlob(ctx, sha256)
	last := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("remove ownerless blob: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit: %w", err)
	}

	r.log.Info("blob reference deleted",
		zap.String("pubkey", pubkey),
		zap.String("hash", sha256),
		zap.Bool("last_reference", last))

	if last && r.storage != nil {
		if err := r.storage.Delete(ctx, sha256); err != nil {
			// The reference and row are gone, so the caller still owes the
			// credit (nil error). The orphaned bytes are left in storage.
			r.log.Error("failed to delete blob bytes after last reference removed",
				zap.String("hash", sha256),
				zap.Error(err))
		}
	}
	return last, nil
}

// addReference gives pubkey a reference to an existing blob, in one
// transaction under the blob row's lock. added is false when pubkey already
// had one; gone is true when the blob row no longer exists (its last owner
// removed it), in which case nothing was written.
func (r *blobService) addReference(ctx context.Context, pubkey, sha256 string, created int64) (added, gone bool, err error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, false, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()
	q := r.queries.WithTx(tx)

	locked, err := q.LockBlobRow(ctx, sha256)
	if err != nil {
		return false, false, fmt.Errorf("lock blob: %w", err)
	}
	if locked == 0 {
		return false, true, nil
	}
	_, err = q.CreateBlobReference(ctx, db.CreateBlobReferenceParams{
		Pubkey:  pubkey,
		Hash:    sha256,
		Created: created,
	})
	// No rows: ON CONFLICT DO NOTHING, the reference already existed.
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, tx.Commit()
	}
	if err != nil {
		return false, false, err
	}
	if err := q.IncrementBlobRefCount(ctx, sha256); err != nil {
		return false, false, fmt.Errorf("increment ref count: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, false, fmt.Errorf("commit: %w", err)
	}
	return true, false, nil
}
