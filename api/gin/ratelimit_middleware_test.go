package gin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/pkg/config"
)

// mockRateLimiter is a mock implementation of ratelimit.RateLimiter for testing
type mockRateLimiter struct {
	allowFunc func(ctx context.Context, key string, limit int, window time.Duration) (bool, int, time.Time)
}

func (m *mockRateLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, int, time.Time) {
	if m.allowFunc != nil {
		return m.allowFunc(ctx, key, limit, window)
	}
	// Default: always allow
	return true, limit - 1, time.Now().Add(window)
}

func (m *mockRateLimiter) AllowN(ctx context.Context, key string, n int, limit int, window time.Duration) (bool, int, time.Time) {
	// Not used in middleware, but required for interface
	return m.Allow(ctx, key, limit, window)
}

// mockBandwidthLimiter is a mock implementation of ratelimit.BandwidthLimiter for testing
type mockBandwidthLimiter struct {
	allowBytesFunc func(ctx context.Context, key string, bytes int64, limitBytes int64, window time.Duration) (bool, int64, time.Time)
}

func (m *mockBandwidthLimiter) AllowBytes(ctx context.Context, key string, bytes int64, limitBytes int64, window time.Duration) (bool, int64, time.Time) {
	if m.allowBytesFunc != nil {
		return m.allowBytesFunc(ctx, key, bytes, limitBytes, window)
	}
	// Default: always allow
	return true, limitBytes - bytes, time.Now().Add(time.Minute)
}

