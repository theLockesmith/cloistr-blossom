package service

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/db"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/storage"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/pkg/urlsign"
)

const mirrorTestKey = "test-signing-key-at-least-32-bytes-long"

// newMirrorHarness builds a mirror wired to a real SQLite database and a real
// local blob store, so the tests exercise the actual SQL and storage paths
// rather than a mock that cannot disagree with them.
//
// SQLite rather than Postgres because these must run in the default `go test
// ./...` suite: a mirror bug that only shows up under an opt-in integration
// tag is a bug that ships.
func newMirrorHarness(t *testing.T, mutate func(*core.MediaMirrorConfig)) (*mediaMirrorService, *db.Queries) {
	t.Helper()

	dbPath := t.TempDir() + "/mirror-test.sqlite3"
	database, err := db.NewDBWithConfig(db.DBConfig{Driver: "sqlite", DSN: dbPath}, "../../db/migrations")
	if err != nil {
		t.Fatalf("open test database (this also verifies the mirror migration applies on SQLite): %v", err)
	}
	t.Cleanup(func() { database.Close() })

	queries := db.New(database)
	store, err := storage.NewLocalStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStorage: %v", err)
	}

	log := zap.NewNop()
	if testing.Verbose() {
		log, _ = zap.NewDevelopment()
	}
	blobs, err := NewBlobService(database, queries, store, nil, "http://test.invalid", log)
	if err != nil {
		t.Fatalf("NewBlobService: %v", err)
	}

	cfg := core.DefaultMediaMirrorConfig()
	cfg.Enabled = true
	cfg.SigningKey = mirrorTestKey
	// The test origins are httptest servers on loopback, which the SSRF guard
	// refuses by design. Allowing private addresses here is what makes the
	// rest of the behaviour testable; the guard itself is tested directly in
	// internal/safefetch.
	cfg.AllowPrivateAddresses = true
	if mutate != nil {
		mutate(&cfg)
	}

	svc, err := NewMediaMirrorService(queries, blobs, cfg, log)
	if err != nil {
		t.Fatalf("NewMediaMirrorService: %v", err)
	}
	return svc.(*mediaMirrorService), queries
}

// pngOfSize builds a valid PNG with the given dimensions.
func pngOfSize(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 1, G: 2, B: 3, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// get signs a URL the way the signing endpoint would, then fetches it the way
// the serving endpoint would. Going through the real signature keeps the tests
// honest about the fact that an unsigned request is not servable.
func (s *mediaMirrorService) get(t *testing.T, rawURL string) (*core.MirroredMedia, error) {
	t.Helper()
	signed, rejected, err := s.SignURLs(context.Background(), []string{rawURL})
	if err != nil {
		t.Fatalf("SignURLs: %v", err)
	}
	if len(signed) != 1 {
		t.Fatalf("SignURLs returned %d signed, %d rejected for %q", len(signed), len(rejected), rawURL)
	}
	exp, sig := s.signer.Sign(signed[0].Source)
	return s.VerifyAndGet(context.Background(), urlsign.EncodeURL(signed[0].Source), exp, sig)
}

func TestMirrorFetchesAndCaches(t *testing.T) {
	body := pngOfSize(t, 32, 32)
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "image/png")
		w.Write(body)
	}))
	defer srv.Close()

	svc, queries := newMirrorHarness(t, nil)

	first, err := svc.get(t, srv.URL+"/emoji.png")
	if err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	if first.FromCache {
		t.Error("first fetch reported a cache hit")
	}
	if !bytes.Equal(first.Data, body) {
		t.Error("mirrored bytes differ from the origin's")
	}
	if first.Mime != "image/png" {
		t.Errorf("mime = %q, want image/png", first.Mime)
	}

	second, err := svc.get(t, srv.URL+"/emoji.png")
	if err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	if !second.FromCache {
		t.Error("second fetch did not come from cache")
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("origin was contacted %d times, want 1 — caching is the whole point", got)
	}
	if second.Sha256 != first.Sha256 {
		t.Error("cached hash differs from the freshly fetched one")
	}

	// The mirrored object must be a normal blob, addressable at GET /<hash>.
	// If it were stored somewhere else the mirror would be a second, parallel
	// store rather than a way of acquiring blobs.
	row, err := queries.GetMirroredMedia(context.Background(), urlHash(srv.URL+"/emoji.png"))
	if err != nil {
		t.Fatalf("GetMirroredMedia: %v", err)
	}
	if row.Status != string(core.MirrorStatusOK) {
		t.Errorf("status = %q, want ok", row.Status)
	}
	if row.Sha256.String != first.Sha256 {
		t.Errorf("recorded sha256 = %q, want %q", row.Sha256.String, first.Sha256)
	}
}

