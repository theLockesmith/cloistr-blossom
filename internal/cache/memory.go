package cache

import (
	"container/list"
	"context"
	"sync"
	"time"
)

type memoryEntry struct {
	key       string
	data      []byte
	expiresAt time.Time
	element   *list.Element // Pointer to LRU list element
}

// MemoryCache is an in-process LRU cache with size limits and TTL support.
type MemoryCache struct {
	mu          sync.Mutex
	items       map[string]*memoryEntry
	lruList     *list.List // Front = most recently used, Back = least recently used
	maxSize     int64
	currentSize int64
}

// NewMemoryCache creates a new in-memory LRU cache with the given max size in bytes.
func NewMemoryCache(maxSize int64) *MemoryCache {
	return &MemoryCache{
		items:   make(map[string]*memoryEntry),
		lruList: list.New(),
		maxSize: maxSize,
	}
}

func (c *MemoryCache) Get(_ context.Context, key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.items[key]
	if !ok {
		return nil, false
	}

	// Check TTL expiration
	if !entry.expiresAt.IsZero() && time.Now().After(entry.expiresAt) {
		c.removeEntry(entry)
		return nil, false
	}

	// Move to front of LRU list (most recently used)
	c.lruList.MoveToFront(entry.element)

	return entry.data, true
}

func (c *MemoryCache) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	newSize := int64(len(value))

	// Remove existing entry if overwriting
	if existing, ok := c.items[key]; ok {
		c.removeEntry(existing)
	}

	// Evict LRU entries until there's enough space
	for c.currentSize+newSize > c.maxSize && c.lruList.Len() > 0 {
		c.evictLRU()
	}

	// If single item is larger than max, don't cache it
	if newSize > c.maxSize {
		return nil
	}

	entry := &memoryEntry{
		key:  key,
		data: value,
	}
	if ttl > 0 {
		entry.expiresAt = time.Now().Add(ttl)
	}

	// Add to front of LRU list
	entry.element = c.lruList.PushFront(entry)
	c.items[key] = entry
	c.currentSize += newSize

	return nil
}

func (c *MemoryCache) Delete(_ context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if entry, ok := c.items[key]; ok {
		c.removeEntry(entry)
	}
	return nil
}

func (c *MemoryCache) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items = make(map[string]*memoryEntry)
	c.lruList.Init()
	c.currentSize = 0
	return nil
}

// removeEntry removes an entry from both the map and LRU list (must hold lock)
func (c *MemoryCache) removeEntry(entry *memoryEntry) {
	c.currentSize -= int64(len(entry.data))
	c.lruList.Remove(entry.element)
	delete(c.items, entry.key)
}

// evictLRU removes the least recently used entry (must hold lock)
func (c *MemoryCache) evictLRU() {
	back := c.lruList.Back()
	if back == nil {
		return
	}
	entry := back.Value.(*memoryEntry)
	c.removeEntry(entry)
}

// Stats returns cache statistics for monitoring
func (c *MemoryCache) Stats() (size int64, count int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.currentSize, len(c.items)
}
