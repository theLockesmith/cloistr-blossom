package gin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
)

// blobHeaderServices serves one stored blob. Every core.Services method the
// GET/HEAD handlers do not need panics through the embedded nil interface.
type blobHeaderServices struct {
	core.Services
	blobs *oneBlobStorage
}

func (s blobHeaderServices) Blob() core.BlobStorage { return s.blobs }
func (s blobHeaderServices) CDN() core.CDNService   { return noRedirectCDN{} }
func (s blobHeaderServices) Federation() core.FederationService {
	return nil
}

type noRedirectCDN struct{ core.CDNService }

func (noRedirectCDN) ShouldRedirect() bool { return false }

// oneBlobStorage holds a single blob and counts full-content reads, so the
// test can check HEAD does not load the bytes when the stored type is usable.
type oneBlobStorage struct {
	core.BlobStorage
	blob      core.Blob
	fullReads int
}

func (m *oneBlobStorage) GetFromHash(_ context.Context, sha256 string) (*core.Blob, error) {
	if sha256 != m.blob.Sha256 {
		return nil, core.ErrBlobNotFound
	}
	m.fullReads++
	b := m.blob
	return &b, nil
}

func (m *oneBlobStorage) GetDescriptor(_ context.Context, sha256 string) (*core.Blob, error) {
	if sha256 != m.blob.Sha256 {
		return nil, core.ErrBlobNotFound
	}
	b := m.blob
	b.Blob = nil
	return &b, nil
}

const (
	testBlobHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	sandboxCSP   = "sandbox; default-src 'none'; style-src 'unsafe-inline'; media-src 'self'"
)

var (
	htmlBytes = []byte("<!DOCTYPE html><html><body><script>alert(1)</script></body></html>")
	pngBytes  = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00")
)

func TestBlobResponseHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		storedType string
		body       []byte
		wantType   string
		wantInline bool
		// wantHeadFullRead is true when HEAD must load the bytes to sniff.
		wantHeadFullRead bool
	}{
		{"html bytes stored as png stay png", "image/png", htmlBytes, "image/png", true, false},
		{"stored text/html", "text/html", htmlBytes, "text/html", false, false},
		{"stored svg", "image/svg+xml", []byte("<svg/>"), "image/svg+xml", false, false},
		{"stored svg with params", "image/svg+xml; charset=utf-8", []byte("<svg/>"), "image/svg+xml; charset=utf-8", false, false},
		{"stored non-standard svg", "image/svg", []byte("<svg/>"), "image/svg", false, false},
		{"stored xhtml", "application/xhtml+xml", htmlBytes, "application/xhtml+xml", false, false},
		{"stored text/xml", "text/xml", []byte("<a/>"), "text/xml", false, false},
		{"stored javascript", "text/javascript", []byte("alert(1)"), "text/javascript", false, false},
		{"stored css", "text/css", []byte("body{}"), "text/css", false, false},
		{"stored type case is normalised", "Image/PNG", pngBytes, "image/png", true, false},
		{"video inline", "video/mp4", []byte("x"), "video/mp4", true, false},
		{"audio inline", "audio/mpeg", []byte("x"), "audio/mpeg", true, false},
		{"pdf inline", "application/pdf", []byte("%PDF-1.4"), "application/pdf", true, false},
		{"malformed stored type falls back to sniff", "not a type", pngBytes, "image/png", true, true},
		{"subtype-less stored type falls back to sniff", "image", pngBytes, "image/png", true, true},
		{"empty stored type sniffs html", "", htmlBytes, "text/html; charset=utf-8", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &oneBlobStorage{blob: core.Blob{
				Sha256: testBlobHash,
				Type:   tt.storedType,
				Size:   int64(len(tt.body)),
				Blob:   tt.body,
			}}
			svc := blobHeaderServices{blobs: store}
			r := gin.New()
			r.GET("/:hash", getBlob(svc))
			r.HEAD("/:hash", hasBlob(svc))

			get := httptest.NewRecorder()
			r.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/"+testBlobHash, nil))
			require.Equal(t, http.StatusOK, get.Code)
			assert.Equal(t, tt.body, get.Body.Bytes())

			readsBeforeHead := store.fullReads
			head := httptest.NewRecorder()
			r.ServeHTTP(head, httptest.NewRequest(http.MethodHead, "/"+testBlobHash, nil))
			require.Equal(t, http.StatusOK, head.Code)
			assert.Empty(t, head.Body.Bytes())
			assert.Equal(t, tt.wantHeadFullRead, store.fullReads > readsBeforeHead,
				"HEAD full-content read")

			for _, w := range []*httptest.ResponseRecorder{get, head} {
				h := w.Header()
				assert.Equal(t, tt.wantType, h.Get("Content-Type"))
				assert.Equal(t, "nosniff", h.Get("X-Content-Type-Options"))
				assert.Equal(t, sandboxCSP, h.Get("Content-Security-Policy"))
				assert.Equal(t, strconv.Itoa(len(tt.body)), h.Get("Content-Length"))
				disp := h.Get("Content-Disposition")
				if tt.wantInline {
					assert.Equal(t, "inline", disp)
				} else {
					assert.True(t, strings.HasPrefix(disp, "attachment;"), "Content-Disposition = %q", disp)
				}
			}

			for _, k := range []string{"Content-Type", "Content-Length", "Content-Disposition",
				"Content-Security-Policy", "X-Content-Type-Options"} {
				assert.Equal(t, get.Header().Values(k), head.Header().Values(k), "HEAD vs GET %s", k)
			}
		})
	}
}

func TestBlobHead_MissingBlobIs404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := blobHeaderServices{blobs: &oneBlobStorage{blob: core.Blob{Sha256: testBlobHash}}}
	r := gin.New()
	r.HEAD("/:hash", hasBlob(svc))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodHead, "/"+strings.Repeat("b", 64), nil))
	assert.Equal(t, http.StatusNotFound, w.Code)
}
