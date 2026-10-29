package gin

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/pkg/config"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/service"
)

type mediaTestServices struct {
	core.Services
	acr    *recordingACR
	blobs  *recordingBlobs
	quota  core.QuotaService        // nil: okQuota
	limits core.UploadLimitsService // nil: limits disabled
}

func (s mediaTestServices) ACR() core.ACRStorage                   { return s.acr }
func (s mediaTestServices) Blob() core.BlobStorage                 { return s.blobs }
func (s mediaTestServices) Media() core.MediaService               { return fixedMedia{} }
func (s mediaTestServices) Moderation() core.ModerationService { return notBlocked{} }

func (s mediaTestServices) Quota() core.QuotaService {
	if s.quota != nil {
		return s.quota
	}
	return okQuota{}
}

func (s mediaTestServices) UploadLimits() core.UploadLimitsService {
	if s.limits != nil {
		return s.limits
	}
	off, _ := service.NewUploadLimitsService(&config.UploadLimitsConfig{}, nil, nil, zap.NewNop())
	return off
}

type recordingACR struct {
	core.ACRStorage
	validated []string
	deny      error
}

func (a *recordingACR) Validate(_ context.Context, pubkey string, _ core.ACRResource) error {
	a.validated = append(a.validated, pubkey)
	return a.deny
}

// recordingBlobs fails the test through a nil embedded interface if the
// handler uses Save, which stores bytes without an owner reference.
type recordingBlobs struct {
	core.BlobStorage
	dedupSaves []string
}

func (b *recordingBlobs) SaveWithDedup(_ context.Context, pubkey, sha256, url string, size int64, mimeType string, _ []byte, created int64, mode core.EncryptionMode) (*core.Blob, bool, error) {
	b.dedupSaves = append(b.dedupSaves, pubkey)
	return &core.Blob{Pubkey: pubkey, Sha256: sha256, Url: url, Size: size, Type: mimeType, Uploaded: created, EncryptionMode: mode}, true, nil
}

type fixedMedia struct{ core.MediaService }

func (fixedMedia) IsSupported(string) bool { return true }
func (fixedMedia) ProcessImage(_ context.Context, r io.Reader, _ string, _ *core.MediaProcessOptions) (*core.MediaProcessResult, error) {
	data, _ := io.ReadAll(r)
	return &core.MediaProcessResult{Data: data, Width: 1, Height: 1, ContentType: "image/png", Hash: testBlobHash}, nil
}

type okQuota struct{ core.QuotaService }

func (okQuota) CheckQuota(context.Context, string, int64) error     { return nil }
func (okQuota) IncrementUsage(context.Context, string, int64) error { return nil }

type notBlocked struct{ core.ModerationService }

func (notBlocked) IsBlocked(context.Context, string) (bool, error) { return false, nil }

func serveMedia(t *testing.T, svc mediaTestServices, pubkey string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.PUT("/media", func(c *gin.Context) { c.Set("pk", pubkey) }, uploadMedia(svc, "http://localhost"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/media", bytes.NewReader(pngBytes)))
	return w
}

// A /media upload must run the access check (which provisions a first-time
// platform user) and record the uploader as an owner, so the photo shows in
// their list and they can delete it.
func TestMediaUpload_ProvisionsAndRecordsOwner(t *testing.T) {
	pk := "0000000000000000000000000000000000000000000000000000000000000001"
	svc := mediaTestServices{acr: &recordingACR{}, blobs: &recordingBlobs{}}

	w := serveMedia(t, svc, pk)

	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, []string{pk}, svc.acr.validated, "access check must run")
	assert.Equal(t, []string{pk}, svc.blobs.dedupSaves, "the uploader must get an owner reference")
}

func TestMediaUpload_AccessDenied(t *testing.T) {
	svc := mediaTestServices{acr: &recordingACR{deny: core.ErrUserBanned}, blobs: &recordingBlobs{}}

	w := serveMedia(t, svc, "0000000000000000000000000000000000000000000000000000000000000002")

	assert.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
	assert.Empty(t, svc.blobs.dedupSaves)
}
