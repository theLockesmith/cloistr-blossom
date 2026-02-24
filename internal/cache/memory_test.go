package cache

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryCache_BasicOperations(t *testing.T) {
	cache := NewMemoryCache(1024)
	ctx := context.Background()

	// Test Set and Get
	err := cache.Set(ctx, "key1", []byte("value1"), 0)
	require.NoError(t, err)

	data, ok := cache.Get(ctx, "key1")
	assert.True(t, ok)
	assert.Equal(t, []byte("value1"), data)

	// Test non-existent key
	data, ok = cache.Get(ctx, "nonexistent")
	assert.False(t, ok)
	assert.Nil(t, data)

	// Test Delete
	err = cache.Delete(ctx, "key1")
	require.NoError(t, err)

	data, ok = cache.Get(ctx, "key1")
	assert.False(t, ok)
	assert.Nil(t, data)
}

func TestMemoryCache_TTLExpiration(t *testing.T) {
	cache := NewMemoryCache(1024)
	ctx := context.Background()

	// Set with short TTL
	err := cache.Set(ctx, "key1", []byte("value1"), 50*time.Millisecond)
	require.NoError(t, err)

	// Should be accessible immediately
	data, ok := cache.Get(ctx, "key1")
	assert.True(t, ok)
	assert.Equal(t, []byte("value1"), data)

	// Wait for expiration
	time.Sleep(60 * time.Millisecond)

	// Should be expired now
	data, ok = cache.Get(ctx, "key1")
	assert.False(t, ok)
	assert.Nil(t, data)
}

func TestMemoryCache_LRUEviction(t *testing.T) {
	// Cache with max size of 30 bytes
	cache := NewMemoryCache(30)
	ctx := context.Background()

	// Add three 10-byte entries (fills cache)
	err := cache.Set(ctx, "key1", []byte("1234567890"), 0)
	require.NoError(t, err)
	err = cache.Set(ctx, "key2", []byte("abcdefghij"), 0)
	require.NoError(t, err)
	err = cache.Set(ctx, "key3", []byte("ABCDEFGHIJ"), 0)
	require.NoError(t, err)

	// All three should exist
	_, ok := cache.Get(ctx, "key1")
	assert.True(t, ok)
	_, ok = cache.Get(ctx, "key2")
	assert.True(t, ok)
	_, ok = cache.Get(ctx, "key3")
	assert.True(t, ok)

	// Access key1 to make it most recently used
	cache.Get(ctx, "key1")

	// Add a fourth entry - should evict key2 (LRU)
	err = cache.Set(ctx, "key4", []byte("xxxxxxxxxx"), 0)
	require.NoError(t, err)

	// key1 should still exist (was accessed)
	_, ok = cache.Get(ctx, "key1")
	assert.True(t, ok, "key1 should still exist after LRU eviction")

	// key2 should be evicted (was LRU)
	_, ok = cache.Get(ctx, "key2")
	assert.False(t, ok, "key2 should be evicted (LRU)")

	// key3 and key4 should exist
	_, ok = cache.Get(ctx, "key3")
	assert.True(t, ok)
	_, ok = cache.Get(ctx, "key4")
	assert.True(t, ok)
}

func TestMemoryCache_EvictMultiple(t *testing.T) {
	// Cache with max size of 30 bytes
	cache := NewMemoryCache(30)
	ctx := context.Background()

	// Add three 10-byte entries
	cache.Set(ctx, "key1", []byte("1234567890"), 0)
	cache.Set(ctx, "key2", []byte("abcdefghij"), 0)
	cache.Set(ctx, "key3", []byte("ABCDEFGHIJ"), 0)

	// Add a 25-byte entry - should evict multiple entries
	err := cache.Set(ctx, "big", []byte("1234567890123456789012345"), 0)
	require.NoError(t, err)

	// Only the big entry should fit
	_, ok := cache.Get(ctx, "big")
	assert.True(t, ok)

	// Previous entries should be evicted
	_, ok = cache.Get(ctx, "key1")
	assert.False(t, ok)
	_, ok = cache.Get(ctx, "key2")
	assert.False(t, ok)
	_, ok = cache.Get(ctx, "key3")
	assert.False(t, ok)
}

func TestMemoryCache_OversizedItem(t *testing.T) {
	// Cache with max size of 10 bytes
	cache := NewMemoryCache(10)
	ctx := context.Background()

	// Try to add a 20-byte entry (larger than max)
	err := cache.Set(ctx, "big", []byte("12345678901234567890"), 0)
	require.NoError(t, err) // Should not error, just skip caching

	// Entry should not exist
	_, ok := cache.Get(ctx, "big")
	assert.False(t, ok, "oversized item should not be cached")

	// Cache should still work for smaller items
	err = cache.Set(ctx, "small", []byte("12345"), 0)
	require.NoError(t, err)
	_, ok = cache.Get(ctx, "small")
	assert.True(t, ok)
}

func TestMemoryCache_Overwrite(t *testing.T) {
	cache := NewMemoryCache(100)
	ctx := context.Background()

	// Set initial value
	err := cache.Set(ctx, "key", []byte("initial"), 0)
	require.NoError(t, err)

	// Overwrite with new value
	err = cache.Set(ctx, "key", []byte("updated"), 0)
	require.NoError(t, err)

	// Should get updated value
	data, ok := cache.Get(ctx, "key")
	assert.True(t, ok)
	assert.Equal(t, []byte("updated"), data)

	// Size should be correct (not double-counted)
	size, count := cache.Stats()
	assert.Equal(t, int64(7), size) // "updated" is 7 bytes
	assert.Equal(t, 1, count)
}

func TestMemoryCache_Stats(t *testing.T) {
	cache := NewMemoryCache(1024)
	ctx := context.Background()

	size, count := cache.Stats()
	assert.Equal(t, int64(0), size)
	assert.Equal(t, 0, count)

	cache.Set(ctx, "key1", []byte("12345"), 0) // 5 bytes
	cache.Set(ctx, "key2", []byte("1234567890"), 0) // 10 bytes

	size, count = cache.Stats()
	assert.Equal(t, int64(15), size)
	assert.Equal(t, 2, count)

	cache.Delete(ctx, "key1")

	size, count = cache.Stats()
	assert.Equal(t, int64(10), size)
	assert.Equal(t, 1, count)
}

func TestMemoryCache_Close(t *testing.T) {
	cache := NewMemoryCache(1024)
	ctx := context.Background()

	cache.Set(ctx, "key1", []byte("value1"), 0)
	cache.Set(ctx, "key2", []byte("value2"), 0)

	err := cache.Close()
	require.NoError(t, err)

	// Cache should be empty
	size, count := cache.Stats()
	assert.Equal(t, int64(0), size)
	assert.Equal(t, 0, count)

	_, ok := cache.Get(ctx, "key1")
	assert.False(t, ok)
}
