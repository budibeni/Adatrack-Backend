package tokenauth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// fakeRedis adalah implementasi in-memory RedisCmd utk unit test.
type fakeRedis struct {
	mu    sync.Mutex
	store map[string]fakeValue
}

type fakeValue struct {
	val      string
	expireAt time.Time
	hasTTL   bool
}

func newFakeRedis() *fakeRedis {
	return &fakeRedis{store: map[string]fakeValue{}}
}

func (f *fakeRedis) get(key string) (fakeValue, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.store[key]
	if !ok {
		return fakeValue{}, false
	}
	if v.hasTTL && time.Now().After(v.expireAt) {
		delete(f.store, key)
		return fakeValue{}, false
	}
	return v, true
}

func (f *fakeRedis) Set(_ context.Context, key string, value interface{}, expiration time.Duration) *redis.StatusCmd {
	cmd := redis.NewStatusCmd(context.Background())
	f.mu.Lock()
	defer f.mu.Unlock()
	v := fakeValue{val: toString(value)}
	if expiration > 0 {
		v.hasTTL = true
		v.expireAt = time.Now().Add(expiration)
	}
	f.store[key] = v
	cmd.SetVal("OK")
	return cmd
}

func (f *fakeRedis) Get(_ context.Context, key string) *redis.StringCmd {
	cmd := redis.NewStringCmd(context.Background())
	if v, ok := f.get(key); ok {
		cmd.SetVal(v.val)
	} else {
		cmd.SetErr(redis.Nil)
	}
	return cmd
}

func (f *fakeRedis) Del(_ context.Context, keys ...string) *redis.IntCmd {
	cmd := redis.NewIntCmd(context.Background())
	f.mu.Lock()
	defer f.mu.Unlock()
	n := int64(0)
	for _, k := range keys {
		if _, ok := f.store[k]; ok {
			delete(f.store, k)
			n++
		}
	}
	cmd.SetVal(n)
	return cmd
}

func (f *fakeRedis) Exists(_ context.Context, keys ...string) *redis.IntCmd {
	cmd := redis.NewIntCmd(context.Background())
	n := int64(0)
	for _, k := range keys {
		if _, ok := f.get(k); ok {
			n++
		}
	}
	cmd.SetVal(n)
	return cmd
}

func toString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func TestIssueResolveRoundtrip(t *testing.T) {
	m := New(newFakeRedis(), "adatrack_gps:")
	ctx := context.Background()
	in := Payload{UserID: 7, CompanyCode: "DEF001", Email: "a@b.c", Role: "Admin", VehicleIDs: []int64{1, 2}}

	tok, err := m.IssueRefresh(ctx, in, time.Hour)
	if err != nil {
		t.Fatalf("IssueRefresh: %v", err)
	}
	if tok == "" || len(tok) < 32 {
		t.Fatalf("token terlalu pendek/kosong: %q", tok)
	}
	out, err := m.ResolveRefresh(ctx, tok)
	if err != nil {
		t.Fatalf("ResolveRefresh: %v", err)
	}
	if out.UserID != in.UserID || out.CompanyCode != in.CompanyCode ||
		out.Email != in.Email || out.Role != in.Role || len(out.VehicleIDs) != 2 {
		t.Fatalf("payload tidak sama: %+v vs %+v", out, in)
	}
	// Key di Redis harus menyimpan HASH, bukan token mentah.
	for k := range m.rdb.(*fakeRedis).store {
		if strings.Contains(k, tok) {
			t.Fatalf("key Redis memuat token mentah (harus hash): %s", k)
		}
	}
}

func TestResolveUnknownToken(t *testing.T) {
	m := New(newFakeRedis(), "adatrack_gps:")
	if _, err := m.ResolveRefresh(context.Background(), "no-such-token"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("harus ErrInvalidToken, dapat: %v", err)
	}
}

func TestRotateInvalidatesOld(t *testing.T) {
	m := New(newFakeRedis(), "adatrack_gps:")
	ctx := context.Background()
	in := Payload{UserID: 9, CompanyCode: "DEF001", Email: "r@b.c", Role: "Operator"}

	old, err := m.IssueRefresh(ctx, in, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	newTok, err := m.RotateRefresh(ctx, old, time.Hour)
	if err != nil {
		t.Fatalf("RotateRefresh: %v", err)
	}
	if newTok == old {
		t.Fatal("rotasi harus menghasilkan token berbeda")
	}
	if _, err := m.ResolveRefresh(ctx, old); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("token lama masih valid setelah rotasi: %v", err)
	}
	out, err := m.ResolveRefresh(ctx, newTok)
	if err != nil || out.UserID != 9 {
		t.Fatalf("token baru tidak valid: %v %+v", err, out)
	}
}

func TestDenyJTI(t *testing.T) {
	m := New(newFakeRedis(), "adatrack_gps:")
	ctx := context.Background()

	if denied, _ := m.IsJTIDenied(ctx, "jti-1"); denied {
		t.Fatal("jti-1 belum di-denylist")
	}
	if err := m.DenyJTI(ctx, "jti-1", time.Hour); err != nil {
		t.Fatalf("DenyJTI: %v", err)
	}
	if denied, _ := m.IsJTIDenied(ctx, "jti-1"); !denied {
		t.Fatal("jti-1 harus ter-denylist")
	}
	if denied, _ := m.IsJTIDenied(ctx, "jti-2"); denied {
		t.Fatal("jti-2 tidak boleh ter-denylist")
	}
}

func TestExpiryHonored(t *testing.T) {
	m := New(newFakeRedis(), "adatrack_gps:")
	ctx := context.Background()
	tok, err := m.IssueRefresh(ctx, Payload{UserID: 1}, 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if _, err := m.ResolveRefresh(ctx, tok); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("token expired harus invalid, dapat: %v", err)
	}
}

func TestRevokeRefresh(t *testing.T) {
	m := New(newFakeRedis(), "adatrack_gps:")
	ctx := context.Background()
	tok, err := m.IssueRefresh(ctx, Payload{UserID: 3}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.RevokeRefresh(ctx, tok); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ResolveRefresh(ctx, tok); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("setelah revoke harus ErrInvalidToken, dapat: %v", err)
	}
}