// setupRateLimitTestRouter creates a test router with rate limit middleware
func setupRateLimitTestRouter(conf *config.RateLimitingConfig, limiter *mockRateLimiter) *gin.Engine {
	gin.SetMode(gin.TestMode)
	logger, _ := zap.NewDevelopment()

	r := gin.New()
	r.Use(RateLimitMiddleware(limiter, conf, logger))
	r.GET("/download/:hash", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	r.PUT("/upload", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	r.GET("/list/:pubkey", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	return r
}

// setupBandwidthTestRouter creates a test router with bandwidth limit middleware
func setupBandwidthTestRouter(conf *config.RateLimitingConfig, limiter *mockBandwidthLimiter) *gin.Engine {
	gin.SetMode(gin.TestMode)
	logger, _ := zap.NewDevelopment()

	r := gin.New()
	r.Use(BandwidthLimitMiddleware(limiter, conf, logger))
	r.PUT("/upload", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	r.GET("/download/:hash", func(c *gin.Context) {
		// Simulate writing some data
		c.JSON(http.StatusOK, gin.H{"data": "test content here"})
	})

	return r
}

// TestRateLimitMiddleware_WithinLimit tests that requests within rate limits pass through
func TestRateLimitMiddleware_WithinLimit(t *testing.T) {
	conf := &config.RateLimitingConfig{
		Enabled: true,
	}
	conf.IP.Download.Requests = 10
	conf.IP.Download.Window = "1m"

	limiter := &mockRateLimiter{
		allowFunc: func(ctx context.Context, key string, limit int, window time.Duration) (bool, int, time.Time) {
			return true, 5, time.Now().Add(time.Minute)
		},
	}

	r := setupRateLimitTestRouter(conf, limiter)

	req := httptest.NewRequest(http.MethodGet, "/download/abc123", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, "ok", response["status"])
}

// TestRateLimitMiddleware_ExceedsLimit tests that requests exceeding rate limits get 429 response
func TestRateLimitMiddleware_ExceedsLimit(t *testing.T) {
	conf := &config.RateLimitingConfig{
		Enabled: true,
	}
	conf.IP.Download.Requests = 10
	conf.IP.Download.Window = "1m"

	resetTime := time.Now().Add(time.Minute)
	limiter := &mockRateLimiter{
		allowFunc: func(ctx context.Context, key string, limit int, window time.Duration) (bool, int, time.Time) {
			return false, 0, resetTime
		},
	}

	r := setupRateLimitTestRouter(conf, limiter)

	req := httptest.NewRequest(http.MethodGet, "/download/abc123", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusTooManyRequests, w.Code)

	var response apiError
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Contains(t, response.Message, "rate limit exceeded")
}

// TestRateLimitMiddleware_Headers tests that rate limit headers are correctly set
func TestRateLimitMiddleware_Headers(t *testing.T) {
	tests := []struct {
		name              string
		allowed           bool
		remaining         int
		limit             int
		expectRetryAfter  bool
	}{
		{
			name:             "within limit",
			allowed:          true,
			remaining:        7,
			limit:            10,
			expectRetryAfter: false,
		},
		{
			name:             "at limit",
			allowed:          false,
			remaining:        0,
			limit:            10,
			expectRetryAfter: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conf := &config.RateLimitingConfig{
				Enabled: true,
			}
			conf.IP.Download.Requests = tt.limit
			conf.IP.Download.Window = "1m"

			resetTime := time.Now().Add(time.Minute)
			limiter := &mockRateLimiter{
				allowFunc: func(ctx context.Context, key string, limit int, window time.Duration) (bool, int, time.Time) {
					return tt.allowed, tt.remaining, resetTime
				},
			}

			r := setupRateLimitTestRouter(conf, limiter)

			req := httptest.NewRequest(http.MethodGet, "/download/abc123", nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			// Check X-RateLimit-Limit header
			limitHeader := w.Header().Get("X-RateLimit-Limit")
			assert.Equal(t, strconv.Itoa(tt.limit), limitHeader)

			// Check X-RateLimit-Remaining header
			remainingHeader := w.Header().Get("X-RateLimit-Remaining")
			assert.Equal(t, strconv.Itoa(tt.remaining), remainingHeader)

			// Check X-RateLimit-Reset header
			resetHeader := w.Header().Get("X-RateLimit-Reset")
			assert.Equal(t, strconv.FormatInt(resetTime.Unix(), 10), resetHeader)

			// Check Retry-After header when rate limited
			if tt.expectRetryAfter {
				retryAfter := w.Header().Get("Retry-After")
				assert.NotEmpty(t, retryAfter)
				// Should be a positive integer
				seconds, err := strconv.Atoi(retryAfter)
				require.NoError(t, err)
				assert.Greater(t, seconds, 0)
			}
		})
	}
}

// TestRateLimitMiddleware_PerIP tests per-IP rate limiting
func TestRateLimitMiddleware_PerIP(t *testing.T) {
	conf := &config.RateLimitingConfig{
		Enabled: true,
	}
	conf.IP.Download.Requests = 10
	conf.IP.Download.Window = "1m"

	var lastKey string
	limiter := &mockRateLimiter{
		allowFunc: func(ctx context.Context, key string, limit int, window time.Duration) (bool, int, time.Time) {
			lastKey = key
			return true, 5, time.Now().Add(time.Minute)
		},
	}

	r := setupRateLimitTestRouter(conf, limiter)

	// First request from IP 192.0.2.1
	req1 := httptest.NewRequest(http.MethodGet, "/download/abc123", nil)
	req1.RemoteAddr = "192.0.2.1:12345"
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)

	assert.Equal(t, http.StatusOK, w1.Code)
	assert.Contains(t, lastKey, "ip:")
	firstKey := lastKey

	// Second request from different IP
	req2 := httptest.NewRequest(http.MethodGet, "/download/abc123", nil)
	req2.RemoteAddr = "192.0.2.2:12345"
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)

	assert.Equal(t, http.StatusOK, w2.Code)
	assert.Contains(t, lastKey, "ip:")
	assert.NotEqual(t, firstKey, lastKey, "Different IPs should have different keys")
}

// TestRateLimitMiddleware_PerPubkey tests per-pubkey rate limiting for authenticated requests
func TestRateLimitMiddleware_PerPubkey(t *testing.T) {
	conf := &config.RateLimitingConfig{
		Enabled: true,
	}
	conf.Pubkey.Upload.Requests = 20
	conf.Pubkey.Upload.Window = "1m"

	var lastKey string
	limiter := &mockRateLimiter{
		allowFunc: func(ctx context.Context, key string, limit int, window time.Duration) (bool, int, time.Time) {
			lastKey = key
			// Check that we got the correct limit for pubkey (20, not IP limit)
			assert.Equal(t, 20, limit)
			return true, 10, time.Now().Add(time.Minute)
		},
	}

	_ = setupRateLimitTestRouter(conf, limiter)

	testPubkey := "abc123def456"
	req := httptest.NewRequest(http.MethodPut, "/upload", nil)

	// Simulate authenticated request by setting pubkey in context via middleware
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	c.Set("pk", testPubkey)

	// Manually call middleware
	middleware := RateLimitMiddleware(limiter, conf, zap.NewNop())
	middleware(c)

	assert.Equal(t, "pk:"+testPubkey, lastKey)
	assert.False(t, c.IsAborted(), "Authenticated request should not be aborted when within limit")
}

// TestRateLimitMiddleware_WhitelistedPubkey tests that whitelisted pubkeys bypass rate limits
func TestRateLimitMiddleware_WhitelistedPubkey(t *testing.T) {
	whitelistedPubkey := "whitelisted123"
	conf := &config.RateLimitingConfig{
		Enabled:            true,
		WhitelistedPubkeys: []string{whitelistedPubkey},
	}
	conf.Pubkey.Upload.Requests = 1
	conf.Pubkey.Upload.Window = "1m"

	limiterCalled := false
	limiter := &mockRateLimiter{
		allowFunc: func(ctx context.Context, key string, limit int, window time.Duration) (bool, int, time.Time) {
			limiterCalled = true
			return false, 0, time.Now().Add(time.Minute)
		},
	}

	_ = setupRateLimitTestRouter(conf, limiter)

	req := httptest.NewRequest(http.MethodPut, "/upload", nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	c.Set("pk", whitelistedPubkey)

	middleware := RateLimitMiddleware(limiter, conf, zap.NewNop())
	middleware(c)

	assert.False(t, limiterCalled, "Limiter should not be called for whitelisted pubkey")
	assert.False(t, c.IsAborted(), "Whitelisted request should pass through")
}

// TestRateLimitMiddleware_Disabled tests that middleware is bypassed when disabled
func TestRateLimitMiddleware_Disabled(t *testing.T) {
	conf := &config.RateLimitingConfig{
		Enabled: false,
	}

	limiterCalled := false
	limiter := &mockRateLimiter{
		allowFunc: func(ctx context.Context, key string, limit int, window time.Duration) (bool, int, time.Time) {
			limiterCalled = true
			return false, 0, time.Now().Add(time.Minute)
		},
	}

	r := setupRateLimitTestRouter(conf, limiter)

	req := httptest.NewRequest(http.MethodGet, "/download/abc123", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.False(t, limiterCalled, "Limiter should not be called when disabled")
}

// TestRateLimitMiddleware_RequestClassification tests different request type classifications
func TestRateLimitMiddleware_RequestClassification(t *testing.T) {
	tests := []struct {
		name           string
		method         string
		path           string
		expectedType   string
		expectedLimit  int
	}{
		{
			name:          "download blob",
			method:        "GET",
			path:          "/download/abc123",
			expectedType:  "download",
			expectedLimit: 100,
		},
		{
			name:          "upload blob",
			method:        "PUT",
			path:          "/upload",
			expectedType:  "upload",
			expectedLimit: 10,
		},
		{
			name:          "list blobs",
			method:        "GET",
			path:          "/list/pubkey123",
			expectedType:  "general",
			expectedLimit: 50,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conf := &config.RateLimitingConfig{
				Enabled: true,
			}
			conf.IP.Download.Requests = 100
			conf.IP.Download.Window = "1m"
			conf.IP.Upload.Requests = 10
			conf.IP.Upload.Window = "1m"
			conf.IP.General.Requests = 50
			conf.IP.General.Window = "1m"

			var capturedLimit int
			limiter := &mockRateLimiter{
				allowFunc: func(ctx context.Context, key string, limit int, window time.Duration) (bool, int, time.Time) {
					capturedLimit = limit
					return true, limit - 1, time.Now().Add(time.Minute)
				},
			}

			r := setupRateLimitTestRouter(conf, limiter)

			req := httptest.NewRequest(tt.method, tt.path, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			assert.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, tt.expectedLimit, capturedLimit, "Should use correct limit for request type")
		})
	}
}

// TestBandwidthLimitMiddleware_UploadWithinLimit tests upload bandwidth within limits
func TestBandwidthLimitMiddleware_UploadWithinLimit(t *testing.T) {
	conf := &config.RateLimitingConfig{
		Enabled: true,
	}
	conf.Bandwidth.UploadMBPerMinute = 10 // 10 MB/min

	limiter := &mockBandwidthLimiter{
		allowBytesFunc: func(ctx context.Context, key string, bytes int64, limitBytes int64, window time.Duration) (bool, int64, time.Time) {
			return true, limitBytes - bytes, time.Now().Add(time.Minute)
		},
	}

	r := setupBandwidthTestRouter(conf, limiter)

	body := bytes.NewBufferString("test upload data")
	req := httptest.NewRequest(http.MethodPut, "/upload", body)
	req.ContentLength = int64(body.Len())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

// TestBandwidthLimitMiddleware_UploadExceedsLimit tests upload bandwidth exceeding limits
func TestBandwidthLimitMiddleware_UploadExceedsLimit(t *testing.T) {
	conf := &config.RateLimitingConfig{
		Enabled: true,
	}
	conf.Bandwidth.UploadMBPerMinute = 10 // 10 MB/min

	resetTime := time.Now().Add(time.Minute)
	limiter := &mockBandwidthLimiter{
		allowBytesFunc: func(ctx context.Context, key string, bytes int64, limitBytes int64, window time.Duration) (bool, int64, time.Time) {
			return false, 0, resetTime
		},
	}

	r := setupBandwidthTestRouter(conf, limiter)

	body := bytes.NewBufferString("test upload data")
	req := httptest.NewRequest(http.MethodPut, "/upload", body)
	req.ContentLength = int64(body.Len())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusTooManyRequests, w.Code)

	var response apiError
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Contains(t, response.Message, "bandwidth limit exceeded")
}

// TestBandwidthLimitMiddleware_Headers tests bandwidth limit headers
func TestBandwidthLimitMiddleware_Headers(t *testing.T) {
	conf := &config.RateLimitingConfig{
		Enabled: true,
	}
	conf.Bandwidth.UploadMBPerMinute = 10 // 10 MB/min

	resetTime := time.Now().Add(time.Minute)
	limiter := &mockBandwidthLimiter{
		allowBytesFunc: func(ctx context.Context, key string, bytes int64, limitBytes int64, window time.Duration) (bool, int64, time.Time) {
			remaining := limitBytes - bytes
			return true, remaining, resetTime
		},
	}

	r := setupBandwidthTestRouter(conf, limiter)

	body := bytes.NewBufferString("test upload data")
	req := httptest.NewRequest(http.MethodPut, "/upload", body)
	req.ContentLength = int64(body.Len())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// Check bandwidth headers
	remainingHeader := w.Header().Get("X-Bandwidth-Remaining")
	assert.NotEmpty(t, remainingHeader)

	resetHeader := w.Header().Get("X-Bandwidth-Reset")
	assert.Equal(t, strconv.FormatInt(resetTime.Unix(), 10), resetHeader)
}

// TestBandwidthLimitMiddleware_WhitelistedPubkey tests that whitelisted pubkeys bypass bandwidth limits
func TestBandwidthLimitMiddleware_WhitelistedPubkey(t *testing.T) {
	whitelistedPubkey := "whitelisted456"
	conf := &config.RateLimitingConfig{
		Enabled:            true,
		WhitelistedPubkeys: []string{whitelistedPubkey},
	}
	conf.Bandwidth.UploadMBPerMinute = 1 // Very low limit

	limiterCalled := false
	limiter := &mockBandwidthLimiter{
		allowBytesFunc: func(ctx context.Context, key string, bytes int64, limitBytes int64, window time.Duration) (bool, int64, time.Time) {
			limiterCalled = true
			return false, 0, time.Now().Add(time.Minute)
		},
	}

	_ = setupBandwidthTestRouter(conf, limiter)

	body := bytes.NewBufferString("test upload data")
	req := httptest.NewRequest(http.MethodPut, "/upload", body)
	req.ContentLength = int64(body.Len())
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	c.Set("pk", whitelistedPubkey)

	middleware := BandwidthLimitMiddleware(limiter, conf, zap.NewNop())
	middleware(c)

	assert.False(t, limiterCalled, "Limiter should not be called for whitelisted pubkey")
	assert.False(t, c.IsAborted(), "Whitelisted request should pass through")
}

// TestBandwidthLimitMiddleware_Disabled tests that middleware is bypassed when disabled
func TestBandwidthLimitMiddleware_Disabled(t *testing.T) {
	conf := &config.RateLimitingConfig{
		Enabled: false,
	}

	limiterCalled := false
	limiter := &mockBandwidthLimiter{
		allowBytesFunc: func(ctx context.Context, key string, bytes int64, limitBytes int64, window time.Duration) (bool, int64, time.Time) {
			limiterCalled = true
			return false, 0, time.Now().Add(time.Minute)
		},
	}

	r := setupBandwidthTestRouter(conf, limiter)

	body := bytes.NewBufferString("test upload data")
	req := httptest.NewRequest(http.MethodPut, "/upload", body)
	req.ContentLength = int64(body.Len())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.False(t, limiterCalled, "Limiter should not be called when disabled")
}

// TestBandwidthLimitMiddleware_Download tests download bandwidth tracking
func TestBandwidthLimitMiddleware_Download(t *testing.T) {
	conf := &config.RateLimitingConfig{
		Enabled: true,
	}
	conf.Bandwidth.DownloadMBPerMinute = 100

	var capturedKey string
	var capturedBytes int64
	limiter := &mockBandwidthLimiter{
		allowBytesFunc: func(ctx context.Context, key string, bytes int64, limitBytes int64, window time.Duration) (bool, int64, time.Time) {
			capturedKey = key
			capturedBytes = bytes
			return true, limitBytes - bytes, time.Now().Add(time.Minute)
		},
	}

	r := setupBandwidthTestRouter(conf, limiter)

	req := httptest.NewRequest(http.MethodGet, "/download/abc123", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, capturedKey, "download:")
	assert.Greater(t, capturedBytes, int64(0), "Should track downloaded bytes")
}

// TestBandwidthLimitMiddleware_PerIP tests per-IP bandwidth limiting
func TestBandwidthLimitMiddleware_PerIP(t *testing.T) {
	conf := &config.RateLimitingConfig{
		Enabled: true,
	}
	conf.Bandwidth.UploadMBPerMinute = 10

	var lastKey string
	limiter := &mockBandwidthLimiter{
		allowBytesFunc: func(ctx context.Context, key string, bytes int64, limitBytes int64, window time.Duration) (bool, int64, time.Time) {
			lastKey = key
			return true, limitBytes - bytes, time.Now().Add(time.Minute)
		},
	}

	r := setupBandwidthTestRouter(conf, limiter)

	body := bytes.NewBufferString("test upload data")

	// First request from IP 192.0.2.1
	req1 := httptest.NewRequest(http.MethodPut, "/upload", bytes.NewBufferString("test upload data"))
	req1.ContentLength = int64(body.Len())
	req1.RemoteAddr = "192.0.2.1:12345"
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)

	assert.Equal(t, http.StatusOK, w1.Code)
	assert.Contains(t, lastKey, "ip:")
	firstKey := lastKey

	// Second request from different IP
	req2 := httptest.NewRequest(http.MethodPut, "/upload", bytes.NewBufferString("test upload data"))
	req2.ContentLength = int64(body.Len())
	req2.RemoteAddr = "192.0.2.2:12345"
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)

	assert.Equal(t, http.StatusOK, w2.Code)
	assert.Contains(t, lastKey, "ip:")
	assert.NotEqual(t, firstKey, lastKey, "Different IPs should have different bandwidth keys")
}

// TestBandwidthLimitMiddleware_PerPubkey tests per-pubkey bandwidth limiting
func TestBandwidthLimitMiddleware_PerPubkey(t *testing.T) {
	conf := &config.RateLimitingConfig{
		Enabled: true,
	}
	conf.Bandwidth.UploadMBPerMinute = 10

	var capturedKey string
	limiter := &mockBandwidthLimiter{
		allowBytesFunc: func(ctx context.Context, key string, bytes int64, limitBytes int64, window time.Duration) (bool, int64, time.Time) {
			capturedKey = key
			return true, limitBytes - bytes, time.Now().Add(time.Minute)
		},
	}

	_ = setupBandwidthTestRouter(conf, limiter)

	testPubkey := "xyz789abc123"
	body := bytes.NewBufferString("test upload data")
	req := httptest.NewRequest(http.MethodPut, "/upload", body)
	req.ContentLength = int64(body.Len())

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	c.Set("pk", testPubkey)

	middleware := BandwidthLimitMiddleware(limiter, conf, zap.NewNop())
	middleware(c)

	assert.Contains(t, capturedKey, "pk:"+testPubkey)
	assert.False(t, c.IsAborted())
}

// TestBandwidthLimitMiddleware_RetryAfterHeader tests Retry-After header when bandwidth limit exceeded
func TestBandwidthLimitMiddleware_RetryAfterHeader(t *testing.T) {
	conf := &config.RateLimitingConfig{
		Enabled: true,
	}
	conf.Bandwidth.UploadMBPerMinute = 1

	resetTime := time.Now().Add(45 * time.Second)
	limiter := &mockBandwidthLimiter{
		allowBytesFunc: func(ctx context.Context, key string, bytes int64, limitBytes int64, window time.Duration) (bool, int64, time.Time) {
			return false, 0, resetTime
		},
	}

	r := setupBandwidthTestRouter(conf, limiter)

	body := bytes.NewBufferString("test upload data")
	req := httptest.NewRequest(http.MethodPut, "/upload", body)
	req.ContentLength = int64(body.Len())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusTooManyRequests, w.Code)

	retryAfter := w.Header().Get("Retry-After")
	assert.NotEmpty(t, retryAfter)
	seconds, err := strconv.Atoi(retryAfter)
	require.NoError(t, err)
	assert.Greater(t, seconds, 0)
	assert.LessOrEqual(t, seconds, 60, "Retry-After should be less than window duration")
}

