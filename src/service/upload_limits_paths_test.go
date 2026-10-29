package service

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/db"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/cache"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/ratelimit"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/pkg/config"
)

var (
	tierTestPNG  = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00")
	tierTestText = []byte("plain words, not an image")
)

// newImageOnlyLimits returns an enabled limits service whose tier (Named, the
// standalone default) accepts only images up to maxBytes, at most perDay a day.
func newImageOnlyLimits(t *testing.T, maxBytes int64, perDay int) core.UploadLimitsService {
	t.Helper()
	limits, err := NewUploadLimitsService(&config.UploadLimitsConfig{
		Enabled: true,
		Named: config.UploadTierLimitsConfig{
			MaxFileBytes:        maxBytes,
			AllowedTypePrefixes: []string{"image/"},
			UploadsPerDay:       perDay,
		},
	}, nil, ratelimit.NewRateLimiter(cache.NewMemoryCache(1<<20)), zap.NewNop())
	require.NoError(t, err)
	return limits
}

func TestCheckUpload(t *testing.T) {
	ctx := context.Background()
	pk := "0000000000000000000000000000000000000000000000000000000000000001"

	t.Run("disabled allows anything", func(t *testing.T) {
		limits, err := NewUploadLimitsService(&config.UploadLimitsConfig{}, nil, nil, zap.NewNop())
		require.NoError(t, err)
		assert.NoError(t, limits.CheckUpload(ctx, pk, tierTestText))
	})

	t.Run("refused type", func(t *testing.T) {
		limits := newImageOnlyLimits(t, 1<<20, 0)
		assert.ErrorIs(t, limits.CheckUpload(ctx, pk, tierTestText), core.ErrUploadMimeTypeNotAllowed)
	})

	t.Run("refused size", func(t *testing.T) {
		limits := newImageOnlyLimits(t, 8, 0)
		assert.ErrorIs(t, limits.CheckUpload(ctx, pk, tierTestPNG), core.ErrUploadFileTooLarge)
	})

	t.Run("daily cap, and a refused upload does not count", func(t *testing.T) {
		limits := newImageOnlyLimits(t, 1<<20, 2)
		require.Error(t, limits.CheckUpload(ctx, pk, tierTestText))
		require.NoError(t, limits.CheckUpload(ctx, pk, tierTestPNG))
		require.NoError(t, limits.CheckUpload(ctx, pk, tierTestPNG))
		assert.ErrorIs(t, limits.CheckUpload(ctx, pk, tierTestPNG), core.ErrDailyUploadLimitReached)
	})
}

func TestBatchUpload_AppliesTierLimits(t *testing.T) {
	svc, blobs, _, _, _ := setupBatchTest(t)
	svc.limits = newImageOnlyLimits(t, 1<<20, 0)

	resp, err := svc.Upload(context.Background(), &core.BatchUploadRequest{
		Pubkey: "testpubkey",
		Files: []core.BatchUploadFile{
			{Filename: "a.png", MimeType: "image/png", Size: int64(len(tierTestPNG)), Reader: bytes.NewReader(tierTestPNG)},
			{Filename: "b.txt", MimeType: "image/png", Size: int64(len(tierTestText)), Reader: bytes.NewReader(tierTestText)},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, resp.SuccessCount)
	assert.True(t, resp.Results[0].Success)
	assert.False(t, resp.Results[1].Success, "text declared as an image is still text")
	assert.Equal(t, core.ErrUploadMimeTypeNotAllowed.Error(), resp.Results[1].Error)
	assert.Len(t, blobs.blobs, 1)
}

func TestChunkedUpload_AppliesTierLimits(t *testing.T) {
	tmp := t.TempDir()
	database, err := db.NewDB(filepath.Join(tmp, "chunked.sqlite3"), "../../db/migrations")
	require.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })

	newSvc := func(limits core.UploadLimitsService) core.ChunkedUploadService {
		svc, err := NewChunkedUploadService(database, db.New(database), nil, &typeCapturingBlobStorage{}, nil, limits,
			&config.ChunkedUploadConfig{DefaultChunkSize: 1024, MinChunkSize: 1, MaxChunkSize: 1024, TempDir: filepath.Join(tmp, "chunks")},
			"http://localhost", zap.NewNop())
		require.NoError(t, err)
		return svc
	}
	ctx := context.Background()
	pk := "0000000000000000000000000000000000000000000000000000000000000001"

	t.Run("declared size over the tier cap is refused at session start", func(t *testing.T) {
		svc := newSvc(newImageOnlyLimits(t, 8, 0))
		_, err := svc.CreateSession(ctx, &core.CreateSessionRequest{Pubkey: pk, TotalSize: 9})
		assert.ErrorIs(t, err, core.ErrUploadFileTooLarge)
	})

	t.Run("assembled bytes of a refused type are not stored", func(t *testing.T) {
		svc := newSvc(newImageOnlyLimits(t, 1<<20, 0))
		sess, err := svc.CreateSession(ctx, &core.CreateSessionRequest{Pubkey: pk, TotalSize: int64(len(tierTestText)), MimeType: "image/png"})
		require.NoError(t, err)
		_, err = svc.UploadChunk(ctx, sess.Session.ID, 0, tierTestText)
		require.NoError(t, err)
		// SQLite binds the progress UPDATE's placeholders out of order (see
		// TestChunkedUpload_StoredType), so record the chunk directly.
		_, err = database.Exec(`UPDATE upload_sessions SET chunks_received = 1 WHERE id = ?`, sess.Session.ID)
		require.NoError(t, err)

		_, err = svc.CompleteUpload(ctx, sess.Session.ID, "")
		assert.ErrorIs(t, err, core.ErrUploadMimeTypeNotAllowed)
	})
}
