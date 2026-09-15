package controllers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// TestRedisKVAdapter exercises the production KVStore implementation against a
// real (in-process) Redis protocol server, so the refresh/denylist/rate-limit
// commands are verified as they are actually issued (FR-5.7, PRD §8.4).
func TestRedisKVAdapter(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer srv.Close()

	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	defer func() { _ = client.Close() }()
	kv := NewRedisKV(client)
	ctx := context.Background()

	if err := kv.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}

	// Set + Get round trip with TTL.
	if err := kv.Set(ctx, "k1", "v1", time.Minute); err != nil {
		t.Fatalf("set: %v", err)
	}
	val, err := kv.Get(ctx, "k1")
	if err != nil || val != "v1" {
		t.Fatalf("get = (%q,%v), want v1", val, err)
	}

	// A missing key is not an error ("" instead) so callers can branch on it.
	missing, err := kv.Get(ctx, "nope")
	if err != nil || missing != "" {
		t.Fatalf("missing key = (%q,%v), want empty + nil", missing, err)
	}

	// Increment + Expire back the fixed-window rate limiter.
	first, err := kv.Incr(ctx, "counter")
	if err != nil || first != 1 {
		t.Fatalf("incr = (%d,%v), want 1", first, err)
	}
	second, _ := kv.Incr(ctx, "counter")
	if second != 2 {
		t.Fatalf("second incr = %d, want 2", second)
	}
	if err := kv.Expire(ctx, "counter", time.Minute); err != nil {
		t.Fatalf("expire: %v", err)
	}
	if ttl := srv.TTL("counter"); ttl <= 0 {
		t.Fatalf("TTL not applied: %v", ttl)
	}

	// Del removes the key (missing keys are ignored).
	if err := kv.Del(ctx, "k1", "absent"); err != nil {
		t.Fatalf("del: %v", err)
	}
	if val, _ := kv.Get(ctx, "k1"); val != "" {
		t.Fatalf("key survived Del: %q", val)
	}
}

// TestRedisKVUnavailableIsClassified asserts a connection failure is reported as
// ErrKVUnavailable so the API answers 503 instead of degrading silently.
func TestRedisKVUnavailableIsClassified(t *testing.T) {
	// A closed port is guaranteed to refuse the connection.
	client := redis.NewClient(&redis.Options{
		Addr:         "127.0.0.1:1",
		DialTimeout:  50 * time.Millisecond,
		ReadTimeout:  50 * time.Millisecond,
		WriteTimeout: 50 * time.Millisecond,
	})
	defer func() { _ = client.Close() }()
	kv := NewRedisKV(client)
	ctx := context.Background()

	if err := kv.Ping(ctx); !errors.Is(err, ErrKVUnavailable) {
		t.Fatalf("ping error = %v, want ErrKVUnavailable", err)
	}
	if _, err := kv.Get(ctx, "k"); !errors.Is(err, ErrKVUnavailable) {
		t.Fatalf("get error = %v, want ErrKVUnavailable", err)
	}
	if _, err := kv.Incr(ctx, "k"); !errors.Is(err, ErrKVUnavailable) {
		t.Fatalf("incr error = %v, want ErrKVUnavailable", err)
	}
}

// TestMemoryKVBehaviour documents the in-memory double used by the unit tests.
func TestMemoryKVBehaviour(t *testing.T) {
	kv := NewMemoryKV()
	ctx := context.Background()

	if err := kv.Set(ctx, "k", "1", 20*time.Millisecond); err != nil {
		t.Fatalf("set: %v", err)
	}
	if val, _ := kv.Get(ctx, "k"); val != "1" {
		t.Fatalf("get = %q, want 1", val)
	}
	time.Sleep(30 * time.Millisecond)
	if val, _ := kv.Get(ctx, "k"); val != "" {
		t.Fatalf("expired key still readable: %q", val)
	}

	kv.SetDown(true)
	if err := kv.Ping(ctx); !errors.Is(err, ErrKVUnavailable) {
		t.Fatalf("ping while down = %v, want ErrKVUnavailable", err)
	}
	kv.SetDown(false)
	if err := kv.Set(ctx, "n", "x", 0); err != nil {
		t.Fatalf("set without TTL: %v", err)
	}
	if n, err := kv.Incr(ctx, "n"); err != nil || n != 1 {
		t.Fatalf("incr on non-numeric value = (%d,%v), want 1", n, err)
	}
}