// Distinct users mirroring the same emoji must yield one blob, not two. This is
// the dedup claim that makes the feature affordable.
func TestMirrorDeduplicatesIdenticalContentAcrossURLs(t *testing.T) {
	body := pngOfSize(t, 16, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	svc, _ := newMirrorHarness(t, nil)

	a, err := svc.get(t, srv.URL+"/one.png")
	if err != nil {
		t.Fatalf("fetch one: %v", err)
	}
	b, err := svc.get(t, srv.URL+"/two.png")
	if err != nil {
		t.Fatalf("fetch two: %v", err)
	}
	if a.Sha256 != b.Sha256 {
		t.Errorf("identical bytes produced different hashes: %s vs %s", a.Sha256, b.Sha256)
	}
}

func TestMirrorRefusesOversizedContent(t *testing.T) {
	t.Run("declared content-length", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "999999999")
			w.Write(make([]byte, 16))
		}))
		defer srv.Close()

		svc, _ := newMirrorHarness(t, func(c *core.MediaMirrorConfig) { c.MaxObjectBytes = 1024 })
		_, err := svc.get(t, srv.URL+"/big.png")
		assertMirrorError(t, err, core.MirrorStatusRefused, core.MirrorReasonTooLarge)
	})

	// A lying Content-Length is why the declared size is a courtesy and the
	// read limit is the control.
	t.Run("lying content-length", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// No Content-Length: chunked, so the size is only known by reading.
			w.Header().Set("Transfer-Encoding", "chunked")
			for i := 0; i < 64; i++ {
				w.Write(make([]byte, 1024))
			}
		}))
		defer srv.Close()

		svc, _ := newMirrorHarness(t, func(c *core.MediaMirrorConfig) { c.MaxObjectBytes = 1024 })
		_, err := svc.get(t, srv.URL+"/big.png")
		assertMirrorError(t, err, core.MirrorStatusRefused, core.MirrorReasonTooLarge)
	})
}

// The decompression-bomb guard. A small file that declares enormous dimensions
// must be refused on the header, before anything allocates a pixel buffer.
func TestMirrorRefusesDecompressionBomb(t *testing.T) {
	// A 6000x6000 all-zero PNG compresses to a few KB but decodes to ~144MB.
	bomb := pngOfSize(t, 6000, 6000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(bomb)
	}))
	defer srv.Close()

	svc, _ := newMirrorHarness(t, func(c *core.MediaMirrorConfig) {
		c.MaxObjectBytes = 10 << 20 // generous: the file itself is small
		c.MaxDimension = 512
		c.MaxPixels = 262144
	})
	_, err := svc.get(t, srv.URL+"/bomb.png")
	assertMirrorError(t, err, core.MirrorStatusRefused, core.MirrorReasonTooManyPixels)
}

// Type is decided from the bytes. A host serving HTML while claiming image/png
// must be refused, or we would cache HTML on our own domain and serve it back
// with an image content type.
func TestMirrorRefusesTypeMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("<html><script>alert(1)</script></html>"))
	}))
	defer srv.Close()

	svc, _ := newMirrorHarness(t, nil)
	_, err := svc.get(t, srv.URL+"/lying.png")
	assertMirrorError(t, err, core.MirrorStatusRefused, core.MirrorReasonTypeNotAllowed)
}

