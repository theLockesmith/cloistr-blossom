package gin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/pkg/config"
)

// In-cluster services (Stash's server, probes, Prometheus) call the pod
// directly: private peer, and no X-Real-IP because they never pass the
// public edge, which always sets it. Limiting them by address would put
// every Stash user into one bucket per Stash pod. Public traffic always
// carries the edge's X-Real-IP, so it can never take this path.
func TestRateLimitSkipsInternalCallers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	conf := &config.Config{}
	conf.ApplyDefaults()
	conf.RateLimiting.Enabled = true
	conf.RateLimiting.IP.General = config.RateLimitConfig{Requests: 1, Window: "1m"}

	counts := map[string]int{}
	limiter := &mockRateLimiter{allowFunc: func(_ context.Context, key string, limit int, window time.Duration) (bool, int, time.Time) {
		counts[key]++
		return counts[key] <= limit, 0, time.Now().Add(window)
	}}
	r, err := newEngine(conf)
	require.NoError(t, err)
	r.Use(RateLimitMiddleware(limiter, &conf.RateLimiting, zap.NewNop()))
	r.GET("/list/:pubkey", func(c *gin.Context) { c.Status(http.StatusOK) })

	do := func(remote, realIP string) int {
		req := httptest.NewRequest(http.MethodGet, "/list/abc", nil)
		req.RemoteAddr = remote
		if realIP != "" {
			req.Header.Set("X-Real-IP", realIP)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}

	for i := 0; i < 3; i++ {
		assert.Equal(t, http.StatusOK, do("10.128.5.182:5000", ""), "in-cluster caller is not limited")
	}
	assert.Equal(t, http.StatusOK, do(routerAddr, "203.0.113.7"))
	assert.Equal(t, http.StatusTooManyRequests, do(routerAddr, "203.0.113.7"), "public client is limited")
	assert.Equal(t, http.StatusOK, do("198.51.100.9:1", ""))
	assert.Equal(t, http.StatusTooManyRequests, do("198.51.100.9:1", ""), "a public peer without the header is still limited")
	assert.NotContains(t, counts, "ip:10.128.5.182")

	conf.RateLimiting.LimitInternalCallers = true
	assert.Equal(t, http.StatusOK, do("10.128.5.182:5000", ""))
	assert.Equal(t, http.StatusTooManyRequests, do("10.128.5.182:5000", ""), "opt-in limits internal callers too")
}
