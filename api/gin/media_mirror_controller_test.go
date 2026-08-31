package gin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/pkg/urlsign"
)

// stubServices satisfies core.Services by embedding the interface: every method
// this test does not use panics if called, which is the desired behaviour for a
// stub -- an accidental dependency shows up as a failure rather than a nil.
type stubServices struct {
	core.Services
	mirror core.MediaMirrorService
}

func (s stubServices) MediaMirror() core.MediaMirrorService { return s.mirror }

// stubMirror returns whatever the test tells it to.
type stubMirror struct {
	enabled bool
	media   *core.MirroredMedia
	err     error

	signed   []core.SignedMirrorURL
	rejected []core.RejectedMirrorURL
	signErr  error
}

func (m *stubMirror) IsEnabled() bool { return m.enabled }

func (m *stubMirror) SignURLs(context.Context, []string) ([]core.SignedMirrorURL, []core.RejectedMirrorURL, error) {
	return m.signed, m.rejected, m.signErr
}

func (m *stubMirror) VerifyAndGet(context.Context, string, int64, string) (*core.MirroredMedia, error) {
	return m.media, m.err
}

func (m *stubMirror) Evict(context.Context) (*core.MirrorEvictResult, error) { return nil, nil }
func (m *stubMirror) StartWorker(context.Context)                            {}
func (m *stubMirror) StopWorker()                                            {}

func mirrorRouter(t *testing.T, m core.MediaMirrorService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	svc := stubServices{mirror: m}
	r.GET("/media/mirror", getMirroredMedia(svc, zap.NewNop()))
	r.POST("/media/mirror/sign", signMirrorURLs(svc, zap.NewNop()))
	return r
}

func doGet(t *testing.T, r *gin.Engine, query string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/media/mirror"+query, nil))
	return w
}

// The contract the client depends on: three failure classes, three statuses,
// three codes. Collapsing any pair of these is what produces an emoji that is
// "just broken" with no way to tell whether reloading would help.
func TestMirrorErrorsAreDistinguishable(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode int
		wantBody string
	}{
		{
			name:     "refused content is permanent",
			err:      core.NewMirrorError(core.MirrorStatusRefused, core.MirrorReasonTooLarge, "secret detail"),
			wantCode: http.StatusUnsupportedMediaType,
			wantBody: CodeMirrorRefused,
		},
		{
			name:     "unreachable host is transient",
			err:      core.NewMirrorError(core.MirrorStatusUnreachable, core.MirrorReasonHTTPStatus, "secret detail"),
			wantCode: http.StatusBadGateway,
			wantBody: CodeMirrorUnreachable,
		},
		{
			name:     "bad signature asks for a re-sign",
			err:      urlsign.ErrBadSignature,
			wantCode: http.StatusForbidden,
			wantBody: CodeMirrorUnsigned,
		},
		{
			name:     "expired signature asks for a re-sign",
			err:      urlsign.ErrExpired,
			wantCode: http.StatusForbidden,
			wantBody: CodeMirrorUnsigned,
		},
	}

	seenStatuses := map[int]string{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := mirrorRouter(t, &stubMirror{enabled: true, err: tc.err})
			w := doGet(t, r, "?u=abc&s=def")

			if w.Code != tc.wantCode {
				t.Errorf("status = %d, want %d", w.Code, tc.wantCode)
			}
			if !strings.Contains(w.Body.String(), tc.wantBody) {
				t.Errorf("body %q does not contain %q", w.Body.String(), tc.wantBody)
			}
			// The detail can carry the remote host's response. Returning it
			// would let anyone with a signed link probe hosts through us.
			if strings.Contains(w.Body.String(), "secret detail") {
				t.Error("response leaked the failure detail to the caller")
			}
		})
		seenStatuses[tc.wantCode] = tc.wantBody
	}

	// Guard against a future refactor that maps two classes onto one status.
	if len(seenStatuses) < 3 {
		t.Errorf("only %d distinct statuses across failure classes; the client cannot tell them apart", len(seenStatuses))
	}
}