func TestMirrorRefusesDisallowedImageType(t *testing.T) {
	body := pngOfSize(t, 8, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	svc, _ := newMirrorHarness(t, func(c *core.MediaMirrorConfig) {
		c.AllowedMimeTypes = []string{"image/webp"}
	})
	_, err := svc.get(t, srv.URL+"/a.png")
	assertMirrorError(t, err, core.MirrorStatusRefused, core.MirrorReasonTypeNotAllowed)
}

// The distinction the peer review asked for by name: a dead host and a refused
// image must not be reported the same way.
func TestMirrorDistinguishesUnreachableFromRefused(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer dead.Close()

	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not an image at all"))
	}))
	defer refusing.Close()

	svc, _ := newMirrorHarness(t, nil)

	_, unreachableErr := svc.get(t, dead.URL+"/gone.png")
	assertMirrorError(t, unreachableErr, core.MirrorStatusUnreachable, core.MirrorReasonHTTPStatus)

	_, refusedErr := svc.get(t, refusing.URL+"/junk.png")
	assertMirrorError(t, refusedErr, core.MirrorStatusRefused, core.MirrorReasonTypeNotAllowed)

	// Belt and braces: the two must not be collapsible by a caller that only
	// looks at the status.
	ue, _ := core.AsMirrorError(unreachableErr)
	re, _ := core.AsMirrorError(refusedErr)
	if ue.Status == re.Status {
		t.Error("unreachable and refused share a status; the client cannot tell them apart")
	}
}

// A dead host must not be re-fetched on every view of a page that references
// it. The negative cache is what keeps one broken emoji from generating
// unbounded outbound traffic.
func TestMirrorNegativeCachesUnreachableHosts(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	svc, _ := newMirrorHarness(t, func(c *core.MediaMirrorConfig) { c.NegativeTTL = time.Hour })

	for i := 0; i < 3; i++ {
		if _, err := svc.get(t, srv.URL+"/dead.png"); err == nil {
			t.Fatal("expected an error from a failing origin")
		}
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("origin was contacted %d times, want 1 — the failure was not cached", got)
	}
}

// A negative cache that never expires would refuse an emoji forever because
// its host had five minutes of downtime.
func TestMirrorRetriesAfterNegativeTTLExpires(t *testing.T) {
	var hits int32
	body := pngOfSize(t, 8, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Write(body)
	}))
	defer srv.Close()

	svc, queries := newMirrorHarness(t, func(c *core.MediaMirrorConfig) { c.NegativeTTL = time.Hour })

	if _, err := svc.get(t, srv.URL+"/flaky.png"); err == nil {
		t.Fatal("expected the first fetch to fail")
	}

	// Age the cached failure past its TTL rather than sleeping for an hour.
	key := urlHash(srv.URL + "/flaky.png")
	row, err := queries.GetMirroredMedia(context.Background(), key)
	if err != nil {
		t.Fatalf("GetMirroredMedia: %v", err)
	}
	if err := queries.UpsertMirroredMedia(context.Background(), db.UpsertMirroredMediaParams{
		UrlHash:    row.UrlHash,
		SourceUrl:  row.SourceUrl,
		Status:     row.Status,
		Reason:     row.Reason,
		Sha256:     row.Sha256,
		Size:       row.Size,
		Mime:       row.Mime,
		FetchedAt:  time.Now().Add(-2 * time.Hour).Unix(),
		AccessedAt: row.AccessedAt,
	}); err != nil {
		t.Fatalf("age the cached failure: %v", err)
	}

	got, err := svc.get(t, srv.URL+"/flaky.png")
	if err != nil {
		t.Fatalf("retry after the negative TTL expired: %v", err)
	}
	if !bytes.Equal(got.Data, body) {
		t.Error("retry returned the wrong bytes")
	}
}

