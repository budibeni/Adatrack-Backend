package controllers

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"

	"adatrack_gps/internal"
)

// DefaultRedisKeyPrefix is the canonical live-state namespace (REDIS_KEY_PREFIX)
// shared with worker-live (FR-2.1).
const DefaultRedisKeyPrefix = "adatrack_gps:"

// RedisKV is the small Redis surface the service needs (revocation denylist,
// rate limiting and the live-state overlay). A fake implementation backs the
// unit tests.
type RedisKV struct {
	client redis.Cmdable
	// keyPrefix namespaces the live-state keys; it MUST match the prefix
	// worker-live writes with, otherwise the overlay silently misses every key.
	keyPrefix string
}

// NewRedisKV wraps a Redis client with the canonical key namespace.
func NewRedisKV(client redis.Cmdable) *RedisKV {
	return NewRedisKVWithPrefix(client, DefaultRedisKeyPrefix)
}

// NewRedisKVWithPrefix wraps a Redis client with an explicit key namespace
// (main.go passes cfg.Redis.KeyPrefix so api-vehicle reads exactly the keys
// worker-live wrote).
func NewRedisKVWithPrefix(client redis.Cmdable, keyPrefix string) *RedisKV {
	if keyPrefix == "" {
		keyPrefix = DefaultRedisKeyPrefix
	}
	return &RedisKV{client: client, keyPrefix: keyPrefix}
}

// Get returns "" for a missing key.
func (k *RedisKV) Get(ctx context.Context, key string) (string, error) {
	val, err := k.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return "", nil
	}
	return val, err
}

// MGet returns one raw value per key ("" for a missing key) in ONE round trip
// (FR-2.3: batched reads keep the fleet listing latency bounded).
func (k *RedisKV) MGet(ctx context.Context, keys ...string) ([]string, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	values, err := k.client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	out := make([]string, len(values))
	for i, v := range values {
		if s, ok := v.(string); ok {
			out[i] = s
		}
	}
	return out, nil
}

// LiveStateKey returns the tenant-scoped live-state key of one IMEI, built with
// the SAME layout worker-live writes (FR-2.1):
//
//	adatrack_gps:{company_code}:vehicle:state:{IMEI}
func (k *RedisKV) LiveStateKey(companyCode, imei string) string {
	return internal.LiveStateKeyFor(k.keyPrefix, companyCode, imei)
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