// An unreachable host must carry Retry-After so a client does not hammer a host
// that is already down.
func TestMirrorUnreachableAdvertisesRetryAfter(t *testing.T) {
	r := mirrorRouter(t, &stubMirror{
		enabled: true,
		err:     core.NewMirrorError(core.MirrorStatusUnreachable, core.MirrorReasonTransport, ""),
	})
	w := doGet(t, r, "?u=abc&s=def")
	if got := w.Header().Get("Retry-After"); got == "" {
		t.Error("no Retry-After on an unreachable response")
	}
}

// A missing signature must be refused identically to a wrong one: telling them
// apart only helps someone probing.
func TestMirrorRequiresSignatureParameters(t *testing.T) {
	r := mirrorRouter(t, &stubMirror{enabled: true, media: &core.MirroredMedia{}})
	for _, q := range []string{"", "?u=abc", "?s=def", "?u=abc&s=def&e=notanumber"} {
		w := doGet(t, r, q)
		if w.Code != http.StatusForbidden {
			t.Errorf("query %q: status = %d, want 403", q, w.Code)
		}
		if !strings.Contains(w.Body.String(), CodeMirrorUnsigned) {
			t.Errorf("query %q: body %q lacks %s", q, w.Body.String(), CodeMirrorUnsigned)
		}
	}
}

func TestMirrorServesBytesWithHardeningHeaders(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}
	r := mirrorRouter(t, &stubMirror{
		enabled: true,
		media: &core.MirroredMedia{
			Sha256: "abc123",
			Size:   int64(len(png)),
			Mime:   "image/png",
			Data:   png,
		},
	})
	w := doGet(t, r, "?u=abc&s=def")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	if got := w.Body.Bytes(); string(got) != string(png) {
		t.Error("served bytes differ from the mirrored ones")
	}
	if got := w.Header().Get("Content-Type"); got != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", got)
	}

	// These headers are the containment for bytes fetched from hosts we do not
	// control: even if something got past the magic-byte check, the browser
	// must not execute it or re-sniff its type.
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := w.Header().Get("Content-Security-Policy"); !strings.Contains(got, "sandbox") {
		t.Errorf("Content-Security-Policy = %q, want a sandbox directive", got)
	}
	if got := w.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("Referrer-Policy = %q, want no-referrer", got)
	}

	// The blob hash lets a client skip the mirror entirely on later loads.
	if got := w.Header().Get("X-Blossom-Sha256"); got != "abc123" {
		t.Errorf("X-Blossom-Sha256 = %q, want abc123", got)
	}
	if got := w.Header().Get("Cache-Control"); !strings.Contains(got, "immutable") {
		t.Errorf("Cache-Control = %q, want an immutable directive", got)
	}
	if got := w.Header().Get("X-Mirror-Status"); got != "miss" {
		t.Errorf("X-Mirror-Status = %q, want miss", got)
	}
}

func TestMirrorReportsCacheHits(t *testing.T) {
	r := mirrorRouter(t, &stubMirror{
		enabled: true,
		media:   &core.MirroredMedia{Sha256: "h", Mime: "image/png", Data: []byte{1}, FromCache: true},
	})
	w := doGet(t, r, "?u=abc&s=def")
	if got := w.Header().Get("X-Mirror-Status"); got != "hit" {
		t.Errorf("X-Mirror-Status = %q, want hit", got)
	}
}

// A disabled mirror must say so plainly rather than 404ing, so a client can
// tell "this server does not do that" from "that link is wrong".
func TestMirrorDisabledReportsNotImplemented(t *testing.T) {
	r := mirrorRouter(t, &stubMirror{enabled: false})
	w := doGet(t, r, "?u=abc&s=def")
	if w.Code != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501", w.Code)
	}
	if !strings.Contains(w.Body.String(), CodeMirrorDisabled) {
		t.Errorf("body %q lacks %s", w.Body.String(), CodeMirrorDisabled)
	}
}

func TestSignEndpointReturnsSignedAndRejected(t *testing.T) {
	r := mirrorRouter(t, &stubMirror{
		enabled:  true,
		signed:   []core.SignedMirrorURL{{Source: "https://a.example/1.png", URL: "/media/mirror?u=x&s=y"}},
		rejected: []core.RejectedMirrorURL{{Source: "file:///etc/passwd", Reason: core.MirrorReasonInvalidURL}},
	})

	w := httptest.NewRecorder()
	body := strings.NewReader(`{"urls":["https://a.example/1.png","file:///etc/passwd"]}`)
	req := httptest.NewRequest(http.MethodPost, "/media/mirror/sign", body)
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}

	var out signMirrorOutput
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	// A partially-bad batch must still yield the good half: an emoji set from
	// a stranger routinely contains one broken entry.
	if len(out.Signed) != 1 {
		t.Errorf("signed %d, want 1", len(out.Signed))
	}
	if len(out.Rejected) != 1 {
		t.Errorf("rejected %d, want 1", len(out.Rejected))
	}
}

