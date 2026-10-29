package gin

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	goNostr "github.com/nbd-wtf/go-nostr"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/pkg/config"
)

// The limiter verified the auth header (a Schnorr signature check) before
// applying the IP limit, so a flood of junk auth headers from one address
// cost a verify each even after that address was over its limit.
func TestRateLimit_OverIPLimitNeverVerifies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var verifies atomic.Int32
	orig := limiterVerify
	limiterVerify = func(h string) (*goNostr.Event, authTags, string) {
		verifies.Add(1)
		return orig(h)
	}
	t.Cleanup(func() { limiterVerify = orig })

	conf := &config.RateLimitingConfig{Enabled: true}
	conf.IP.Upload = config.RateLimitConfig{Requests: 30, Window: "1m"}
	conf.Pubkey.Upload = config.RateLimitConfig{Requests: 30, Window: "1m"}
	conf.Bandwidth.UploadMBPerMinute = 500
	overLimit := &mockRateLimiter{allowFunc: func(_ context.Context, key string, limit int, w time.Duration) (bool, int, time.Time) {
		return false, 0, time.Now().Add(w)
	}}

	r := gin.New()
	r.Use(RateLimitMiddleware(overLimit, conf, zap.NewNop()))
	r.Use(BandwidthLimitMiddleware(&mockBandwidthLimiter{}, conf, zap.NewNop()))
	r.PUT("/upload", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodPut, "/upload", bytes.NewReader([]byte("x")))
	req.Header.Set("Authorization", "Nostr eyJqdW5rIjp0cnVlfQ==")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Equal(t, int32(0), verifies.Load(), "an address over its limit must not cost a signature check")
}

// Under the limit, one request is verified once across both limiters.
func TestRateLimit_VerifiesOncePerRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var verifies atomic.Int32
	orig := limiterVerify
	limiterVerify = func(h string) (*goNostr.Event, authTags, string) {
		verifies.Add(1)
		return orig(h)
	}
	t.Cleanup(func() { limiterVerify = orig })

	r, _ := limiterRouter(t, "00")
	assert.Equal(t, http.StatusOK, putUpload(r, signedToken(t, "upload", xHashA)))
	assert.Equal(t, int32(1), verifies.Load())
}
