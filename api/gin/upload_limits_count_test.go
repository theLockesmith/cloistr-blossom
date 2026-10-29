package gin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/cache"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/ratelimit"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/pkg/config"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/service"
)

type fullQuota struct{ core.QuotaService }

func (fullQuota) CheckQuota(context.Context, string, int64) error { return core.ErrQuotaExceeded }

// A /media upload refused for quota must not use up one of the day's uploads:
// with a cap of one a day, the next upload, once there is room, succeeds.
func TestMediaUpload_QuotaRefusalDoesNotUseDailySlot(t *testing.T) {
	limits, err := service.NewUploadLimitsService(&config.UploadLimitsConfig{
		Enabled: true,
		Named:   config.UploadTierLimitsConfig{UploadsPerDay: 1},
	}, nil, ratelimit.NewRateLimiter(cache.NewMemoryCache(1<<20)), zap.NewNop())
	require.NoError(t, err)
	pk := "0000000000000000000000000000000000000000000000000000000000000003"

	refused := serveMedia(t, mediaTestServices{acr: &recordingACR{}, blobs: &recordingBlobs{}, quota: fullQuota{}, limits: limits}, pk)
	require.Equal(t, http.StatusPaymentRequired, refused.Code, refused.Body.String())

	blobs := &recordingBlobs{}
	stored := serveMedia(t, mediaTestServices{acr: &recordingACR{}, blobs: blobs, limits: limits}, pk)
	assert.Equal(t, http.StatusOK, stored.Code, stored.Body.String())
	assert.Len(t, blobs.dedupSaves, 1)
}

// The tus finaliser must check the type it stores, not the sniffed one.
func TestTusUpload_ChecksStoredType(t *testing.T) {
	dir := t.TempDir()
	h, err := NewTusHandler(nil, nil, nil, tierLimits(t, 1<<20, "text/plain"), TusConfig{TempDir: dir}, zap.NewNop())
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "u1"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "u1", "data"), []byte("console.log('hello')"), 0o644))

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPatch, "/files/u1", nil)
	// blobService is nil, so reaching the save would panic.
	_, err = h.finalizeUpload(c, "u1", &tusUploadInfo{ID: "u1", Pubkey: "0000000000000000000000000000000000000000000000000000000000000001", Metadata: map[string]string{"filetype": "text/javascript"}}, "")
	assert.ErrorIs(t, err, core.ErrUploadMimeTypeNotAllowed)
}
