package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
)

// ---------------------------------------------------------------------------
// HTTP Security Headers (GAP #12 — Hardening §4.2)
// ---------------------------------------------------------------------------

// securityHeaders is the set of HTTP security headers applied to every
// response served by HTTP-based services (service-websocket REST, api-vehicle).
// These mitigate clickjacking, MIME-sniffing, XSS, and enforce HTTPS in
// production.
var securityHeaders = map[string]string{
	// Force HTTPS for 1 year (including subdomains).
	"Strict-Transport-Security": "max-age=31536000; includeSubDomains",
	// Prevent clickjacking.
	"X-Frame-Options": "DENY",
	// Prevent MIME-type sniffing.
	"X-Content-Type-Options": "nosniff",
	// Control referrer info.
	"Referrer-Policy": "strict-origin-when-cross-origin",
	// CSP — restrict directives to self + known origins.
	"Content-Security-Policy": "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; connect-src 'self' wss:; frame-ancestors 'none'; base-uri 'self'",
	// Permissions-Policy — limit powerful APIs.
	"Permissions-Policy": "geolocation=(), microphone=(), camera=()",
	// XSS protection (legacy browsers).
	"X-XSS-Protection": "1; mode=block",
	// Disable caching for API endpoints (sensitive data).
	"Cache-Control": "no-store, no-cache, must-revalidate, max-age=0",
	"Pragma":        "no-cache",
}

// ApplySecurityHeaders sets all security headers on the given ResponseWriter.
// Call this at the start of an HTTP handler or via middleware before writing
// the response body.
func ApplySecurityHeaders(w http.ResponseWriter) {
	for k, v := range securityHeaders {
		w.Header().Set(k, v)
	}
}

// SecurityHeadersMiddleware returns an http.Handler middleware that applies
// security headers to every response.
func SecurityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ApplySecurityHeaders(w)
		next.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------------------
// Redis Client
// ---------------------------------------------------------------------------

// RedisClient wraps Redis connection pool with metrics.
// Provides connection pooling, retry logic, and Prometheus metrics.
type RedisClient struct {
	client     *redis.Client
	config     *Config
	opsCounter *prometheus.CounterVec
	opDuration *prometheus.HistogramVec
}

// NewRedisClient creates a new Redis client with connection pooling.
func NewRedisClient(config *Config, opsCounter *prometheus.CounterVec, opDuration *prometheus.HistogramVec) (*RedisClient, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:         config.Redis.Addr,
		Password:     config.Redis.Password,
		DB:           config.Redis.DB,
		PoolSize:     config.Redis.PoolSize,
		MinIdleConns: 2,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	slog.Info("Redis client initialized",
		"addr", config.Redis.Addr,
		"db", config.Redis.DB,
		"poolSize", config.Redis.PoolSize,
	)

	return &RedisClient{
		client:     rdb,
		config:     config,
		opsCounter: opsCounter,
		opDuration: opDuration,
	}, nil
}

// Client returns the underlying redis.Client for direct access.
func (c *RedisClient) Client() *redis.Client {
	return c.client
}

// Ping checks Redis connectivity (readiness probe).
func (c *RedisClient) Ping(ctx context.Context) error {
	return c.client.Ping(ctx).Err()
}

// Close closes the Redis connection pool.
func (c *RedisClient) Close() error {
	if c.client != nil {
		return c.client.Close()
	}
	return nil
}

// Get retrieves a value from Redis by key.
func (c *RedisClient) Get(ctx context.Context, key string) (string, error) {
	start := time.Now()
	val, err := c.client.Get(ctx, key).Result()
	c.recordMetrics("GET", err == nil)
	if c.opDuration != nil {
		c.opDuration.WithLabelValues("GET").Observe(time.Since(start).Seconds())
	}
	if err != nil {
		return "", err
	}
	return val, nil
}

// Set stores a value in Redis with optional expiration.
func (c *RedisClient) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	start := time.Now()
	err := c.client.Set(ctx, key, value, expiration).Err()
	c.recordMetrics("SET", err == nil)
	if c.opDuration != nil {
		c.opDuration.WithLabelValues("SET").Observe(time.Since(start).Seconds())
	}
	return err
}

// MSet sets multiple key-value pairs in Redis using pipeline.
func (c *RedisClient) MSet(ctx context.Context, values map[string]interface{}, expiration time.Duration) error {
	if len(values) == 0 {
		return nil
	}
	pipe := c.client.TxPipeline()
	for key, value := range values {
		pipe.Set(ctx, key, value, expiration)
	}
	start := time.Now()
	_, err := pipe.Exec(ctx)
	c.recordMetrics("MSET", err == nil)
	if c.opDuration != nil {
		c.opDuration.WithLabelValues("MSET").Observe(time.Since(start).Seconds())
	}
	return err
}

// SetWithRetry sets a value with exponential backoff retry logic.
func (c *RedisClient) SetWithRetry(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	maxRetries := 3
	baseDelay := 1 * time.Second
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		err := c.Set(ctx, key, value, expiration)
		if err == nil {
			return nil
		}
		lastErr = err
		if attempt < maxRetries {
			delay := baseDelay * time.Duration(1<<uint(attempt))
			slog.Warn("Redis set retry",
				"attempt", attempt+1,
				"delay", delay,
				"key", key,
				"error", err,
			)
			time.Sleep(delay)
		}
	}
	return lastErr
}

// GetState retrieves vehicle state from Redis using key pattern vehicle:state:<IMEI>.
func (c *RedisClient) GetState(ctx context.Context, imei string) (map[string]interface{}, error) {
	key := "vehicle:state:" + imei
	val, err := c.Get(ctx, key)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(val), &result); err != nil {
		return nil, fmt.Errorf("failed to parse vehicle state for IMEI %s: %w", imei, err)
	}
	return result, nil
}

// SetState stores vehicle state in Redis with TTL.
// Key format: vehicle:state:<IMEI>, TTL: 5 minutes
func (c *RedisClient) SetState(ctx context.Context, imei string, state map[string]interface{}) error {
	key := "vehicle:state:" + imei
	expiration := 5 * time.Minute
	return c.SetWithRetry(ctx, key, state, expiration)
}

// Del deletes a key from Redis.
func (c *RedisClient) Del(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	return c.client.Del(ctx, keys...).Err()
}

// Expire sets an expiration on a key.
func (c *RedisClient) Expire(ctx context.Context, key string, expiration time.Duration) (bool, error) {
	return c.client.Expire(ctx, key, expiration).Result()
}

// TTL returns the remaining TTL for a key.
func (c *RedisClient) TTL(ctx context.Context, key string) (time.Duration, error) {
	return c.client.TTL(ctx, key).Result()
}

// Exists checks if a key exists in Redis.
func (c *RedisClient) Exists(ctx context.Context, keys ...string) (int64, error) {
	return c.client.Exists(ctx, keys...).Result()
}

// HSet sets a field in a Redis hash.
func (c *RedisClient) HSet(ctx context.Context, key, field string, value interface{}) error {
	return c.client.HSet(ctx, key, field, value).Err()
}

// HGetAll retrieves all fields and values of a Redis hash.
func (c *RedisClient) HGetAll(ctx context.Context, key string) (map[string]string, error) {
	return c.client.HGetAll(ctx, key).Result()
}

// recordMetrics updates the Prometheus metrics counters.
func (c *RedisClient) recordMetrics(command string, success bool) {
	if c.opsCounter != nil {
		status := "success"
		if !success {
			status = "error"
		}
		c.opsCounter.WithLabelValues(command, status).Inc()
	}
}
