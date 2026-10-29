package ratelimit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/cache"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/metrics"
)

var errCacheDown = errors.New("cache down")

// brokenCache fails every read and write, like Dragonfly during an outage.
type brokenCache struct{}

func (brokenCache) Get(context.Context, string) ([]byte, bool) { return nil, false }
func (brokenCache) Set(context.Context, string, []byte, time.Duration) error {
	return errCacheDown
}
func (brokenCache) Delete(context.Context, string) error { return errCacheDown }
func (brokenCache) Close() error                         { return nil }

// brokenCounter is a brokenCache that also offers the atomic counter, which
// is the path production takes.
type brokenCounter struct{ brokenCache }

func (brokenCounter) IncrBy(context.Context, string, int64, time.Duration) (int64, error) {
	return 0, errCacheDown
}

func failOpenCount(name string) float64 {
	return testutil.ToFloat64(metrics.RateLimiterFailOpenTotal.WithLabelValues(name))
}

// alignToWindow waits for the start of a fresh window, so a test's calls
// cannot straddle a boundary.
func alignToWindow(window time.Duration) {
	time.Sleep(time.Until(time.Now().Truncate(window).Add(window)))
}

// A cache error lets the request through (fail open), and every such request
// is counted, but the warning is logged once per window rather than per request.
func TestRateLimiter_CacheErrorFailsOpenVisibly(t *testing.T) {
	for _, tc := range []struct {
		name string
		c    cache.Cache
	}{
		{"counter_path", brokenCounter{}},
		{"get_set_path", brokenCache{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			core, logs := observer.New(zapcore.WarnLevel)
			name := "test_requests_" + tc.name
			limiter := NewRateLimiter(tc.c, WithLogger(zap.New(core)), WithName(name))
			before := failOpenCount(name)

			const window = 300 * time.Millisecond
			alignToWindow(window)
			for i := 0; i < 3; i++ {
				ok, _, _ := limiter.Allow(context.Background(), "k", 1, window)
				assert.True(t, ok, "a cache error must not refuse the request")
			}

			assert.Equal(t, 3.0, failOpenCount(name)-before, "every fail-open is counted")
			require.Equal(t, 1, logs.Len(), "logged once for the window, not per request")
			entry := logs.All()[0]
			assert.Equal(t, name, entry.ContextMap()["limiter"])
			assert.Equal(t, errCacheDown.Error(), entry.ContextMap()["error"])

			alignToWindow(window)
			ok, _, _ := limiter.Allow(context.Background(), "k", 1, window)
			assert.True(t, ok)
			assert.Equal(t, 2, logs.Len(), "a new window logs again")
		})
	}
}

func TestBandwidthLimiter_CacheErrorFailsOpenVisibly(t *testing.T) {
	core, logs := observer.New(zapcore.WarnLevel)
	const name = "test_bandwidth"
	limiter := NewBandwidthLimiter(brokenCounter{}, WithLogger(zap.New(core)), WithName(name))
	before := failOpenCount(name)

	const window = 300 * time.Millisecond
	alignToWindow(window)
	for i := 0; i < 3; i++ {
		ok, _, _ := limiter.AllowBytes(context.Background(), "k", 10, 1, window)
		assert.True(t, ok, "a cache error must not refuse the transfer")
	}

	assert.Equal(t, 3.0, failOpenCount(name)-before)
	assert.Equal(t, 1, logs.Len())
}
