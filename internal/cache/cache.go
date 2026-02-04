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
