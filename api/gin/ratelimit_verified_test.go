package gin

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nbd-wtf/go-nostr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/pkg/config"
)

// The limiters run on every request before the per-route auth middleware,
// which is the only thing that set "pk". So the limiter never saw a pubkey:
// whitelisted_pubkeys never applied, and every caller was limited by IP.
// The limiter now verifies the auth header itself and trusts a pubkey only
// from a valid signature.

type keyRecorder struct {
	mu   sync.Mutex
	keys []string
}

func (k *keyRecorder) add(key string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.keys = append(k.keys, key)
}

func (k *keyRecorder) seen() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]string(nil), k.keys...)
}

func limiterRouter(t *testing.T, whitelisted string) (*gin.Engine, *keyRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	log := zap.NewNop()
	rec := &keyRecorder{}
	conf := &config.RateLimitingConfig{Enabled: true, WhitelistedPubkeys: []string{whitelisted}}
	conf.IP.Upload = config.RateLimitConfig{Requests: 30, Window: "1m"}
	conf.Pubkey.Upload = config.RateLimitConfig{Requests: 30, Window: "1m"}
	conf.Bandwidth.UploadMBPerMinute = 500

	rl := &mockRateLimiter{allowFunc: func(_ context.Context, key string, limit int, window time.Duration) (bool, int, time.Time) {
		rec.add("rate:" + key)
		return true, limit - 1, time.Now().Add(window)
	}}
	bw := &mockBandwidthLimiter{allowBytesFunc: func(_ context.Context, key string, n, limit int64, _ time.Duration) (bool, int64, time.Time) {
		rec.add("bw:" + key)
		return true, limit - n, time.Now().Add(time.Minute)
	}}

	r := gin.New()
	r.Use(RateLimitMiddleware(rl, conf, log))
	r.Use(BandwidthLimitMiddleware(bw, conf, log))
	r.PUT("/upload", nostrAuthMiddleware("upload", log), func(c *gin.Context) { c.Status(http.StatusOK) })
	return r, rec
}

func putUpload(r *gin.Engine, auth string) int {
	req := httptest.NewRequest(http.MethodPut, "/upload", bytes.NewReader([]byte("hello")))
	req.RemoteAddr = "203.0.113.7:4000"
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

func TestRateLimit_WhitelistedKeyThroughRealAuth(t *testing.T) {
	sk := nostr.GeneratePrivateKey()
	pk, _ := nostr.GetPublicKey(sk)
	r, rec := limiterRouter(t, pk)

	require.Equal(t, http.StatusOK, putUpload(r, signedTokenAs(t, sk, "upload", xHashA)))
	// The IP limit is checked first (before any signature check); after
	// that a verified whitelisted key skips its per-key and bandwidth limits.
	assert.Equal(t, []string{"rate:ip:203.0.113.7:upload"}, rec.seen())
}

func TestRateLimit_ForgedWhitelistedKeyIsLimited(t *testing.T) {
	sk := nostr.GeneratePrivateKey()
	pk, _ := nostr.GetPublicKey(sk)
	r, rec := limiterRouter(t, pk)

	// Claim the whitelisted pubkey, signed by someone else.
	var ev nostr.Event
	other := signedToken(t, "upload", xHashA)
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(other, "Nostr "))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &ev))
	ev.PubKey = pk
	forged, err := encodeAuthEvent(&ev)
	require.NoError(t, err)

	assert.Equal(t, http.StatusUnauthorized, putUpload(r, "Nostr "+forged))
	assert.Contains(t, rec.seen(), "rate:ip:203.0.113.7:upload", "a forged claim is limited by IP")
	assert.NotContains(t, rec.seen(), "rate:pk:"+pk+":upload", "and never counted as the claimed key")
}

func TestRateLimit_UnsignedIsLimitedByIP(t *testing.T) {
	r, rec := limiterRouter(t, "00")
	assert.Equal(t, http.StatusUnauthorized, putUpload(r, ""))
	assert.Equal(t, []string{"rate:ip:203.0.113.7:upload", "bw:upload:ip:203.0.113.7"}, rec.seen())
}

func TestRateLimit_SignedKeyIsLimitedByIPAndKey(t *testing.T) {
	sk := nostr.GeneratePrivateKey()
	pk, _ := nostr.GetPublicKey(sk)
	r, rec := limiterRouter(t, "00")

	require.Equal(t, http.StatusOK, putUpload(r, signedTokenAs(t, sk, "upload", xHashA)))
	// The IP limit still applies, so minting fresh keys from one address
	// does not buy a fresh budget each time.
	assert.Contains(t, rec.seen(), "rate:ip:203.0.113.7:upload")
	assert.Contains(t, rec.seen(), "rate:pk:"+pk+":upload")
}
