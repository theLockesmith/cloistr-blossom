package service

import (
	"context"
	"os"
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

// typeCapturingBlobStorage records the MIME type CompleteUpload saves.
// Every other core.BlobStorage method panics through the embedded interface.
type typeCapturingBlobStorage struct {
	core.BlobStorage
	savedType string
}

func (m *typeCapturingBlobStorage) SaveWithDedup(
	_ context.Context,
	pubkey, sha256, url string,
	size int64,
	mimeType string,
	_ []byte,
	created int64,
	encryptionMode core.EncryptionMode,
) (*core.Blob, bool, error) {
	m.savedType = mimeType
	return &core.Blob{Pubkey: pubkey, Sha256: sha256, Url: url, Size: size, Type: mimeType,
		Uploaded: created, EncryptionMode: encryptionMode}, true, nil
}

// TestChunkedUpload_StoredType checks that a chunked upload stores the sniffed
// type, upgraded from the session's declared type only for allowlisted text
// formats. The declared type is client-supplied and must never be stored
// verbatim: HTML, SVG and XML declarations keep the sniffed type.
func TestChunkedUpload_StoredType(t *testing.T) {
	tmp := t.TempDir()
	database, err := db.NewDB(filepath.Join(tmp, "chunked.sqlite3"), "../../db/migrations")
	require.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })

	plainText := []byte("body { color: red }\n")
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00")

	tests := []struct {
		name     string
		data     []byte
		declared string
		want     string
	}{
		{"declared html over text stays text", plainText, "text/html", "text/plain; charset=utf-8"},
		{"declared svg over text stays text", plainText, "image/svg+xml", "text/plain; charset=utf-8"},
		{"declared xhtml over text stays text", plainText, "application/xhtml+xml", "text/plain; charset=utf-8"},
		{"declared xml over text stays text", plainText, "text/xml", "text/plain; charset=utf-8"},
		{"declared css over text is upgraded", plainText, "text/css", "text/css; charset=utf-8"},
		{"declared type on recognised bytes is ignored", png, "text/css", "image/png"},
		{"no declared type sniffs", png, "", "image/png"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blobs := &typeCapturingBlobStorage{}
			svc, err := NewChunkedUploadService(
				database,
				db.New(database),
				nil,
				blobs,
				nil,
				&config.ChunkedUploadConfig{
					DefaultChunkSize: 1024,
					MinChunkSize:     1,
					MaxChunkSize:     1024,
					TempDir:          filepath.Join(tmp, "chunks"),
				},
				"http://localhost",
				zap.NewNop(),
			)
			require.NoError(t, err)

			ctx := context.Background()
			sess, err := svc.CreateSession(ctx, &core.CreateSessionRequest{
				Pubkey:    "0000000000000000000000000000000000000000000000000000000000000001",
				TotalSize: int64(len(tt.data)),
				MimeType:  tt.declared,
			})
			require.NoError(t, err)
			require.Equal(t, 1, sess.TotalChunks)

			_, err = svc.UploadChunk(ctx, sess.Session.ID, 0, tt.data)
			require.NoError(t, err)
			// On SQLite the session-progress UPDATE binds its $N placeholders
			// out of order and silently matches no row (production runs
			// Postgres), so record the received chunk directly.
			_, err = database.Exec(`UPDATE upload_sessions SET chunks_received = 1 WHERE id = ?`, sess.Session.ID)
			require.NoError(t, err)
			_, err = svc.CompleteUpload(ctx, sess.Session.ID, "")
			require.NoError(t, err)

			assert.Equal(t, tt.want, blobs.savedType)
		})
	}

	_ = os.RemoveAll(filepath.Join(tmp, "chunks"))
}
