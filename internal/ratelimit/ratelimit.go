package ratelimit

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/cache"
)

// RateLimiter provides rate limiting functionality.
type RateLimiter interface {
	// Allow checks if a request is allowed for the given key.
	// Returns true if allowed, false if rate limited.
	// Also returns the current count and reset time.
	Allow(ctx context.Context, key string, limit int, window time.Duration) (allowed bool, remaining int, resetAt time.Time)

	// AllowN checks if N requests are allowed for the given key.
	AllowN(ctx context.Context, key string, n int, limit int, window time.Duration) (allowed bool, remaining int, resetAt time.Time)
}

// LimitConfig defines rate limit configuration for a specific limit type.
type LimitConfig struct {
	Requests int           // Max requests per window
	Window   time.Duration // Time window (e.g., 1 minute)
}

// Config holds all rate limiting configuration.
type Config struct {
	Enabled bool // Global enable/disable

	// Per-IP limits (for unauthenticated requests)
	IPLimits struct {
		Download LimitConfig // GET requests for blobs
		Upload   LimitConfig // PUT/POST requests
		General  LimitConfig // All other requests
	}

	// Per-pubkey limits (for authenticated requests)
	PubkeyLimits struct {
		Download LimitConfig // GET requests for blobs
		Upload   LimitConfig // PUT/POST requests
		General  LimitConfig // All other requests
	}

	// Bandwidth limits (bytes per window)
	BandwidthLimits struct {
		DownloadBytesPerMinute int64 // Max download bytes per minute per key
		UploadBytesPerMinute   int64 // Max upload bytes per minute per key
	}

	// Whitelist of pubkeys exempt from rate limiting
	WhitelistedPubkeys []string
}

// slidingWindowLimiter implements RateLimiter using sliding window counters.
type slidingWindowLimiter struct {
	cache cache.Cache
	mu    sync.Mutex // For local fallback when cache is nil
	local map[string]*localBucket
}

type localBucket struct {
	count    int
	windowAt time.Time
}

type cacheEntry struct {
	Count    int   `json:"c"`
	WindowAt int64 `json:"w"`
}

// NewRateLimiter creates a new rate limiter.
// If cache is nil, uses in-memory storage (not distributed).
func NewRateLimiter(c cache.Cache) RateLimiter {
	r := &slidingWindowLimiter{
		cache: c,
		local: make(map[string]*localBucket),
	}
	// Start background cleanup to prevent memory leaks
	go r.cleanupLoop()
	return r
}

// cleanupLoop periodically removes expired entries from local map.
func (r *slidingWindowLimiter) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		r.cleanup()
	}
}

// cleanup removes entries older than 2 windows.
func (r *slidingWindowLimiter) cleanup() {
	r.mu.Lock()
	defer r.mu.Unlock()

	cutoff := time.Now().Add(-10 * time.Minute) // Keep recent windows
	for key, bucket := range r.local {
		if bucket.windowAt.Before(cutoff) {
			delete(r.local, key)
		}
	}
}

func (r *slidingWindowLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, int, time.Time) {
	return r.AllowN(ctx, key, 1, limit, window)
}

func (r *slidingWindowLimiter) AllowN(ctx context.Context, key string, n int, limit int, window time.Duration) (bool, int, time.Time) {
	now := time.Now()
	windowStart := now.Truncate(window)
	resetAt := windowStart.Add(window)

	cacheKey := fmt.Sprintf("rl:%s:%d", key, windowStart.Unix())

	// Try cache first
	if r.cache != nil {
		return r.allowWithCache(ctx, cacheKey, n, limit, window, resetAt)
	}

	// Fall back to local storage
	return r.allowLocal(key, n, limit, window, windowStart, resetAt)
}

func (r *slidingWindowLimiter) allowWithCache(ctx context.Context, cacheKey string, n int, limit int, window time.Duration, resetAt time.Time) (bool, int, time.Time) {
	// Get current count
	var entry cacheEntry
	if data, ok := r.cache.Get(ctx, cacheKey); ok {
		_ = json.Unmarshal(data, &entry)
	}

	// Check if allowed
	newCount := entry.Count + n
	if newCount > limit {
		remaining := limit - entry.Count
		if remaining < 0 {
			remaining = 0
		}
		return false, remaining, resetAt
	}

	// Increment count
	entry.Count = newCount
	entry.WindowAt = resetAt.Unix()
	data, _ := json.Marshal(entry)
	_ = r.cache.Set(ctx, cacheKey, data, window+time.Second) // TTL slightly longer than window

	remaining := limit - newCount
	return true, remaining, resetAt
}