// N simultaneous viewers opening the same emoji set must produce ONE outbound
// fetch, not N. Without collapsing, the mirror amplifies load onto the very
// hosts we promised to contact less.
func TestMirrorCollapsesConcurrentFetches(t *testing.T) {
	body := pngOfSize(t, 8, 8)
	var hits int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		<-release // hold the first request open so the others pile up
		w.Write(body)
	}))
	defer srv.Close()

	svc, _ := newMirrorHarness(t, nil)
	target := srv.URL + "/hot.png"

	const callers = 8
	var wg sync.WaitGroup
	errs := make([]error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = svc.get(t, target)
		}(i)
	}

	// Give the goroutines time to queue behind the in-flight fetch.
	time.Sleep(150 * time.Millisecond)
	close(release)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("caller %d: %v", i, err)
		}
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("origin was contacted %d times for %d concurrent callers, want 1", got, callers)
	}
}

// A forged or unsigned link must never cause an outbound fetch. If it did, the
// signature would be decoration rather than a control.
func TestMirrorRejectsUnsignedRequestWithoutFetching(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
	}))
	defer srv.Close()

	svc, _ := newMirrorHarness(t, nil)
	target := srv.URL + "/private.png"

	_, err := svc.VerifyAndGet(context.Background(), urlsign.EncodeURL(target), 0, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	if !errors.Is(err, urlsign.ErrBadSignature) {
		t.Fatalf("error = %v, want ErrBadSignature", err)
	}
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Errorf("origin was contacted %d times on an unsigned request, want 0", got)
	}
}

func TestMirrorSignURLsRejectsBadInputsIndividually(t *testing.T) {
	svc, _ := newMirrorHarness(t, nil)

	signed, rejected, err := svc.SignURLs(context.Background(), []string{
		"https://example.com/good.png",
		"file:///etc/passwd",
		"not a url at all",
		"https://example.com/also-good.png",
	})
	if err != nil {
		t.Fatalf("SignURLs: %v", err)
	}
	// A picker resolving a set published by a stranger will routinely contain
	// one broken entry; failing the batch would leave the user with nothing.
	if len(signed) != 2 {
		t.Errorf("signed %d urls, want 2", len(signed))
	}
	if len(rejected) != 2 {
		t.Errorf("rejected %d urls, want 2", len(rejected))
	}
	for _, r := range rejected {
		if r.Reason != core.MirrorReasonInvalidURL {
			t.Errorf("rejection reason = %q, want %q", r.Reason, core.MirrorReasonInvalidURL)
		}
	}
}

func TestMirrorSignURLsDeduplicatesCanonicalDuplicates(t *testing.T) {
	svc, _ := newMirrorHarness(t, nil)
	signed, _, err := svc.SignURLs(context.Background(), []string{
		"https://example.com/a.png",
		"HTTPS://Example.com:443/a.png",
	})
	if err != nil {
		t.Fatalf("SignURLs: %v", err)
	}
	if len(signed) != 1 {
		t.Errorf("signed %d urls, want 1 — these canonicalize to the same URL", len(signed))
	}
}

func TestMirrorSignURLsEnforcesBatchCap(t *testing.T) {
	svc, _ := newMirrorHarness(t, func(c *core.MediaMirrorConfig) { c.MaxSignBatch = 2 })
	_, _, err := svc.SignURLs(context.Background(), []string{"https://a.example/1", "https://a.example/2", "https://a.example/3"})
	if err == nil {
		t.Fatal("oversized batch was accepted")
	}
}

