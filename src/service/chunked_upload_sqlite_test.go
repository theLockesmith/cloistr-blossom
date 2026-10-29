package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/db"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/pkg/config"
)

// savingBlobStorage records the blob CompleteUpload saves. Every other
// core.BlobStorage method panics through the embedded interface.
type savingBlobStorage struct {
	core.BlobStorage
	saved []byte
}

func (m *savingBlobStorage) SaveWithDedup(
	_ context.Context,
	pubkey, sha256, url string,
	size int64,
	mimeType string,
	data []byte,
	created int64,
	encryptionMode core.EncryptionMode,
) (*core.Blob, bool, error) {
	m.saved = append([]byte(nil), data...)
	return &core.Blob{Pubkey: pubkey, Sha256: sha256, Url: url, Size: size, Type: mimeType,
		Uploaded: created, EncryptionMode: encryptionMode}, true, nil
}

// TestChunkedUpload_SQLiteEndToEnd runs a multi-chunk upload against the
// SQLite backend with the real queries: create session, upload every chunk,
// check progress, complete, and check the session is marked complete.
func TestChunkedUpload_SQLiteEndToEnd(t *testing.T) {
	tmp := t.TempDir()
	database, err := db.NewDB(filepath.Join(tmp, "chunked.sqlite3"), "../../db/migrations")
	require.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })

	blobs := &savingBlobStorage{}
	svc, err := NewChunkedUploadService(
		database,
		db.New(database),
		nil,
		blobs,
		nil,
		nil,
		&config.ChunkedUploadConfig{
			DefaultChunkSize: 4,
			MinChunkSize:     1,
			MaxChunkSize:     1024,
			TempDir:          filepath.Join(tmp, "chunks"),
		},
		"http://localhost",
		zap.NewNop(),
	)
	require.NoError(t, err)

	data := []byte("hello chunked world")
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])

	ctx := context.Background()
	sess, err := svc.CreateSession(ctx, &core.CreateSessionRequest{
		Pubkey:    "0000000000000000000000000000000000000000000000000000000000000001",
		TotalSize: int64(len(data)),
		Hash:      hash,
	})
	require.NoError(t, err)
	require.Equal(t, 5, sess.TotalChunks)

	for i := 0; i < sess.TotalChunks; i++ {
		end := (i + 1) * int(sess.ChunkSize)
		if end > len(data) {
			end = len(data)
		}
		_, err := svc.UploadChunk(ctx, sess.Session.ID, i, data[i*int(sess.ChunkSize):end])
		require.NoError(t, err, "chunk %d", i)
	}

	got, err := svc.GetSession(ctx, sess.Session.ID)
	require.NoError(t, err)
	assert.Equal(t, sess.TotalChunks, got.ChunksReceived, "chunks received recorded")
	assert.Equal(t, int64(len(data)), got.BytesReceived, "bytes received recorded")

	resp, err := svc.CompleteUpload(ctx, sess.Session.ID, "")
	require.NoError(t, err)
	assert.Equal(t, hash, resp.Blob.Sha256)
	assert.Equal(t, data, blobs.saved)

	done, err := svc.GetSession(ctx, sess.Session.ID)
	require.NoError(t, err)
	assert.Equal(t, "complete", string(done.Status))
}