func (r *slidingWindowLimiter) allowLocal(key string, n int, limit int, window time.Duration, windowStart time.Time, resetAt time.Time) (bool, int, time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()

	bucket, exists := r.local[key]
	if !exists || bucket.windowAt.Before(windowStart) {
		// New window
		bucket = &localBucket{
			count:    0,
			windowAt: windowStart,
		}
		r.local[key] = bucket
	}

	// Check if allowed
	newCount := bucket.count + n
	if newCount > limit {
		remaining := limit - bucket.count
		if remaining < 0 {
			remaining = 0
		}
		return false, remaining, resetAt
	}

	// Increment count
	bucket.count = newCount
	remaining := limit - newCount
	return true, remaining, resetAt
}

// BandwidthLimiter provides bandwidth-based rate limiting.
type BandwidthLimiter interface {
	// AllowBytes checks if transferring N bytes is allowed.
	AllowBytes(ctx context.Context, key string, bytes int64, limitBytes int64, window time.Duration) (allowed bool, remaining int64, resetAt time.Time)
}

// bandwidthLimiter implements BandwidthLimiter.
type bandwidthLimiter struct {
	cache cache.Cache
	mu    sync.Mutex
	local map[string]*localBandwidthBucket
}

type localBandwidthBucket struct {
	bytes    int64
	windowAt time.Time
}

type bandwidthCacheEntry struct {
	Bytes    int64 `json:"b"`
	WindowAt int64 `json:"w"`
}

// NewBandwidthLimiter creates a new bandwidth limiter.
func NewBandwidthLimiter(c cache.Cache) BandwidthLimiter {
	b := &bandwidthLimiter{
		cache: c,
		local: make(map[string]*localBandwidthBucket),
	}
	// Start background cleanup to prevent memory leaks
	go b.cleanupLoop()
	return b
}

// cleanupLoop periodically removes expired entries from local map.
func (b *bandwidthLimiter) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		b.cleanup()
	}
}

// cleanup removes entries older than 2 windows.
func (b *bandwidthLimiter) cleanup() {
	b.mu.Lock()
	defer b.mu.Unlock()

	cutoff := time.Now().Add(-10 * time.Minute) // Keep recent windows
	for key, bucket := range b.local {
		if bucket.windowAt.Before(cutoff) {
			delete(b.local, key)
		}
	}
}

func (b *bandwidthLimiter) AllowBytes(ctx context.Context, key string, bytes int64, limitBytes int64, window time.Duration) (bool, int64, time.Time) {
	now := time.Now()
	windowStart := now.Truncate(window)
	resetAt := windowStart.Add(window)

	cacheKey := fmt.Sprintf("bw:%s:%d", key, windowStart.Unix())

	if b.cache != nil {
		return b.allowWithCache(ctx, cacheKey, bytes, limitBytes, window, resetAt)
	}

	return b.allowLocal(key, bytes, limitBytes, window, windowStart, resetAt)
}

func (b *bandwidthLimiter) allowWithCache(ctx context.Context, cacheKey string, bytes int64, limitBytes int64, window time.Duration, resetAt time.Time) (bool, int64, time.Time) {
	var entry bandwidthCacheEntry
	if data, ok := b.cache.Get(ctx, cacheKey); ok {
		_ = json.Unmarshal(data, &entry)
	}

	newBytes := entry.Bytes + bytes
	if newBytes > limitBytes {
		remaining := limitBytes - entry.Bytes
		if remaining < 0 {
			remaining = 0
		}
		return false, remaining, resetAt
	}

	entry.Bytes = newBytes
	entry.WindowAt = resetAt.Unix()
	data, _ := json.Marshal(entry)
	_ = b.cache.Set(ctx, cacheKey, data, window+time.Second)

	remaining := limitBytes - newBytes
	return true, remaining, resetAt
}

func (b *bandwidthLimiter) allowLocal(key string, bytes int64, limitBytes int64, window time.Duration, windowStart time.Time, resetAt time.Time) (bool, int64, time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()

	bucket, exists := b.local[key]
	if !exists || bucket.windowAt.Before(windowStart) {
		bucket = &localBandwidthBucket{
			bytes:    0,
			windowAt: windowStart,
		}
		b.local[key] = bucket
	}

	newBytes := bucket.bytes + bytes
	if newBytes > limitBytes {
		remaining := limitBytes - bucket.bytes
		if remaining < 0 {
			remaining = 0
		}
		return false, remaining, resetAt
	}

	bucket.bytes = newBytes
	remaining := limitBytes - newBytes
	return true, remaining, resetAt
}
