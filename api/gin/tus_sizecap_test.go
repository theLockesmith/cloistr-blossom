package gin

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// Resumable uploads were capped at a hardcoded 100 GiB instead of the
// server's max_upload_size_bytes, which every other upload path enforces.

func tusSizeCapRouter(t *testing.T, dir string, maxFile int64, blobs *tusSaveRecorder) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h, err := NewTusHandler(blobs, nil, nil, nil, nil, TusConfig{TempDir: dir, MaxFileBytes: maxFile}, zap.NewNop())
	require.NoError(t, err)
	r := gin.New()
	auth := func(c *gin.Context) { c.Set(ctxPubkey, tusOwnerPK) }
	RegisterTusRoutes(r, h, auth, zap.NewNop())
	return r
}

func TestTusSizeCap_CreateRefusesOverMax(t *testing.T) {
	r := tusSizeCapRouter(t, t.TempDir(), 10, nil)

	w := tusCreateRequest(r, "11")
	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code, "body %s", w.Body)
	assert.Equal(t, "10", w.Header().Get("Tus-Max-Size"), "advertise the real cap")

	w = tusCreateRequest(r, "10")
	assert.Equal(t, http.StatusCreated, w.Code, "body %s", w.Body)
}

func TestTusSizeCap_FinishRefusesOverMax(t *testing.T) {
	// Created under a cap of 10, finished after the cap became 5 (e.g. a
	// config change and restart in between).
	dir := t.TempDir()
	created := tusCreateRequest(tusSizeCapRouter(t, dir, 10, nil), "8")
	require.Equal(t, http.StatusCreated, created.Code, "body %s", created.Body)
	id := path.Base(created.Header().Get("Location"))

	blobs := &tusSaveRecorder{}
	r := tusSizeCapRouter(t, dir, 5, blobs)
	req := httptest.NewRequest(http.MethodPatch, "/files/"+id, bytes.NewReader([]byte("12345678")))
	req.Header.Set("Content-Type", "application/offset+octet-stream")
	req.Header.Set("Upload-Offset", "0")
	req.Header.Set("Content-Length", strconv.Itoa(8))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code, "body %s", w.Body)
	assert.False(t, blobs.saved, "an over-cap upload must not reach storage")
}