// TestClassifyRequest tests the classifyRequest function behavior
func TestClassifyRequest(t *testing.T) {
	tests := []struct {
		name         string
		method       string
		path         string
		expectedType string
	}{
		{
			name:         "blob download",
			method:       "GET",
			path:         "/abc123",
			expectedType: "download",
		},
		{
			name:         "thumbnail download",
			method:       "GET",
			path:         "/abc123/thumb",
			expectedType: "download",
		},
		{
			name:         "HLS stream",
			method:       "GET",
			path:         "/abc123/hls/master.m3u8",
			expectedType: "download",
		},
		{
			name:         "upload",
			method:       "PUT",
			path:         "/upload",
			expectedType: "upload",
		},
		{
			name:         "media upload",
			method:       "PUT",
			path:         "/media",
			expectedType: "upload",
		},
		{
			name:         "mirror",
			method:       "PUT",
			path:         "/mirror",
			expectedType: "upload",
		},
		{
			name:         "transcode",
			method:       "POST",
			path:         "/abc123/transcode",
			expectedType: "upload",
		},
		{
			name:         "list blobs",
			method:       "GET",
			path:         "/list/pubkey123",
			expectedType: "general",
		},
		{
			name:         "stats",
			method:       "GET",
			path:         "/stats",
			expectedType: "general",
		},
		{
			name:         "metrics",
			method:       "GET",
			path:         "/metrics",
			expectedType: "general",
		},
		{
			name:         "well-known",
			method:       "GET",
			path:         "/.well-known/nostr.json",
			expectedType: "general",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(tt.method, tt.path, nil)

			result := classifyRequest(c)
			assert.Equal(t, tt.expectedType, result)
		})
	}
}

