package cache

import (
	"context"
	"time"
)

// Cache provides a key-value cache interface.
// Implementations include in-memory (default) and Redis/Dragonfly (optional).
type Cache interface {
	// Get retrieves a value by key. Returns nil, false if not found.
	Get(ctx context.Context, key string) ([]byte, bool)

	// Set stores a value with an optional TTL. Zero TTL means no expiration.
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error

	// Delete removes a value by key.
	Delete(ctx context.Context, key string) error

	// Close cleans up resources.
	Close() error
}

// Counter is implemented by caches that can increment a counter atomically.
// Rate limiting needs it: a Get-then-Set count lets a parallel burst through
// far past the limit.
type Counter interface {
	// IncrBy adds n to the integer at key and returns the new value. A missing
	// key starts at 0 and is created with the given TTL; later increments
	// never extend it, so a counter keyed per window expires on schedule.
	IncrBy(ctx context.Context, key string, n int64, ttl time.Duration) (int64, error)
}
