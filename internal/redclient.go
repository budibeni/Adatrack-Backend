package internal

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisClient wraps the Redis client with the PRD §FR-2.4 pool sizing
// (Min 10 / Max 30 / timeouts 5 s) and Prometheus instrumentation.
type RedisClient struct {
	client *redis.Client
	cfg    *Config
}

// NewRedisClient connects to Redis and verifies connectivity.
func NewRedisClient(cfg *Config) (*RedisClient, error) {
	client := redis.NewClient(&redis.Options{
		Addr:            cfg.RedisAddr(),
		Password:        cfg.Redis.Password,
		DB:              cfg.Redis.DB,
		PoolSize:        cfg.Redis.PoolSize,
		MinIdleConns:    cfg.Redis.PoolMin,
		DialTimeout:     5 * time.Second,
		ReadTimeout:     3 * time.Second,
		WriteTimeout:    3 * time.Second,
		ConnMaxIdleTime: 5 * time.Minute,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("redis ping: %w", err)
	}
	return &RedisClient{client: client, cfg: cfg}, nil
}

// Cmdable exposes the underlying client for the optional IMEI lookup cache.
func (c *RedisClient) Cmdable() redis.Cmdable { return c.client }

// Client exposes the concrete client (tenant cache + advanced commands).
func (c *RedisClient) Client() *redis.Client { return c.client }

// Ping verifies connectivity (readiness probe).
func (c *RedisClient) Ping(ctx context.Context) error {
	if c == nil || c.client == nil {
		return fmt.Errorf("redis client not initialised")
	}
	return c.client.Ping(ctx).Err()
}

// KeyPrefix returns the configured namespace (REDIS_KEY_PREFIX, "adatrack_gps:").
func (c *RedisClient) KeyPrefix() string {
	if c == nil || c.cfg == nil || c.cfg.Redis.KeyPrefix == "" {
		return "adatrack_gps:"
	}
	return c.cfg.Redis.KeyPrefix
}

// LiveStateKey builds the tenant-scoped live-state key (FR-2.1):
//
//	adatrack_gps:{company_code}:vehicle:state:{IMEI}
func (c *RedisClient) LiveStateKey(companyCode, imei string) string {
	code := normalizeCompany(companyCode)
	return c.KeyPrefix() + code + ":vehicle:state:" + imei
}

// MSetBatch writes many keys in ONE round trip (FR-2.3: 100x fewer Redis ops).
// Errors are returned, never swallowed; callers log + count them.
func (c *RedisClient) MSetBatch(ctx context.Context, values map[string]string, ttl time.Duration) error {
	if len(values) == 0 {
		return nil
	}
	pipe := c.client.Pipeline()
	for k, v := range values {
		pipe.Set(ctx, k, v, ttl)
	}
	start := time.Now()
	_, err := pipe.Exec(ctx)
	c.record("MSET", len(values), time.Since(start), err)
	return err
}

// Get reads a raw value ("" + redis.Nil when missing).
func (c *RedisClient) Get(ctx context.Context, key string) (string, error) {
	start := time.Now()
	val, err := c.client.Get(ctx, key).Result()
	if err == redis.Nil {
		err = nil
	}
	c.record("GET", 1, time.Since(start), err)
	return val, err
}

// Set stores a value with a TTL.
func (c *RedisClient) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	start := time.Now()
	err := c.client.Set(ctx, key, value, ttl).Err()
	c.record("SET", 1, time.Since(start), err)
	return err
}

// Del removes keys.
func (c *RedisClient) Del(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	start := time.Now()
	err := c.client.Del(ctx, keys...).Err()
	c.record("DEL", len(keys), time.Since(start), err)
	return err
}

// ScanKeys returns keys matching a pattern using SCAN (never KEYS, which blocks
// the server on large datasets). matchCount caps the number of returned keys so
// callers stay bounded (PRD §FR-4.4 bounded buffers).
func (c *RedisClient) ScanKeys(ctx context.Context, pattern string, matchCount int64) ([]string, error) {
	if matchCount <= 0 {
		matchCount = 500
	}
	var (
		cursor uint64
		out    []string
	)
	start := time.Now()
	for {
		keys, next, err := c.client.Scan(ctx, cursor, pattern, 200).Result()
		if err != nil {
			c.record("SCAN", 1, time.Since(start), err)
			return nil, err
		}
		out = append(out, keys...)
		cursor = next
		if cursor == 0 || int64(len(out)) >= matchCount {
			break
		}
	}
	c.record("SCAN", len(out), time.Since(start), nil)
	if int64(len(out)) > matchCount {
		out = out[:matchCount]
	}
	return out, nil
}

// PoolStats exposes pool counters for /metrics and /healthz.
func (c *RedisClient) PoolStats() *redis.PoolStats {
	if c == nil || c.client == nil {
		return nil
	}
	return c.client.PoolStats()
}

// Close releases the client.
func (c *RedisClient) Close() error {
	if c == nil || c.client == nil {
		return nil
	}
	return c.client.Close()
}

// record updates the shared Redis metrics (command + status + latency).
func (c *RedisClient) record(command string, _ int, d time.Duration, err error) {
	if RedisOperations != nil {
		status := "success"
		if err != nil {
			status = "error"
		}
		RedisOperations.WithLabelValues(command, status).Inc()
	}
	if RedisOperationDuration != nil {
		RedisOperationDuration.WithLabelValues(command).Observe(d.Seconds())
	}
}

// normalizeCompany lowercases/trims a company code and defaults to "default",
// guaranteeing tenant-isolated Redis keys (FR-2.1).
func normalizeCompany(code string) string {
	out := ""
	for _, r := range code {
		switch {
		case r >= 'A' && r <= 'Z':
			out += string(r + ('a' - 'A'))
		case r == ' ' || r == '\t':
			continue
		default:
			out += string(r)
		}
	}
	if out == "" {
		return "default"
	}
	return out
}
