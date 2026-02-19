package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRateLimiterAllow(t *testing.T) {
	limiter := NewRateLimiter(nil) // Use local storage

	ctx := context.Background()
	key := "test-key"
	limit := 5
	window := time.Second

	// First 5 requests should be allowed
	for i := 0; i < limit; i++ {
		allowed, remaining, _ := limiter.Allow(ctx, key, limit, window)
		assert.True(t, allowed, "request %d should be allowed", i+1)
		assert.Equal(t, limit-i-1, remaining, "remaining should be %d", limit-i-1)
	}

	// 6th request should be denied
	allowed, remaining, _ := limiter.Allow(ctx, key, limit, window)
	assert.False(t, allowed, "6th request should be denied")
	assert.Equal(t, 0, remaining, "remaining should be 0")
}

func TestRateLimiterAllowN(t *testing.T) {
	limiter := NewRateLimiter(nil)

	ctx := context.Background()
	key := "test-key-n"
	limit := 10
	window := time.Second

	// Request 3 at once
	allowed, remaining, _ := limiter.AllowN(ctx, key, 3, limit, window)
	assert.True(t, allowed, "3 requests should be allowed")
	assert.Equal(t, 7, remaining)

	// Request 5 more
	allowed, remaining, _ = limiter.AllowN(ctx, key, 5, limit, window)
	assert.True(t, allowed, "5 more requests should be allowed")
	assert.Equal(t, 2, remaining)

	// Request 3 more (should fail, only 2 remaining)
	allowed, remaining, _ = limiter.AllowN(ctx, key, 3, limit, window)
	assert.False(t, allowed, "3 requests should be denied (only 2 remaining)")
	assert.Equal(t, 2, remaining)
}

func TestRateLimiterWindowReset(t *testing.T) {
	limiter := NewRateLimiter(nil)

	ctx := context.Background()
	key := "test-key-reset"
	limit := 2
	window := 100 * time.Millisecond // Short window for testing

	// Use up the limit
	limiter.Allow(ctx, key, limit, window)
	limiter.Allow(ctx, key, limit, window)

	// Should be denied
	allowed, _, _ := limiter.Allow(ctx, key, limit, window)
	assert.False(t, allowed, "should be denied after limit reached")

	// Wait for window to reset
	time.Sleep(window + 10*time.Millisecond)

	// Should be allowed again
	allowed, remaining, _ := limiter.Allow(ctx, key, limit, window)
	assert.True(t, allowed, "should be allowed after window reset")
	assert.Equal(t, 1, remaining)
}

func TestRateLimiterDifferentKeys(t *testing.T) {
	limiter := NewRateLimiter(nil)

	ctx := context.Background()
	limit := 2
	window := time.Second

	// Key1 uses its limit
	limiter.Allow(ctx, "key1", limit, window)
	limiter.Allow(ctx, "key1", limit, window)

	// Key1 should be denied
	allowed, _, _ := limiter.Allow(ctx, "key1", limit, window)
	assert.False(t, allowed, "key1 should be denied")

	// Key2 should still be allowed
	allowed, _, _ = limiter.Allow(ctx, "key2", limit, window)
	assert.True(t, allowed, "key2 should still be allowed")
}

func TestBandwidthLimiterAllowBytes(t *testing.T) {
	limiter := NewBandwidthLimiter(nil)

	ctx := context.Background()
	key := "test-bandwidth"
	limitBytes := int64(1000)
	window := time.Second

	// First request for 400 bytes
	allowed, remaining, _ := limiter.AllowBytes(ctx, key, 400, limitBytes, window)
	assert.True(t, allowed, "400 bytes should be allowed")
	assert.Equal(t, int64(600), remaining)

	// Second request for 500 bytes
	allowed, remaining, _ = limiter.AllowBytes(ctx, key, 500, limitBytes, window)
	assert.True(t, allowed, "500 bytes should be allowed")
	assert.Equal(t, int64(100), remaining)

	// Third request for 200 bytes (should fail)
	allowed, remaining, _ = limiter.AllowBytes(ctx, key, 200, limitBytes, window)
	assert.False(t, allowed, "200 bytes should be denied (only 100 remaining)")
	assert.Equal(t, int64(100), remaining)
}

func TestBandwidthLimiterWindowReset(t *testing.T) {
	limiter := NewBandwidthLimiter(nil)

	ctx := context.Background()
	key := "test-bandwidth-reset"
	limitBytes := int64(100)
	window := 100 * time.Millisecond

	// Use up the limit
	limiter.AllowBytes(ctx, key, 100, limitBytes, window)

	// Should be denied
	allowed, _, _ := limiter.AllowBytes(ctx, key, 1, limitBytes, window)
	assert.False(t, allowed, "should be denied after limit reached")

	// Wait for window to reset
	time.Sleep(window + 10*time.Millisecond)

	// Should be allowed again
	allowed, remaining, _ := limiter.AllowBytes(ctx, key, 50, limitBytes, window)
	assert.True(t, allowed, "should be allowed after window reset")
	assert.Equal(t, int64(50), remaining)
}

func TestRateLimiterResetTime(t *testing.T) {
	limiter := NewRateLimiter(nil)

	ctx := context.Background()
	window := time.Minute

	_, _, resetAt := limiter.Allow(ctx, "test-reset-time", 10, window)

	// Reset time should be within the next window
	now := time.Now()
	expectedReset := now.Truncate(window).Add(window)

	// Allow 1 second tolerance for test execution
	assert.WithinDuration(t, expectedReset, resetAt, time.Second)
}