// TestParseDuration tests the parseDuration function
func TestParseDuration(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		defaultVal   time.Duration
		expected     time.Duration
	}{
		{
			name:       "valid 1 minute",
			input:      "1m",
			defaultVal: time.Second,
			expected:   time.Minute,
		},
		{
			name:       "valid 1 hour",
			input:      "1h",
			defaultVal: time.Minute,
			expected:   time.Hour,
		},
		{
			name:       "valid 30 seconds",
			input:      "30s",
			defaultVal: time.Minute,
			expected:   30 * time.Second,
		},
		{
			name:       "empty string uses default",
			input:      "",
			defaultVal: time.Minute,
			expected:   time.Minute,
		},
		{
			name:       "invalid format uses default",
			input:      "invalid",
			defaultVal: time.Minute,
			expected:   time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseDuration(tt.input, tt.defaultVal)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestRateLimitMiddleware_WindowParsing tests that window durations are parsed correctly
func TestRateLimitMiddleware_WindowParsing(t *testing.T) {
	conf := &config.RateLimitingConfig{
		Enabled: true,
	}
	conf.IP.Download.Requests = 10
	conf.IP.Download.Window = "2m" // 2 minutes

	var capturedWindow time.Duration
	limiter := &mockRateLimiter{
		allowFunc: func(ctx context.Context, key string, limit int, window time.Duration) (bool, int, time.Time) {
			capturedWindow = window
			return true, 5, time.Now().Add(window)
		},
	}

	r := setupRateLimitTestRouter(conf, limiter)

	req := httptest.NewRequest(http.MethodGet, "/download/abc123", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 2*time.Minute, capturedWindow, "Window should be parsed correctly")
}

// TestRateLimitMiddleware_MultipleRequests tests sequential requests incrementing rate limit
func TestRateLimitMiddleware_MultipleRequests(t *testing.T) {
	conf := &config.RateLimitingConfig{
		Enabled: true,
	}
	conf.IP.Download.Requests = 3
	conf.IP.Download.Window = "1m"

	requestCount := 0
	limiter := &mockRateLimiter{
		allowFunc: func(ctx context.Context, key string, limit int, window time.Duration) (bool, int, time.Time) {
			requestCount++
			remaining := limit - requestCount
			if remaining < 0 {
				remaining = 0
				return false, 0, time.Now().Add(window)
			}
			return true, remaining, time.Now().Add(window)
		},
	}

	r := setupRateLimitTestRouter(conf, limiter)

	// First request - should succeed
	req1 := httptest.NewRequest(http.MethodGet, "/download/abc123", nil)
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)
	assert.Equal(t, http.StatusOK, w1.Code)

	// Second request - should succeed
	req2 := httptest.NewRequest(http.MethodGet, "/download/abc123", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusOK, w2.Code)

	// Third request - should succeed
	req3 := httptest.NewRequest(http.MethodGet, "/download/abc123", nil)
	w3 := httptest.NewRecorder()
	r.ServeHTTP(w3, req3)
	assert.Equal(t, http.StatusOK, w3.Code)

	// Fourth request - should be rate limited
	req4 := httptest.NewRequest(http.MethodGet, "/download/abc123", nil)
	w4 := httptest.NewRecorder()
	r.ServeHTTP(w4, req4)
	assert.Equal(t, http.StatusTooManyRequests, w4.Code)
}
