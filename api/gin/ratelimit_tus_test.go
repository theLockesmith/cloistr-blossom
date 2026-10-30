package gin

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/pkg/config"
)

// tusLimiterRouter registers the tus routes that carry upload bytes behind the
// given middleware, with handlers that just answer 204.
func tusLimiterRouter(mw gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(mw)
	ok := func(c *gin.Context) { c.Status(http.StatusNoContent) }
	r.POST("/files", ok)
	r.PATCH("/files/:id", ok)
	return r
}

// A resumable upload is held to the upload request limit, not the much larger
// general one: the create and every chunk count as uploads.
func TestRateLimitMiddleware_TusUsesUploadLimit(t *testing.T) {
	conf := &config.RateLimitingConfig{Enabled: true}
	conf.IP.Upload.Requests = 2
	conf.IP.Upload.Window = "1m"
	conf.IP.General.Requests = 100
	conf.IP.General.Window = "1m"

	var mu sync.Mutex
	counts := map[string]int{}
	limiter := &mockRateLimiter{
		allowFunc: func(ctx context.Context, key string, limit int, window time.Duration) (bool, int, time.Time) {
			mu.Lock()
			defer mu.Unlock()
			counts[key]++
			return counts[key] <= limit, limit - counts[key], time.Now().Add(window)
		},
	}
	logger, _ := zap.NewDevelopment()
	r := tusLimiterRouter(RateLimitMiddleware(limiter, conf, logger))

	send := func(method, path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(method, path, nil))
		return w
	}

	w := send(http.MethodPost, "/files")
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Equal(t, "2", w.Header().Get("X-RateLimit-Limit"), "tus create must be charged to the upload limit")

	assert.Equal(t, http.StatusNoContent, send(http.MethodPatch, "/files/abc123").Code)

	w = send(http.MethodPatch, "/files/abc123")
	assert.Equal(t, http.StatusTooManyRequests, w.Code, "third upload request in the window must be refused")
	assert.Equal(t, "2", w.Header().Get("X-RateLimit-Limit"))
}

// Chunk bytes sent with PATCH count toward the upload bandwidth cap, exactly
// like a PUT body.
func TestBandwidthLimitMiddleware_MetersTusChunks(t *testing.T) {
	conf := &config.RateLimitingConfig{Enabled: true}
	conf.Bandwidth.UploadMBPerMinute = 1

	var mu sync.Mutex
	used := map[string]int64{}
	limiter := &mockBandwidthLimiter{
		allowBytesFunc: func(ctx context.Context, key string, n int64, limitBytes int64, window time.Duration) (bool, int64, time.Time) {
			mu.Lock()
			defer mu.Unlock()
			if used[key]+n > limitBytes {
				return false, limitBytes - used[key], time.Now().Add(window)
			}
			used[key] += n
			return true, limitBytes - used[key], time.Now().Add(window)
		},
	}
	logger, _ := zap.NewDevelopment()
	r := tusLimiterRouter(BandwidthLimitMiddleware(limiter, conf, logger))

	chunk := func(size int) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPatch, "/files/abc123", bytes.NewReader(make([]byte, size)))
		req.ContentLength = int64(size)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	w := chunk(700 * 1024)
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.NotEmpty(t, w.Header().Get("X-Bandwidth-Remaining"), "a chunk must be metered")

	assert.Equal(t, http.StatusTooManyRequests, chunk(700*1024).Code,
		"a chunk that takes the address past its upload cap must be refused")
	assert.Equal(t, int64(700*1024), used["upload:ip:192.0.2.1"])
}
