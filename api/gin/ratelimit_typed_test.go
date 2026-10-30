package gin

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/pkg/config"
)

// Each request type has its own counter: downloads do not use up the upload
// allowance of the same address.
func TestRateLimitMiddleware_CountersKeyedByType(t *testing.T) {
	conf := &config.RateLimitingConfig{Enabled: true}
	conf.IP.Download.Requests = 120
	conf.IP.Download.Window = "1m"
	conf.IP.Upload.Requests = 2
	conf.IP.Upload.Window = "1m"

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
	r := setupRateLimitTestRouter(conf, limiter)
	send := func(method, path string) int {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(method, path, nil))
		return w.Code
	}

	for i := 0; i < 5; i++ {
		require.Equal(t, http.StatusOK, send(http.MethodGet, "/download/abc123"))
	}
	assert.Equal(t, http.StatusOK, send(http.MethodPut, "/upload"), "downloads must not use up the upload allowance")
	assert.Equal(t, http.StatusOK, send(http.MethodPut, "/upload"))
	assert.Equal(t, http.StatusTooManyRequests, send(http.MethodPut, "/upload"), "the upload limit itself still holds")

	assert.Equal(t, 5, counts["ip:192.0.2.1:download"])
	assert.Equal(t, 3, counts["ip:192.0.2.1:upload"])
}

// cappedBandwidthLimiter is a byte limiter with a real cap: a charge that
// would cross it is refused and not counted.
func cappedBandwidthLimiter(used map[string]int64, mu *sync.Mutex) *mockBandwidthLimiter {
	return &mockBandwidthLimiter{
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
}

// streamRouter answers a PUT by reading the whole body, the way the upload
// handlers do, and reports a read error as 400.
func streamRouter(conf *config.RateLimitingConfig, limiter *mockBandwidthLimiter, read *int64) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(BandwidthLimitMiddleware(limiter, conf, zap.NewNop()))
	r.Handle(http.MethodPut, "/upload", readAll(read))
	r.Handle(http.MethodPatch, "/files/:id", readAll(read))
	return r
}

func readAll(read *int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		n, err := io.Copy(io.Discard, c.Request.Body)
		*read = n
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"message": "read failed: " + err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	}
}

// unsized hides the length of a body, as chunked transfer encoding does.
type unsized struct{ io.Reader }

func putUnsized(r *gin.Engine, size int) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut, "/upload", unsized{bytes.NewReader(make([]byte, size))})
	req.ContentLength = -1
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// A body sent without a Content-Length is metered as it is read and refused
// partway once the address is over its upload cap.
func TestBandwidthLimitMiddleware_RefusesChunkedBodyMidStream(t *testing.T) {
	conf := &config.RateLimitingConfig{Enabled: true}
	conf.Bandwidth.UploadMBPerMinute = 2

	var mu sync.Mutex
	used := map[string]int64{}
	var read int64
	r := streamRouter(conf, cappedBandwidthLimiter(used, &mu), &read)

	w := putUnsized(r, 8<<20)
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.NotEmpty(t, w.Header().Get("Retry-After"))
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body), "the refusal must be the whole response body")
	assert.Contains(t, body["message"], "bandwidth limit exceeded")
	assert.Less(t, read, int64(8<<20), "the handler must not get the whole body")
	assert.LessOrEqual(t, used["upload:ip:192.0.2.1"], int64(2<<20))
}

// An unsized body under the cap goes through and is charged exactly its size.
func TestBandwidthLimitMiddleware_ChargesChunkedBodyExactly(t *testing.T) {
	conf := &config.RateLimitingConfig{Enabled: true}
	conf.Bandwidth.UploadMBPerMinute = 10

	var mu sync.Mutex
	used := map[string]int64{}
	var read int64
	r := streamRouter(conf, cappedBandwidthLimiter(used, &mu), &read)

	size := 3<<20 + 12345
	w := putUnsized(r, size)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, int64(size), read)
	assert.Equal(t, int64(size), used["upload:ip:192.0.2.1"])
}

// A body with a Content-Length is charged once, up front, not again as read.
func TestBandwidthLimitMiddleware_SizedBodyChargedOnce(t *testing.T) {
	conf := &config.RateLimitingConfig{Enabled: true}
	conf.Bandwidth.UploadMBPerMinute = 10

	var mu sync.Mutex
	used := map[string]int64{}
	var read int64
	r := streamRouter(conf, cappedBandwidthLimiter(used, &mu), &read)

	size := 3 << 20
	req := httptest.NewRequest(http.MethodPut, "/upload", bytes.NewReader(make([]byte, size)))
	req.ContentLength = int64(size)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, int64(size), used["upload:ip:192.0.2.1"])
}

// A tus chunk sent without a Content-Length is cut off the same way.
func TestBandwidthLimitMiddleware_RefusesChunkedTusPatch(t *testing.T) {
	conf := &config.RateLimitingConfig{Enabled: true}
	conf.Bandwidth.UploadMBPerMinute = 2

	var mu sync.Mutex
	used := map[string]int64{}
	var read int64
	r := streamRouter(conf, cappedBandwidthLimiter(used, &mu), &read)

	req := httptest.NewRequest(http.MethodPatch, "/files/abc123", unsized{bytes.NewReader(make([]byte, 8<<20))})
	req.ContentLength = -1
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Less(t, read, int64(8<<20))
}
