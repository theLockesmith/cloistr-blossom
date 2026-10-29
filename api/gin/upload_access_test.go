package gin

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
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

// Batch upload checked neither the blocklist nor the platform access rule
// (which refuses a disabled platform account), and tus did not check the
// access rule. PUT /upload checks both.

type fakeBlocklist struct {
	core.ModerationService
	blocked map[string]bool
	err     error
}

func (m *fakeBlocklist) IsBlocked(_ context.Context, pk string) (bool, error) {
	if m.err != nil {
		return false, m.err
	}
	return m.blocked[pk], nil
}

type fakeACR struct {
	denied map[string]bool
}

func (a *fakeACR) Validate(_ context.Context, pk string, _ core.ACRResource) error {
	if a.denied[pk] {
		return errors.New("unauthorized")
	}
	return nil
}

type recordingBatch struct {
	core.BatchService
	uploads int
}

func (b *recordingBatch) Upload(_ context.Context, req *core.BatchUploadRequest) (*core.BatchUploadResponse, error) {
	b.uploads++
	return &core.BatchUploadResponse{TotalFiles: len(req.Files)}, nil
}

func batchUploadBody(t *testing.T) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("files", "a.txt")
	require.NoError(t, err)
	_, _ = fw.Write([]byte("hello"))
	require.NoError(t, mw.Close())
	return &body, mw.FormDataContentType()
}

func TestBatchUpload_AccessChecks(t *testing.T) {
	const pk = "0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c"
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name   string
		mod    *fakeBlocklist
		acr    *fakeACR
		status int
	}{
		{"allowed key uploads", &fakeBlocklist{}, &fakeACR{}, http.StatusOK},
		{"blocklisted key is refused", &fakeBlocklist{blocked: map[string]bool{pk: true}}, &fakeACR{}, http.StatusForbidden},
		{"disabled platform account is refused", &fakeBlocklist{}, &fakeACR{denied: map[string]bool{pk: true}}, http.StatusForbidden},
		{"blocklist lookup failure refuses", &fakeBlocklist{err: errors.New("db down")}, &fakeACR{}, http.StatusServiceUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			batch := &recordingBatch{}
			c := NewBatchController(batch, tc.mod, tc.acr)
			r := gin.New()
			r.POST("/batch/upload", func(c *gin.Context) { c.Set(ctxPubkey, pk) }, c.BatchUpload)

			body, ct := batchUploadBody(t)
			req := httptest.NewRequest(http.MethodPost, "/batch/upload", body)
			req.Header.Set("Content-Type", ct)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			assert.Equal(t, tc.status, w.Code, "body %s", w.Body)
			if tc.status != http.StatusOK {
				assert.Zero(t, batch.uploads, "a refused key must not reach the batch service")
			}
		})
	}
}

func TestTusUpload_PlatformAccessRule(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := func(t *testing.T, store core.TusUploadStore, acr core.ACRStorage, blobs core.BlobStorage) *gin.Engine {
		h, err := NewTusHandler(blobs, nil, nil, acr, nil, TusConfig{Store: store}, zap.NewNop())
		require.NoError(t, err)
		r := gin.New()
		RegisterTusRoutes(r, h, func(c *gin.Context) { c.Set(ctxPubkey, tusOwnerPK) }, zap.NewNop())
		return r
	}

	t.Run("a denied key cannot start an upload", func(t *testing.T) {
		w := tusCreateRequest(router(t, newTusTestStore(t), &fakeACR{denied: map[string]bool{tusOwnerPK: true}}, nil), "5")
		assert.Equal(t, http.StatusForbidden, w.Code, "body %s", w.Body)
	})

	t.Run("a key denied mid-upload cannot finish", func(t *testing.T) {
		store := newTusTestStore(t)
		acr := &fakeACR{denied: map[string]bool{}}
		blobs := &tusSaveRecorder{}
		r := router(t, store, acr, blobs)

		created := tusCreateRequest(r, "5")
		require.Equal(t, http.StatusCreated, created.Code, "body %s", created.Body)
		id := path.Base(created.Header().Get("Location"))

		acr.denied[tusOwnerPK] = true
		req := httptest.NewRequest(http.MethodPatch, "/files/"+id, bytes.NewReader([]byte("hello")))
		req.Header.Set("Content-Type", "application/offset+octet-stream")
		req.Header.Set("Upload-Offset", "0")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusForbidden, w.Code, "body %s", w.Body)
		assert.False(t, blobs.saved, "a denied key's upload must not reach storage")
	})
}
