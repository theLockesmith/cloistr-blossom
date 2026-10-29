package gin

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// The upload id names a directory under the tus temp dir. gin hands ".." and
// "." to the handler as an id, so an unchecked id let DELETE remove the temp
// dir's parent, or every upload at once. And DELETE did not check who owned
// the upload.

const (
	tusOwnerPK = "0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a"
	tusOtherPK = "0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b"
)

// tusIDRouter registers the tus routes with a stand-in for auth that sets the
// caller from the X-Test-Caller header.
func tusIDRouter(t *testing.T, dir string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h, err := NewTusHandler(nil, nil, nil, nil, nil, TusConfig{TempDir: dir}, zap.NewNop())
	require.NoError(t, err)
	r := gin.New()
	auth := func(c *gin.Context) { c.Set(handlerPubkeyKey, c.GetHeader("X-Test-Caller")) }
	RegisterTusRoutes(r, h, auth, zap.NewNop())
	return r
}

func tusDo(r *gin.Engine, method, target, caller string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	if caller != "" {
		req.Header.Set("X-Test-Caller", caller)
	}
	if method == http.MethodPost {
		req.Header.Set("Upload-Length", "5")
	}
	if method == http.MethodPatch {
		req.Header.Set("Content-Type", "application/offset+octet-stream")
		req.Header.Set("Upload-Offset", "0")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestTusUploadID_DotSegmentsAreNotUploads(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "tus")
	sentinel := filepath.Join(root, "sentinel")
	require.NoError(t, os.WriteFile(sentinel, []byte("keep"), 0o600))

	r := tusIDRouter(t, dir)
	created := tusDo(r, http.MethodPost, "/files", tusOwnerPK)
	require.Equal(t, http.StatusCreated, created.Code, "body %s", created.Body)
	id := path.Base(created.Header().Get("Location"))

	for _, bad := range []string{"..", "%2e%2e", "."} {
		for _, method := range []string{http.MethodDelete, http.MethodHead, http.MethodPatch} {
			w := tusDo(r, method, "/files/"+bad, tusOwnerPK)
			assert.Equal(t, http.StatusNotFound, w.Code, "%s /files/%s", method, bad)
		}
	}

	_, err := os.Stat(sentinel)
	assert.NoError(t, err, "DELETE /files/.. must not remove the temp dir's parent")
	_, err = os.Stat(filepath.Join(dir, id))
	assert.NoError(t, err, "DELETE /files/. must not remove other uploads")
}

func TestTusDelete_OnlyTheOwnerCanTerminate(t *testing.T) {
	dir := t.TempDir()
	r := tusIDRouter(t, dir)

	created := tusDo(r, http.MethodPost, "/files", tusOwnerPK)
	require.Equal(t, http.StatusCreated, created.Code, "body %s", created.Body)
	id := path.Base(created.Header().Get("Location"))

	w := tusDo(r, http.MethodDelete, "/files/"+id, tusOtherPK)
	assert.Equal(t, http.StatusForbidden, w.Code, "another key must not terminate the upload")
	_, err := os.Stat(filepath.Join(dir, id))
	require.NoError(t, err, "the upload must survive another key's DELETE")

	w = tusDo(r, http.MethodDelete, "/files/"+id, tusOwnerPK)
	assert.Equal(t, http.StatusNoContent, w.Code, "body %s", w.Body)
	_, err = os.Stat(filepath.Join(dir, id))
	assert.True(t, os.IsNotExist(err), "the owner's DELETE removes the upload")
}
