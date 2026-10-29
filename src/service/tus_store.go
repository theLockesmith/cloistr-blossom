package service

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/db"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/storage"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
)

// tusStore implements core.TusUploadStore on the database and blob storage
// that every replica shares.
type tusStore struct {
	db      *sql.DB
	queries *db.Queries
	storage storage.StorageBackend
	log     *zap.Logger
}

// NewTusStore creates a resumable upload store.
func NewTusStore(database *sql.DB, queries *db.Queries, storageBackend storage.StorageBackend, log *zap.Logger) core.TusUploadStore {
	return &tusStore{db: database, queries: queries, storage: storageBackend, log: log}
}

func (s *tusStore) Create(ctx context.Context, upload *core.TusUpload) error {
	metadata, err := json.Marshal(upload.Metadata)
	if err != nil {
		return fmt.Errorf("encode metadata: %w", err)
	}
	return s.queries.CreateTusUpload(ctx, db.CreateTusUploadParams{
		ID:           upload.ID,
		Pubkey:       upload.Pubkey,
		Size:         upload.Size,
		UploadOffset: upload.Offset,
		Metadata:     string(metadata),
		CreatedAt:    upload.CreatedAt,
		ExpiresAt:    upload.ExpiresAt,
	})
}

func (s *tusStore) Get(ctx context.Context, id string) (*core.TusUpload, error) {
	row, err := s.queries.GetTusUpload(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, core.ErrTusUploadNotFound
	}
	if err != nil {
		return nil, err
	}
	metadata := map[string]string{}
	if err := json.Unmarshal([]byte(row.Metadata), &metadata); err != nil {
		return nil, fmt.Errorf("decode metadata: %w", err)
	}
	return &core.TusUpload{
		ID:        row.ID,
		Pubkey:    row.Pubkey,
		Size:      row.Size,
		Offset:    row.UploadOffset,
		Metadata:  metadata,
		CreatedAt: row.CreatedAt,
		ExpiresAt: row.ExpiresAt,
	}, nil
}

// AppendChunk writes the bytes first, under a key no other request can use,
// then records the chunk and advances the offset in one transaction that
// succeeds only if the offset is still the one the chunk was written for. Two
// requests racing for the same offset cannot both win, and the loser removes
// its own bytes without touching the winner's.
func (s *tusStore) AppendChunk(ctx context.Context, id string, offset int64, data []byte) (int64, error) {
	if len(data) == 0 {
		return offset, nil
	}
	key, err := tusChunkKey(id, offset)
	if err != nil {
		return offset, err
	}
	if err := s.storage.Put(ctx, key, bytes.NewReader(data), int64(len(data))); err != nil {
		return offset, fmt.Errorf("store chunk: %w", err)
	}
	newOffset := offset + int64(len(data))
	if err := s.recordChunk(ctx, id, offset, newOffset, key); err != nil {
		s.removeObjects(ctx, id, []string{key})
		return offset, err
	}
	return newOffset, nil
}

func (s *tusStore) recordChunk(ctx context.Context, id string, offset, newOffset int64, key string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	q := s.queries.WithTx(tx)

	advanced, err := q.AdvanceTusUploadOffset(ctx, db.AdvanceTusUploadOffsetParams{
		ID:             id,
		UploadOffset:   offset,
		UploadOffset_2: newOffset,
	})
	if err != nil {
		return err
	}
	if advanced == 0 {
		return core.ErrTusOffsetConflict
	}
	if err := q.CreateTusUploadChunk(ctx, db.CreateTusUploadChunkParams{
		UploadID:    id,
		ChunkOffset: offset,
		Size:        newOffset - offset,
		ObjectKey:   key,
	}); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *tusStore) ReadAll(ctx context.Context, id string) ([]byte, error) {
	chunks, err := s.queries.GetTusUploadChunks(ctx, id)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	for _, chunk := range chunks {
		if chunk.ChunkOffset != int64(buf.Len()) {
			return nil, fmt.Errorf("chunk at offset %d, expected %d", chunk.ChunkOffset, buf.Len())
		}
		r, err := s.storage.Get(ctx, chunk.ObjectKey)
		if err != nil {
			return nil, fmt.Errorf("read chunk at offset %d: %w", chunk.ChunkOffset, err)
		}
		n, err := io.Copy(&buf, r)
		r.Close()
		if err != nil {
			return nil, fmt.Errorf("read chunk at offset %d: %w", chunk.ChunkOffset, err)
		}
		if n != chunk.Size {
			return nil, fmt.Errorf("chunk at offset %d has %d bytes, expected %d", chunk.ChunkOffset, n, chunk.Size)
		}
	}
	return buf.Bytes(), nil
}

// Delete removes the upload row first. A chunk racing it either committed
// before (so its row is returned here and its bytes removed) or finds the
// upload gone and removes its own bytes.
func (s *tusStore) Delete(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	q := s.queries.WithTx(tx)

	removed, err := q.DeleteTusUpload(ctx, id)
	if err != nil {
		return err
	}
	keys, err := q.DeleteTusUploadChunks(ctx, id)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.removeObjects(ctx, id, keys)
	if removed == 0 {
		return core.ErrTusUploadNotFound
	}
	return nil
}

func (s *tusStore) DeleteExpired(ctx context.Context, now int64) (int, error) {
	ids, err := s.queries.GetExpiredTusUploadIDs(ctx, now)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, id := range ids {
		err := s.Delete(ctx, id)
		if err != nil && !errors.Is(err, core.ErrTusUploadNotFound) {
			return removed, err
		}
		if err == nil {
			removed++
		}
	}
	return removed, nil
}

// removeObjects removes chunk bytes whose rows are gone. A failure leaves an
// unreferenced object behind, which is logged rather than returned: the
// upload itself is already gone.
func (s *tusStore) removeObjects(ctx context.Context, id string, keys []string) {
	for _, key := range keys {
		if err := s.storage.Delete(ctx, key); err != nil {
			s.log.Warn("failed to remove tus chunk bytes",
				zap.String("upload_id", id),
				zap.String("object_key", key),
				zap.Error(err))
		}
	}
}

// tusChunkKey names a chunk's object in blob storage. The random suffix keeps
// two requests writing the same offset from overwriting each other's bytes.
// The name is not a SHA-256, so it can never collide with a blob.
func tusChunkKey(id string, offset int64) (string, error) {
	b := make([]byte, 8)
	if _, err := crand.Read(b); err != nil {
		return "", fmt.Errorf("chunk key: %w", err)
	}
	return fmt.Sprintf("tus-%s-%d-%s", id, offset, hex.EncodeToString(b)), nil
}
