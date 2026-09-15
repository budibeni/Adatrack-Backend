package controllers

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrKVUnavailable is returned when the key/value backend cannot answer. The API
// converts it into a 503 SERVICE_UNAVAILABLE instead of silently degrading
// (PRD §8.1, §9.6 fail-closed).
var ErrKVUnavailable = errors.New("auth: key/value store unavailable")

// KVStore is the small Redis surface the auth layer needs (refresh records,
// token denylist, rate-limit counters). Keeping it an interface lets the unit
// tests run without Redis while the production adapter is exercised against a
// real server (miniredis) in the adapter test.
type KVStore interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string, ttl time.Duration) error
	Del(ctx context.Context, keys ...string) error
	// Incr increments a counter, returning the new value.
	Incr(ctx context.Context, key string) (int64, error)
	// Expire sets/refreshes a TTL on an existing key.
	Expire(ctx context.Context, key string, ttl time.Duration) error
	Ping(ctx context.Context) error
}

// RedisKV adapts a go-redis client to KVStore (production path).
type RedisKV struct {
	client *redis.Client
}

// NewRedisKV wraps an existing go-redis client.
func NewRedisKV(client *redis.Client) *RedisKV { return &RedisKV{client: client} }

// Get reads a value ("" when the key is missing).
func (r *RedisKV) Get(ctx context.Context, key string) (string, error) {
	val, err := r.client.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	if err != nil {
		return "", errors.Join(ErrKVUnavailable, err)
	}
	return val, nil
}

// Set stores a value with a TTL.
func (r *RedisKV) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	if err := r.client.Set(ctx, key, value, ttl).Err(); err != nil {
		return errors.Join(ErrKVUnavailable, err)
	}
	return nil
}

// Del removes keys (missing keys are not an error).
func (r *RedisKV) Del(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	if err := r.client.Del(ctx, keys...).Err(); err != nil {
		return errors.Join(ErrKVUnavailable, err)
	}
	return nil
}

// Incr increments a counter.
func (r *RedisKV) Incr(ctx context.Context, key string) (int64, error) {
	n, err := r.client.Incr(ctx, key).Result()
	if err != nil {
		return 0, errors.Join(ErrKVUnavailable, err)
	}
	return n, nil
}

// Expire applies a TTL to an existing key.
func (r *RedisKV) Expire(ctx context.Context, key string, ttl time.Duration) error {
	if err := r.client.Expire(ctx, key, ttl).Err(); err != nil {
		return errors.Join(ErrKVUnavailable, err)
	}
	return nil
}

// Ping verifies connectivity (readiness probe).
func (r *RedisKV) Ping(ctx context.Context) error {
	if err := r.client.Ping(ctx).Err(); err != nil {
		return errors.Join(ErrKVUnavailable, err)
	}
	return nil
}

// MemoryKV is an in-memory KVStore used by unit tests (no Redis required).
// Expiry is evaluated on read, which is enough for the fixed-window semantics
// the auth layer relies on.
type MemoryKV struct {
	mu      sync.Mutex
	values  map[string]string
	expires map[string]time.Time
	down    bool
}

// NewMemoryKV builds an empty in-memory store.
func NewMemoryKV() *MemoryKV {
	return &MemoryKV{values: map[string]string{}, expires: map[string]time.Time{}}
}

// SetDown makes every operation fail, simulating an unavailable Redis.
func (m *MemoryKV) SetDown(down bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.down = down
}

// Get implements KVStore.
func (m *MemoryKV) Get(_ context.Context, key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.down {
		return "", ErrKVUnavailable
	}
	if exp, ok := m.expires[key]; ok && time.Now().After(exp) {
		delete(m.values, key)
		delete(m.expires, key)
		return "", nil
	}
	return m.values[key], nil
}

// Set implements KVStore.
func (m *MemoryKV) Set(_ context.Context, key, value string, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.down {
		return ErrKVUnavailable
	}
	m.values[key] = value
	if ttl > 0 {
		m.expires[key] = time.Now().Add(ttl)
	}
	return nil
}

// Del implements KVStore.
func (m *MemoryKV) Del(_ context.Context, keys ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.down {
		return ErrKVUnavailable
	}
	for _, k := range keys {
		delete(m.values, k)
		delete(m.expires, k)
	}
	return nil
}

// Incr implements KVStore.
func (m *MemoryKV) Incr(_ context.Context, key string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.down {
		return 0, ErrKVUnavailable
	}
	var n int64
	if exp, ok := m.expires[key]; ok && time.Now().After(exp) {
		delete(m.values, key)
		delete(m.expires, key)
	}
	if raw, ok := m.values[key]; ok {
		for _, r := range raw {
			if r < '0' || r > '9' {
				n = 0
				break
			}
			n = n*10 + int64(r-'0')
		}
	}
	n++
	m.values[key] = itoa(n)
	return n, nil
}

// Expire implements KVStore.
func (m *MemoryKV) Expire(_ context.Context, key string, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.down {
		return ErrKVUnavailable
	}
	if _, ok := m.values[key]; ok {
		m.expires[key] = time.Now().Add(ttl)
	}
	return nil
}

// Ping implements KVStore.
func (m *MemoryKV) Ping(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.down {
		return ErrKVUnavailable
	}
	return nil
}

// itoa formats a non-negative int64 without importing strconv at call sites.
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