// LRU eviction must actually free bytes and must drain below the cap, not to
// it — otherwise the next insert triggers another full pass.
func TestMirrorEvictsToLowWaterMark(t *testing.T) {
	body := pngOfSize(t, 64, 64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	const entries = 10
	svc, queries := newMirrorHarness(t, func(c *core.MediaMirrorConfig) {
		// Cap at roughly half of what we are about to store.
		c.CacheMaxBytes = int64(len(body)) * entries / 2
		c.CacheLowWaterPercent = 50
	})

	for i := 0; i < entries; i++ {
		if _, err := svc.get(t, fmt.Sprintf("%s/e%d.png", srv.URL, i)); err != nil {
			t.Fatalf("fetch %d: %v", i, err)
		}
	}

	before, err := queries.SumMirroredMediaSize(context.Background())
	if err != nil {
		t.Fatalf("SumMirroredMediaSize: %v", err)
	}
	if before <= svc.config.CacheMaxBytes {
		t.Fatalf("test bug: cache (%d) never exceeded the cap (%d)", before, svc.config.CacheMaxBytes)
	}

	result, err := svc.Evict(context.Background())
	if err != nil {
		t.Fatalf("Evict: %v", err)
	}
	if result.Evicted == 0 {
		t.Fatal("eviction freed nothing despite being over the cap")
	}

	after, err := queries.SumMirroredMediaSize(context.Background())
	if err != nil {
		t.Fatalf("SumMirroredMediaSize: %v", err)
	}
	target := svc.lowWaterMark()
	if after > target {
		t.Errorf("cache is %d bytes after eviction, want <= low water mark %d", after, target)
	}
	// Draining to exactly the cap would re-trigger on the very next insert.
	if after > svc.config.CacheMaxBytes {
		t.Errorf("cache is %d bytes, still over the cap %d", after, svc.config.CacheMaxBytes)
	}
}

// Eviction must not run when the cache is under its cap.
func TestMirrorEvictIsNoOpUnderCap(t *testing.T) {
	body := pngOfSize(t, 8, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	svc, _ := newMirrorHarness(t, func(c *core.MediaMirrorConfig) { c.CacheMaxBytes = 1 << 30 })
	if _, err := svc.get(t, srv.URL+"/a.png"); err != nil {
		t.Fatalf("fetch: %v", err)
	}

	result, err := svc.Evict(context.Background())
	if err != nil {
		t.Fatalf("Evict: %v", err)
	}
	if result.Evicted != 0 {
		t.Errorf("evicted %d entries while under the cap", result.Evicted)
	}
}

// An evicted entry must be re-fetchable. If eviction left a row pointing at a
// deleted blob, the mirror would answer "I have this" and then 404.
func TestMirrorRefetchesAfterEviction(t *testing.T) {
	body := pngOfSize(t, 32, 32)
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Write(body)
	}))
	defer srv.Close()

	svc, queries := newMirrorHarness(t, func(c *core.MediaMirrorConfig) { c.CacheMaxBytes = 1 })

	if _, err := svc.get(t, srv.URL+"/a.png"); err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	if _, err := svc.Evict(context.Background()); err != nil {
		t.Fatalf("Evict: %v", err)
	}
	if _, err := queries.GetMirroredMedia(context.Background(), urlHash(srv.URL+"/a.png")); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("entry survived eviction: %v", err)
	}

	got, err := svc.get(t, srv.URL+"/a.png")
	if err != nil {
		t.Fatalf("re-fetch after eviction: %v", err)
	}
	if !bytes.Equal(got.Data, body) {
		t.Error("re-fetched bytes differ from the origin's")
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Errorf("origin contacted %d times, want 2 (once before eviction, once after)", got)
	}
}

// A disabled mirror must not serve, sign, or evict.
func TestMirrorDisabledRefusesEverything(t *testing.T) {
	svc, _ := newMirrorHarness(t, func(c *core.MediaMirrorConfig) { c.Enabled = false })
	if svc.IsEnabled() {
		t.Fatal("IsEnabled true for a disabled mirror")
	}
	if _, _, err := svc.SignURLs(context.Background(), []string{"https://example.com/a.png"}); !errors.Is(err, core.ErrMirrorDisabled) {
		t.Errorf("SignURLs error = %v, want ErrMirrorDisabled", err)
	}
	if _, err := svc.VerifyAndGet(context.Background(), "x", 0, "y"); !errors.Is(err, core.ErrMirrorDisabled) {
		t.Errorf("VerifyAndGet error = %v, want ErrMirrorDisabled", err)
	}
	if _, err := svc.Evict(context.Background()); !errors.Is(err, core.ErrMirrorDisabled) {
		t.Errorf("Evict error = %v, want ErrMirrorDisabled", err)
	}
}

