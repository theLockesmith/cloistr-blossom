package gin

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
)

// A blocklist lookup error used to let the upload through on every path. A
// refused upload can be retried; a banned key's stored content cannot be
// taken back, so a lookup error now refuses with 503.

const failClosedPK = "0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d"

type blocklistDownServices struct {
	core.Services
}

func (blocklistDownServices) Moderation() core.ModerationService {
	return &fakeBlocklist{err: errors.New("blocklist unavailable")}
}

func TestUploadPaths_BlocklistLookupErrorRefuses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := blocklistDownServices{}
	paths := map[string]gin.HandlerFunc{
		"/upload": uploadBlob(svc, "http://localhost"),
		"/mirror": mirrorBlob(svc, "http://localhost"),
		"/media":  uploadMedia(svc, "http://localhost"),
	}
	for p, h := range paths {
		t.Run(p, func(t *testing.T) {
			r := gin.New()
			r.Use(gin.Recovery())
			r.PUT(p, func(c *gin.Context) { c.Set("pk", failClosedPK); c.Set("x", xHashA) }, h)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, p, bytes.NewReader([]byte("hello"))))
			assert.Equal(t, http.StatusServiceUnavailable, w.Code, "body %s", w.Body)
		})
	}
}

func tusFailClosedRouter(t *testing.T, dir string, mod core.ModerationService, blobs core.BlobStorage) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h, err := NewTusHandler(blobs, nil, mod, nil, nil, TusConfig{TempDir: dir}, zap.NewNop())
	require.NoError(t, err)
	r := gin.New()
	RegisterTusRoutes(r, h, func(c *gin.Context) { c.Set(ctxPubkey, failClosedPK) }, zap.NewNop())
	return r
}

func tusPatch(r *gin.Engine, id string, offset int, chunk string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPatch, "/files/"+id, bytes.NewReader([]byte(chunk)))
	req.Header.Set("Content-Type", "application/offset+octet-stream")
	req.Header.Set("Upload-Offset", strconv.Itoa(offset))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestTusUpload_BlocklistFailsClosed(t *testing.T) {
	t.Run("create refuses on a lookup error", func(t *testing.T) {
		mod := &fakeBlocklist{err: errors.New("blocklist unavailable")}
		w := tusCreateRequest(tusFailClosedRouter(t, t.TempDir(), mod, nil), "5")
		assert.Equal(t, http.StatusServiceUnavailable, w.Code, "body %s", w.Body)
	})

	t.Run("finish refuses on a lookup error and keeps the upload to retry", func(t *testing.T) {
		dir := t.TempDir()
		mod := &fakeBlocklist{}
		blobs := &tusSaveRecorder{}
		r := tusFailClosedRouter(t, dir, mod, blobs)
		created := tusCreateRequest(r, "5")
		require.Equal(t, http.StatusCreated, created.Code, "body %s", created.Body)
		id := path.Base(created.Header().Get("Location"))

		mod.err = errors.New("blocklist unavailable")
		w := tusPatch(r, id, 0, "hello")
		assert.Equal(t, http.StatusServiceUnavailable, w.Code, "body %s", w.Body)
		assert.False(t, blobs.saved, "nothing may be stored while the blocklist is unknown")
		_, err := os.Stat(filepath.Join(dir, id))
		assert.NoError(t, err, "the upload is kept so the client can retry")
	})

	t.Run("a key blocked mid-upload is stopped at the next chunk", func(t *testing.T) {
		dir := t.TempDir()
		mod := &fakeBlocklist{blocked: map[string]bool{}}
		r := tusFailClosedRouter(t, dir, mod, &tusSaveRecorder{})
		created := tusCreateRequest(r, "15")
		require.Equal(t, http.StatusCreated, created.Code, "body %s", created.Body)
		id := path.Base(created.Header().Get("Location"))

		require.Equal(t, http.StatusNoContent, tusPatch(r, id, 0, "12345").Code)

		mod.blocked[failClosedPK] = true
		w := tusPatch(r, id, 5, "67890")
		assert.Equal(t, http.StatusForbidden, w.Code, "body %s", w.Body)
		_, err := os.Stat(filepath.Join(dir, id))
		assert.True(t, os.IsNotExist(err), "a blocked key's partial upload is removed")
	})

	t.Run("a lookup error mid-upload refuses the chunk", func(t *testing.T) {
		dir := t.TempDir()
		mod := &fakeBlocklist{}
		r := tusFailClosedRouter(t, dir, mod, &tusSaveRecorder{})
		created := tusCreateRequest(r, "15")
		require.Equal(t, http.StatusCreated, created.Code, "body %s", created.Body)
		id := path.Base(created.Header().Get("Location"))

		mod.err = errors.New("blocklist unavailable")
		w := tusPatch(r, id, 0, "12345")
		assert.Equal(t, http.StatusServiceUnavailable, w.Code, "body %s", w.Body)
	})
}
