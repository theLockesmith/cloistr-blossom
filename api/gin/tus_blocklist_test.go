package gin

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
)

// tusBlocklist is a blocklist the test can change mid-upload.
type tusBlocklist struct {
	core.ModerationService
	blocked map[string]bool
}

func (m *tusBlocklist) IsBlocked(_ context.Context, pk string) (bool, error) {
	return m.blocked[pk], nil
}

// tusSaveRecorder records whether a finished upload reached storage.
type tusSaveRecorder struct {
	core.BlobStorage
	saved bool
}

func (s *tusSaveRecorder) SaveWithDedup(_ context.Context, pk, hash, url string, size int64, mime string, _ []byte, created int64, _ core.EncryptionMode) (*core.Blob, bool, error) {
	s.saved = true
	return &core.Blob{Pubkey: pk, Sha256: hash, Url: url, Size: size, Type: mime, Uploaded: created}, true, nil
}

func tusBlocklistRouter(t *testing.T, mod core.ModerationService, blobs core.BlobStorage, store core.TusUploadStore, pk string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h, err := NewTusHandler(blobs, nil, mod, nil, nil, TusConfig{Store: store}, zap.NewNop())
	require.NoError(t, err)
	r := gin.New()
	r.POST("/files", func(c *gin.Context) { c.Set(ctxPubkey, pk) }, h.tusCreate())
	r.PATCH("/files/:id", h.tusPatch())
	return r
}

func tusCreateRequest(r *gin.Engine, length string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/files", nil)
	req.Header.Set("Upload-Length", length)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestTusUpload_RefusesBlocklistedKey(t *testing.T) {
	const pk = "b10cb10cb10cb10cb10cb10cb10cb10cb10cb10cb10cb10cb10cb10cb10cb10c"

	t.Run("a blocklisted key cannot start an upload", func(t *testing.T) {
		mod := &tusBlocklist{blocked: map[string]bool{pk: true}}
		store := &countingTusStore{TusUploadStore: newTusTestStore(t)}
		w := tusCreateRequest(tusBlocklistRouter(t, mod, nil, store, pk), "5")
		assert.Equal(t, http.StatusForbidden, w.Code, "body %s", w.Body)
		assert.Zero(t, store.creates, "no upload should be created")
	})

	t.Run("an unblocked key still can", func(t *testing.T) {
		mod := &tusBlocklist{blocked: map[string]bool{}}
		w := tusCreateRequest(tusBlocklistRouter(t, mod, nil, newTusTestStore(t), pk), "5")
		assert.Equal(t, http.StatusCreated, w.Code, "body %s", w.Body)
	})

	t.Run("an upload started before the block cannot be finished", func(t *testing.T) {
		store := newTusTestStore(t)
		mod := &tusBlocklist{blocked: map[string]bool{}}
		blobs := &tusSaveRecorder{}
		r := tusBlocklistRouter(t, mod, blobs, store, pk)

		created := tusCreateRequest(r, "5")
		require.Equal(t, http.StatusCreated, created.Code, "body %s", created.Body)
		id := path.Base(created.Header().Get("Location"))

		mod.blocked[pk] = true

		req := httptest.NewRequest(http.MethodPatch, "/files/"+id, bytes.NewReader([]byte("hello")))
		req.Header.Set("Content-Type", "application/offset+octet-stream")
		req.Header.Set("Upload-Offset", "0")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusForbidden, w.Code, "body %s", w.Body)
		assert.False(t, blobs.saved, "a blocklisted key's upload must not reach storage")
		_, err := store.Get(context.Background(), id)
		assert.ErrorIs(t, err, core.ErrTusUploadNotFound, "the abandoned upload should be removed")
	})
}
