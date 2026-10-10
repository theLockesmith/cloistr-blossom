package ratelimit

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/cache"
)

// Production runs the limiter on a shared cache (Dragonfly) across two pods.
// A read-then-write counter lets a parallel burst through far past the limit;
// the count has to be one atomic increment.
func TestRateLimiterConcurrentBurstWithCache(t *testing.T) {
	limiter := NewRateLimiter(cache.NewMemoryCache(1 << 20))
	const limit, burst = 50, 200

	var allowed atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < burst; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if ok, _, _ := limiter.Allow(context.Background(), "ip:203.0.113.7", limit, time.Minute); ok {
				allowed.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	assert.EqualValues(t, limit, allowed.Load())
}

func TestBandwidthLimiterConcurrentBurstWithCache(t *testing.T) {
	limiter := NewBandwidthLimiter(cache.NewMemoryCache(1 << 20))
	const chunk, limitBytes, burst = 1000, 50_000, 200

	var allowed atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < burst; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if ok, _, _ := limiter.AllowBytes(context.Background(), "upload:ip:203.0.113.7", chunk, limitBytes, time.Minute); ok {
				allowed.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	assert.EqualValues(t, limitBytes/chunk, allowed.Load())
}

// The window is fixed: a key's expiry is set when the window's key is created
// and never pushed back by later hits, so steady traffic cannot keep a bucket
// alive (and full) past its reset time.
func TestRateLimiterFixedWindowWithCache(t *testing.T) {
	limiter := NewRateLimiter(cache.NewMemoryCache(1 << 20))
	ctx := context.Background()
	window := 200 * time.Millisecond

	// Align to the start of a window so the burst cannot straddle a boundary.
	time.Sleep(time.Until(time.Now().Truncate(window).Add(window)))
	for i := 0; i < 3; i++ {
		ok, _, _ := limiter.Allow(ctx, "k", 3, window)
		assert.True(t, ok)
	}
	ok, _, resetAt := limiter.Allow(ctx, "k", 3, window)
	assert.False(t, ok, "fourth hit in the window is refused")

	time.Sleep(time.Until(resetAt) + 10*time.Millisecond)
	ok, remaining, _ := limiter.Allow(ctx, "k", 3, window)
	assert.True(t, ok, "a new window starts fresh")
	assert.Equal(t, 2, remaining)
}
