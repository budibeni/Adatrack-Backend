package controllers

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisKV is the small Redis surface the service needs (revocation denylist +
// rate limiting). A fake implementation backs the unit tests.
type RedisKV struct {
	client redis.Cmdable
}

// NewRedisKV wraps a Redis client (or a cluster/universal cmdable).
func NewRedisKV(client redis.Cmdable) *RedisKV { return &RedisKV{client: client} }

// Get returns "" for a missing key.
func (k *RedisKV) Get(ctx context.Context, key string) (string, error) {
	val, err := k.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return "", nil
	}
	return val, err
}

// Set stores a value with a TTL.
func (k *RedisKV) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	return k.client.Set(ctx, key, value, ttl).Err()
}

// Incr increments a counter, returning the new value.
func (k *RedisKV) Incr(ctx context.Context, key string) (int64, error) {
	return k.client.Incr(ctx, key).Result()
}

// Expire sets a TTL on an existing key.
func (k *RedisKV) Expire(ctx context.Context, key string, ttl time.Duration) error {
	return k.client.Expire(ctx, key, ttl).Err()
}

// Ping verifies connectivity (healthz).
func (k *RedisKV) Ping(ctx context.Context) error {
	return k.client.Ping(ctx).Err()
}