// A mirror enabled without a usable signing key must refuse to construct.
// Starting anyway would run an open proxy.
func TestMirrorRefusesToStartWithoutSigningKey(t *testing.T) {
	cfg := core.DefaultMediaMirrorConfig()
	cfg.Enabled = true
	cfg.SigningKey = ""
	if _, err := NewMediaMirrorService(nil, nil, cfg, zap.NewNop()); err == nil {
		t.Fatal("mirror constructed with no signing key — that is an open proxy")
	}

	cfg.SigningKey = "too-short"
	if _, err := NewMediaMirrorService(nil, nil, cfg, zap.NewNop()); err == nil {
		t.Fatal("mirror constructed with a short signing key")
	}
}

func TestMirrorRefusesToStartWithMalformedDenyCIDR(t *testing.T) {
	cfg := core.DefaultMediaMirrorConfig()
	cfg.Enabled = true
	cfg.SigningKey = mirrorTestKey
	cfg.ExtraDeniedCIDRs = []string{"garbage"}
	if _, err := NewMediaMirrorService(nil, nil, cfg, zap.NewNop()); err == nil {
		t.Fatal("mirror constructed with a malformed deny CIDR; the operator would think they had a fence")
	}
}

// The SSRF guard must be live in the service by default, not only in the
// safefetch package's own tests.
func TestMirrorBlocksPrivateAddressesByDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the mirror connected to a loopback address")
	}))
	defer srv.Close()

	svc, _ := newMirrorHarness(t, func(c *core.MediaMirrorConfig) { c.AllowPrivateAddresses = false })
	_, err := svc.get(t, srv.URL+"/internal.png")
	assertMirrorError(t, err, core.MirrorStatusRefused, core.MirrorReasonBlockedAddress)
}

// An SSRF attempt must be recorded as refused, not unreachable: cached as
// unreachable it would be retried every negative-TTL window forever.
func TestMirrorRecordsBlockedAddressAsRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	svc, queries := newMirrorHarness(t, func(c *core.MediaMirrorConfig) { c.AllowPrivateAddresses = false })
	if _, err := svc.get(t, srv.URL+"/internal.png"); err == nil {
		t.Fatal("expected refusal")
	}

	row, err := queries.GetMirroredMedia(context.Background(), urlHash(srv.URL+"/internal.png"))
	if err != nil {
		t.Fatalf("GetMirroredMedia: %v", err)
	}
	if row.Status != string(core.MirrorStatusRefused) {
		t.Errorf("status = %q, want refused", row.Status)
	}
	if row.Reason.String != core.MirrorReasonBlockedAddress {
		t.Errorf("reason = %q, want %q", row.Reason.String, core.MirrorReasonBlockedAddress)
	}
}

// The failure detail can contain the remote host's response. Returning it would
// let anyone with a signed link use the mirror as a network probe.
func TestMirrorDoesNotLeakFailureDetailToCaller(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	defer srv.Close()

	svc, _ := newMirrorHarness(t, nil)
	_, err := svc.get(t, srv.URL+"/probe.png")
	me, ok := core.AsMirrorError(err)
	if !ok {
		t.Fatalf("error is not a MirrorError: %v", err)
	}
	if me.Detail != "" {
		t.Errorf("detail %q was returned to the caller; it belongs in the log only", me.Detail)
	}
	if me.Reason == "" {
		t.Error("reason is empty; the client has nothing to branch on")
	}
}

func assertMirrorError(t *testing.T, err error, wantStatus core.MirrorStatus, wantReason string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected a %s/%s error, got success", wantStatus, wantReason)
	}
	me, ok := core.AsMirrorError(err)
	if !ok {
		t.Fatalf("error is not a MirrorError: %v", err)
	}
	if me.Status != wantStatus {
		t.Errorf("status = %q, want %q (err: %v)", me.Status, wantStatus, err)
	}
	if me.Reason != wantReason {
		t.Errorf("reason = %q, want %q (err: %v)", me.Reason, wantReason, err)
	}
}
