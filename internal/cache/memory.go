package cache

import (
	"context"
	"sync"
	"time"
)

type memoryEntry struct {
	data      []byte
	expiresAt time.Time
}

// MemoryCache is an in-process cache with size limits and TTL support.
type MemoryCache struct {
	mu          sync.RWMutex
	items       map[string]memoryEntry
	maxSize     int64
	currentSize int64
}

// NewMemoryCache creates a new in-memory cache with the given max size in bytes.
func NewMemoryCache(maxSize int64) *MemoryCache {
	return &MemoryCache{
		items:   make(map[string]memoryEntry),
		maxSize: maxSize,
	}
}

func (c *MemoryCache) Get(_ context.Context, key string) ([]byte, bool) {
	c.mu.RLock()
	entry, ok := c.items[key]
	c.mu.RUnlock()

	if !ok {
		return nil, false
	}

	if !entry.expiresAt.IsZero() && time.Now().After(entry.expiresAt) {
		c.Delete(context.Background(), key)
		return nil, false
	}

	return entry.data, true
}

func (c *MemoryCache) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	newSize := int64(len(value))

	// Remove existing entry size if overwriting
	if existing, ok := c.items[key]; ok {
		c.currentSize -= int64(len(existing.data))
	}

	// Evict if adding this would exceed max
	if c.currentSize+newSize > c.maxSize {
		c.items = make(map[string]memoryEntry)
		c.currentSize = 0
	}

	entry := memoryEntry{data: value}
	if ttl > 0 {
		entry.expiresAt = time.Now().Add(ttl)
	}

	c.items[key] = entry
	c.currentSize += newSize
	return nil
}

func (c *MemoryCache) Delete(_ context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if entry, ok := c.items[key]; ok {
		c.currentSize -= int64(len(entry.data))
		delete(c.items, key)
	}
	return nil
}

func (c *MemoryCache) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = make(map[string]memoryEntry)
	c.currentSize = 0
	return nil
}