func TestSignEndpointRejectsEmptyBatch(t *testing.T) {
	r := mirrorRouter(t, &stubMirror{enabled: true})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/media/mirror/sign", strings.NewReader(`{"urls":[]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

// The regression test for the production 404.
//
// The mirror shipped with its routes registered only when enabled, so a server
// running the new build with the feature switched off returned 404. That is
// indistinguishable from a typo in the path, a missing ingress rule, or a
// server too old to have the feature — and it was in fact read as a failed
// deploy while the correct build was running perfectly.
//
// A disabled feature must answer 501 from a route that EXISTS. This test
// asserts registration, not just handler behaviour, which is why it drives
// registerMediaMirrorRoutes rather than mounting the handlers by hand.
func TestMirrorRoutesExistWhenDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	registerMediaMirrorRoutes(r, stubServices{mirror: &stubMirror{enabled: false}}, zap.NewNop())

	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"serve route", http.MethodGet, "/media/mirror?u=x&s=y", ""},
		{"sign route", http.MethodPost, "/media/mirror/sign", `{"urls":["https://a.example/1.png"]}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			var req *http.Request
			if tc.body != "" {
				req = httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
				req.Header.Set("Content-Type", "application/json")
			} else {
				req = httptest.NewRequest(tc.method, tc.path, nil)
			}
			r.ServeHTTP(w, req)

			if w.Code == http.StatusNotFound {
				t.Fatalf("%s %s returned 404 while disabled — the route was not registered. "+
					"A client cannot tell this from a wrong path or an undeployed server.", tc.method, tc.path)
			}
			if w.Code != http.StatusNotImplemented {
				t.Errorf("status = %d, want 501", w.Code)
			}
			if !strings.Contains(w.Body.String(), CodeMirrorDisabled) {
				t.Errorf("body %q lacks %s", w.Body.String(), CodeMirrorDisabled)
			}
		})
	}
}

// The disabled answer must not sit behind authentication. An unauthenticated
// caller hitting the sign route on a disabled server must learn that the
// feature is off (501), not that they need to log in (401) — otherwise the
// actual state is hidden behind a login they cannot complete, which is the
// same "cannot tell what is wrong" failure in a different costume.
func TestMirrorDisabledAnswersBeforeAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	registerMediaMirrorRoutes(r, stubServices{mirror: &stubMirror{enabled: false}}, zap.NewNop())

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/media/mirror/sign", strings.NewReader(`{"urls":[]}`))
	req.Header.Set("Content-Type", "application/json")
	// Deliberately no Authorization header.
	r.ServeHTTP(w, req)

	if w.Code == http.StatusUnauthorized {
		t.Fatal("disabled mirror returned 401; the enabled-check must run before the auth middleware")
	}
	if w.Code != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501", w.Code)
	}
}

// With the mirror enabled, the sign route must still be authenticated — the
// fix above must not have opened it up.
func TestMirrorSignStillRequiresAuthWhenEnabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	registerMediaMirrorRoutes(r, stubServices{mirror: &stubMirror{enabled: true}}, zap.NewNop())

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/media/mirror/sign", strings.NewReader(`{"urls":[]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 — an unauthenticated caller must not be able to mint links", w.Code)
	}
}

// The serve route must stay anonymous when enabled: it is the request a browser
// makes for every <img src>, and requiring auth there would attach an identity
// to every image view.
func TestMirrorServeStaysAnonymousWhenEnabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	registerMediaMirrorRoutes(r, stubServices{
		mirror: &stubMirror{enabled: true, media: &core.MirroredMedia{Sha256: "h", Mime: "image/png", Data: []byte{1}}},
	}, zap.NewNop())

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/media/mirror?u=x&s=y", nil))

	if w.Code == http.StatusUnauthorized {
		t.Fatal("serve route demanded auth; that would identify the viewer of every image")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}
