package cache

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Runs against a real Redis/Dragonfly when BLOSSOM_TEST_REDIS_URL is set
// (e.g. redis://localhost:6379/15); skipped otherwise.
func TestRedisIncrBy(t *testing.T) {
	url := os.Getenv("BLOSSOM_TEST_REDIS_URL")
	if url == "" {
		t.Skip("BLOSSOM_TEST_REDIS_URL not set")
	}
	c, err := NewRedisCache(url, "blossomtest:"+t.Name()+":")
	require.NoError(t, err)
	defer c.Close()
	ctx := context.Background()
	key := "ctr"
	_ = c.Delete(ctx, key)
	defer c.Delete(ctx, key)

	// Atomic under a parallel burst.
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = c.IncrBy(ctx, key, 2, 2*time.Second) }()
	}
	wg.Wait()
	v, err := c.IncrBy(ctx, key, 0, 2*time.Second)
	require.NoError(t, err)
	assert.EqualValues(t, 200, v)

	// TTL is set at creation and not extended by later increments.
	ttl1 := c.client.PTTL(ctx, c.key(key)).Val()
	time.Sleep(1100 * time.Millisecond)
	_, _ = c.IncrBy(ctx, key, 1, 2*time.Second)
	ttl2 := c.client.PTTL(ctx, c.key(key)).Val()
	assert.Less(t, ttl2, ttl1, "increments must not push the expiry back")
	time.Sleep(time.Second)
	v, _ = c.IncrBy(ctx, key, 1, 2*time.Second)
	assert.EqualValues(t, 1, v, "the key expired on schedule and restarted")
}
