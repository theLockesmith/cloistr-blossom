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
	"time"

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
		ExpiresAt:      time.Now().Add(core.TusUploadTTL).Unix(),
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
	removed, err := s.deleteUpload(ctx, id, func(q *db.Queries) (int64, error) {
		return q.DeleteTusUpload(ctx, id)
	})
	if err != nil {
		return err
	}
	if !removed {
		return core.ErrTusUploadNotFound
	}
	return nil
}

// DeleteExpired removes every upload whose expiry is before now. Each replica
// may run it at the same time: an upload removed by one is skipped by the
// other.
func (s *tusStore) DeleteExpired(ctx context.Context, now int64) (int, error) {
	ids, err := s.queries.GetExpiredTusUploadIDs(ctx, now)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, id := range ids {
		ok, err := s.deleteIfExpired(ctx, id, now)
		if err != nil {
			return removed, err
		}
		if ok {
			removed++
		}
	}
	return removed, nil
}

// deleteIfExpired removes the upload only if it is still expired: a chunk
// that arrived after the listing moved its expiry forward, and it stays.
func (s *tusStore) deleteIfExpired(ctx context.Context, id string, now int64) (bool, error) {
	return s.deleteUpload(ctx, id, func(q *db.Queries) (int64, error) {
		return q.DeleteTusUploadIfExpired(ctx, db.DeleteTusUploadIfExpiredParams{ID: id, ExpiresAt: now})
	})
}

// deleteUpload removes the upload row with del and, only if that removed it,
// its chunk rows and then their bytes.
func (s *tusStore) deleteUpload(ctx context.Context, id string, del func(*db.Queries) (int64, error)) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	q := s.queries.WithTx(tx)

	removed, err := del(q)
	if err != nil {
		return false, err
	}
	if removed == 0 {
		return false, nil
	}
	keys, err := q.DeleteTusUploadChunks(ctx, id)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	s.removeObjects(ctx, id, keys)
	return true, nil
}

// RunTusExpirySweep removes expired uploads now and then every interval until
// ctx ends, so abandoned uploads go even when no new upload arrives.
func RunTusExpirySweep(ctx context.Context, store core.TusUploadStore, interval time.Duration, log *zap.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		n, err := store.DeleteExpired(ctx, time.Now().Unix())
		if err != nil {
			log.Warn("failed to remove expired tus uploads", zap.Error(err))
		} else if n > 0 {
			log.Info("removed expired tus uploads", zap.Int("count", n))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
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
