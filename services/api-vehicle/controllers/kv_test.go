package controllers

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// newMiniredisKV starts an in-process Redis protocol server (miniredis) and
// returns the PRODUCTION adapter wired to it, so the exact commands api-vehicle
// issues (denylist reads, rate-limit INCR/EXPIRE, live-state MGET) are verified
// without external infrastructure (same pattern as service-websocket).
func newMiniredisKV(t *testing.T) (*RedisKV, *miniredis.Miniredis) {
	t.Helper()
	srv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() {
		_ = client.Close()
		srv.Close()
	})
	return NewRedisKV(client), srv
}

// newDownKV points the adapter at a closed port so every command fails — the
// 503 degradation path of the rate limiter and /healthz (PRD §10.2).
//
// MaxRetries is disabled: the suite calls six commands and go-redis would
// otherwise back off between every retry (~10 s total), slowing `make test`
// without adding any coverage — the error still surfaces on the first attempt.
func newDownKV(t *testing.T) *RedisKV {
	t.Helper()
	client := redis.NewClient(&redis.Options{
		Addr:         "127.0.0.1:1",
		DialTimeout:  50 * time.Millisecond,
		ReadTimeout:  50 * time.Millisecond,
		WriteTimeout: 50 * time.Millisecond,
		MaxRetries:   -1,
	})
	t.Cleanup(func() { _ = client.Close() })
	return NewRedisKV(client)
}

// TestRedisKVCommandSurface exercises every method the service depends on:
// revocation reads, the fixed-window rate limiter and the live-state batch read
// (FR-5.7, PRD §8.4, FR-2.1).
func TestRedisKVCommandSurface(t *testing.T) {
	kv, srv := newMiniredisKV(t)
	ctx := context.Background()

	if err := kv.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}

	// Denylist write + read (jti marker).
	if err := kv.Set(ctx, "adatrack_gps:auth:denylist:jti-1", "1", time.Minute); err != nil {
		t.Fatalf("set: %v", err)
	}
	val, err := kv.Get(ctx, "adatrack_gps:auth:denylist:jti-1")
	if err != nil || val != "1" {
		t.Fatalf("get = (%q,%v), want (1,nil)", val, err)
	}

	// A missing key is NOT an error: callers branch on the empty string.
	missing, err := kv.Get(ctx, "adatrack_gps:auth:denylist:absent")
	if err != nil || missing != "" {
		t.Fatalf("missing key = (%q,%v), want (\"\",nil)", missing, err)
	}

	// Rate limiter: INCR then EXPIRE on the first hit only.
	first, err := kv.Incr(ctx, "adatrack_gps:auth:api:user:7")
	if err != nil || first != 1 {
		t.Fatalf("incr = (%d,%v), want (1,nil)", first, err)
	}
	if err := kv.Expire(ctx, "adatrack_gps:auth:api:user:7", 30*time.Second); err != nil {
		t.Fatalf("expire: %v", err)
	}
	if ttl := srv.TTL("adatrack_gps:auth:api:user:7"); ttl <= 0 || ttl > 30*time.Second {
		t.Fatalf("ttl = %v, want (0,30s]", ttl)
	}
	if second, _ := kv.Incr(ctx, "adatrack_gps:auth:api:user:7"); second != 2 {
		t.Fatalf("second incr = %d, want 2", second)
	}

	// Live-state batch read (one round trip): present + absent keys.
	if err := kv.Set(ctx, kv.LiveStateKey("DEV001", "864201040512345"), `{"imei":"864201040512345"}`, time.Minute); err != nil {
		t.Fatalf("set live state: %v", err)
	}
	batch, err := kv.MGet(ctx, kv.LiveStateKey("DEV001", "864201040512345"), kv.LiveStateKey("DEV001", "000000000000000"))
	if err != nil {
		t.Fatalf("mget: %v", err)
	}
	if len(batch) != 2 || batch[0] == "" || batch[1] != "" {
		t.Fatalf("mget = %q, want [payload \"\"]", batch)
	}

	// An empty key list short-circuits without touching Redis.
	if got, err := kv.MGet(ctx); err != nil || got != nil {
		t.Fatalf("mget() = (%v,%v), want (nil,nil)", got, err)
	}
}

// TestRedisKVNamespace asserts the live-state key layout is IDENTICAL to the one
// worker-live writes — a mismatch silently disables the REST live overlay.
func TestRedisKVNamespace(t *testing.T) {
	kv, _ := newMiniredisKV(t)

	if got, want := kv.LiveStateKey("DEV001", "864201040512345"),
		"adatrack_gps:dev001:vehicle:state:864201040512345"; got != want {
		t.Errorf("live state key = %q, want %q", got, want)
	}

	// An explicit prefix (REDIS_KEY_PREFIX) always wins.
	custom := NewRedisKVWithPrefix(kv.client, "tenant:")
	if got, want := custom.LiveStateKey("DEV001", "123"), "tenant:dev001:vehicle:state:123"; got != want {
		t.Errorf("custom key = %q, want %q", got, want)
	}

	// An empty prefix falls back to the canonical namespace instead of producing
	// keys that no other service can read.
	fallback := NewRedisKVWithPrefix(kv.client, "")
	if got, want := fallback.LiveStateKey("DEV001", "123"), "adatrack_gps:dev001:vehicle:state:123"; got != want {
		t.Errorf("fallback key = %q, want %q", got, want)
	}
	if got, want := NewRedisKV(kv.client).LiveStateKey("DEV001", "123"),
		"adatrack_gps:dev001:vehicle:state:123"; got != want {
		t.Errorf("NewRedisKV key = %q, want %q", got, want)
	}
}

// TestRedisKVUnavailable propagates a connection failure on EVERY command so the
// callers can answer 503 instead of degrading silently (PRD §10.2).
func TestRedisKVUnavailable(t *testing.T) {
	kv := newDownKV(t)
	ctx := context.Background()

	if err := kv.Ping(ctx); err == nil {
		t.Error("ping against a closed port must fail")
	}
	if _, err := kv.Get(ctx, "k"); err == nil {
		t.Error("get must fail")
	}
	if _, err := kv.MGet(ctx, "k"); err == nil {
		t.Error("mget must fail")
	}
	if err := kv.Set(ctx, "k", "v", time.Minute); err == nil {
		t.Error("set must fail")
	}
	if _, err := kv.Incr(ctx, "k"); err == nil {
		t.Error("incr must fail")
	}
	if err := kv.Expire(ctx, "k", time.Minute); err == nil {
		t.Error("expire must fail")
	}
}
