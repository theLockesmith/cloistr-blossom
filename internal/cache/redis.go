package cache

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisCache implements Cache using Redis/Dragonfly.
type RedisCache struct {
	client *redis.Client
	prefix string
}

// NewRedisCache creates a cache backed by Redis or Dragonfly.
// url should be a Redis URL like "redis://host:port" or "redis://:password@host:port".
func NewRedisCache(url string, prefix string) (*RedisCache, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}

	client := redis.NewClient(opts)

	// Verify connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		client.Close()
		return nil, err
	}

	return &RedisCache{
		client: client,
		prefix: prefix,
	}, nil
}

func (c *RedisCache) key(k string) string {
	return c.prefix + k
}

func (c *RedisCache) Get(ctx context.Context, key string) ([]byte, bool) {
	val, err := c.client.Get(ctx, c.key(key)).Bytes()
	if err != nil {
		return nil, false
	}
	return val, true
}

func (c *RedisCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return c.client.Set(ctx, c.key(key), value, ttl).Err()
}

func (c *RedisCache) Delete(ctx context.Context, key string) error {
	return c.client.Del(ctx, c.key(key)).Err()
}

func (c *RedisCache) Close() error {
	return c.client.Close()
}

// IncrBy implements Counter. SETNX creates the key at 0 with its TTL, then
// INCRBY adds; MULTI/EXEC makes the pair atomic. The TTL is set only when
// the key is created, so later hits never push the expiry back.
func (c *RedisCache) IncrBy(ctx context.Context, key string, n int64, ttl time.Duration) (int64, error) {
	k := c.key(key)
	var incr *redis.IntCmd
	_, err := c.client.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.SetNX(ctx, k, 0, ttl)
		incr = p.IncrBy(ctx, k, n)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return incr.Val(), nil
}
