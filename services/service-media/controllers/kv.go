package controllers

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"

	"adatrack_gps/internal"
)

// KVStore is the Redis surface service-media needs: token-revocation denylist
// lookups (FR-5.7) and the per-user API rate limiter (PRD §8.4).
type KVStore interface {
	Get(ctx context.Context, key string) (string, error)
	Incr(ctx context.Context, key string) (int64, error)
	Expire(ctx context.Context, key string, ttl time.Duration) error
	Ping(ctx context.Context) error
}

// redisKV adapts internal.RedisClient: a missing key is (\"\", nil) instead of
// redis.Nil so callers never depend on the driver's sentinel error.
type redisKV struct{ client *internal.RedisClient }

// NewKV wraps the shared Redis client for service-media.
func NewKV(client *internal.RedisClient) KVStore { return &redisKV{client: client} }

// Get reads a key ("" when absent).
func (k *redisKV) Get(ctx context.Context, key string) (string, error) {
	val, err := k.client.Get(ctx, key)
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	return val, err
}

// Incr increments a counter, returning the new value.
func (k *redisKV) Incr(ctx context.Context, key string) (int64, error) {
	return k.client.Incr(ctx, key)
}

// Expire sets a TTL on an existing key.
func (k *redisKV) Expire(ctx context.Context, key string, ttl time.Duration) error {
	return k.client.Expire(ctx, key, ttl)
}

// Ping verifies connectivity (readiness probe).
func (k *redisKV) Ping(ctx context.Context) error { return k.client.Ping(ctx) }
