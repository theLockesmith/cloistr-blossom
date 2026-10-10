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

// routerAddr stands in for the in-cluster router hop: requests reach the pod
// from a private address, with X-Real-IP set by the public edge.
const routerAddr = "10.51.1.16:43210"

func clientIPEngine(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	conf := &config.Config{}
	conf.ApplyDefaults()
	r, err := newEngine(conf)
	require.NoError(t, err)
	r.GET("/ip", func(c *gin.Context) { c.String(http.StatusOK, c.ClientIP()) })
	return r
}

func clientIPOf(r *gin.Engine, remoteAddr string, headers map[string]string) string {
	req := httptest.NewRequest(http.MethodGet, "/ip", nil)
	req.RemoteAddr = remoteAddr
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Body.String()
}

func TestClientIP(t *testing.T) {
	r := clientIPEngine(t)

	tests := []struct {
		name    string
		remote  string
		headers map[string]string
		want    string
	}{
		{
			// The edge overwrites X-Real-IP but only appends to X-Forwarded-For,
			// so the leftmost XFF entry is whatever the client sent.
			name:   "via router: X-Real-IP wins over a forged X-Forwarded-For",
			remote: routerAddr,
			headers: map[string]string{
				"X-Real-IP":       "203.0.113.7",
				"X-Forwarded-For": "6.6.6.6, 203.0.113.7",
			},
			want: "203.0.113.7",
		},
		{
			name:    "via router: X-Forwarded-For alone is ignored",
			remote:  routerAddr,
			headers: map[string]string{"X-Forwarded-For": "6.6.6.6"},
			want:    "10.51.1.16",
		},
		{
			name:   "direct from a public address: both headers are ignored",
			remote: "198.51.100.9:5555",
			headers: map[string]string{
				"X-Real-IP":       "6.6.6.6",
				"X-Forwarded-For": "6.6.6.6",
			},
			want: "198.51.100.9",
		},
		{
			name:    "via router: garbage X-Real-IP falls back to the peer",
			remote:  routerAddr,
			headers: map[string]string{"X-Real-IP": "not-an-ip"},
			want:    "10.51.1.16",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, clientIPOf(r, tt.remote, tt.headers))
		})
	}
}

// TestRateLimitIgnoresForgedForwardedFor is the attack itself: rotating a
// forged X-Forwarded-For must not buy a fresh per-IP budget.
func TestRateLimitIgnoresForgedForwardedFor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	conf := &config.Config{}
	conf.ApplyDefaults()
	conf.RateLimiting.Enabled = true
	conf.RateLimiting.IP.General = config.RateLimitConfig{Requests: 2, Window: "1m"}

	counts := map[string]int{}
	limiter := &mockRateLimiter{allowFunc: func(_ context.Context, key string, limit int, window time.Duration) (bool, int, time.Time) {
		counts[key]++
		return counts[key] <= limit, limit - counts[key], time.Now().Add(window)
	}}

	r, err := newEngine(conf)
	require.NoError(t, err)
	r.Use(RateLimitMiddleware(limiter, &conf.RateLimiting, zap.NewNop()))
	r.GET("/list/:pubkey", func(c *gin.Context) { c.Status(http.StatusOK) })

	var last int
	for _, forged := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"} {
		req := httptest.NewRequest(http.MethodGet, "/list/abc", nil)
		req.RemoteAddr = routerAddr
		req.Header.Set("X-Real-IP", "203.0.113.7")
		req.Header.Set("X-Forwarded-For", forged)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		last = w.Code
	}
	assert.Equal(t, http.StatusTooManyRequests, last, "third request from one client must be limited")
	assert.Equal(t, map[string]int{"ip:203.0.113.7": 3}, counts)
}
