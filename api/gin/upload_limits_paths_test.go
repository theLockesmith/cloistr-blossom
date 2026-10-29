package gin

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/pkg/config"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/service"
)

// tierLimits returns an enabled limits service whose tier (Named, the
// standalone default) accepts only the given type prefixes up to maxBytes.
func tierLimits(t *testing.T, maxBytes int64, prefixes ...string) core.UploadLimitsService {
	t.Helper()
	limits, err := service.NewUploadLimitsService(&config.UploadLimitsConfig{
		Enabled: true,
		Named:   config.UploadTierLimitsConfig{MaxFileBytes: maxBytes, AllowedTypePrefixes: prefixes},
	}, nil, nil, zap.NewNop())
	require.NoError(t, err)
	return limits
}

func TestMediaUpload_AppliesTierLimits(t *testing.T) {
	gin.SetMode(gin.TestMode)
	blobs := &recordingBlobs{}
	svc := mediaTestServices{acr: &recordingACR{}, blobs: blobs, limits: tierLimits(t, 1<<20, "video/")}

	w := serveMedia(t, svc, "0000000000000000000000000000000000000000000000000000000000000001")

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Empty(t, blobs.dedupSaves)
	assert.Contains(t, w.Body.String(), core.ErrUploadMimeTypeNotAllowed.Error())
}

func TestTusUpload_AppliesTierLimits(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pk := "0000000000000000000000000000000000000000000000000000000000000001"

	t.Run("declared length over the tier cap is refused at creation", func(t *testing.T) {
		h, err := NewTusHandler(nil, nil, tierLimits(t, 8, "image/"), TusConfig{TempDir: t.TempDir()}, zap.NewNop())
		require.NoError(t, err)
		r := gin.New()
		r.POST("/files", func(c *gin.Context) { c.Set("pubkey", pk) }, h.tusCreate())

		req := httptest.NewRequest(http.MethodPost, "/files", nil)
		req.Header.Set("Tus-Resumable", "1.0.0")
		req.Header.Set("Upload-Length", "9")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	})

	t.Run("finished upload of a refused type is not stored", func(t *testing.T) {
		dir := t.TempDir()
		h, err := NewTusHandler(nil, nil, tierLimits(t, 1<<20, "image/"), TusConfig{TempDir: dir}, zap.NewNop())
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "u1"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "u1", "data"), htmlBytes, 0o644))

		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPatch, "/files/u1", nil)
		// blobService is nil, so reaching the save would panic.
		_, err = h.finalizeUpload(c, "u1", &tusUploadInfo{ID: "u1", Pubkey: pk, Metadata: map[string]string{"filetype": "image/png"}}, "")
		assert.ErrorIs(t, err, core.ErrUploadMimeTypeNotAllowed)
	})
}
