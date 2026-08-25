// Package tokenauth menyediakan penyimpanan refresh-token + denylist JTI
// (revocation) di Redis untuk kedua API (service-websocket, api-vehicle) —
// Phase B4 hardening GAP #12.
//
// Desain keamanan:
//   - Refresh token = random 32-byte (crypto/rand), dikirim ke klien sebagai
//     opaque string; yang disimpan di Redis adalah SHA-256(hash)-nya saja,
//     sehingga bocornya Redis tidak langsung menghasilkan token valid.
//   - Rotasi wajib: setiap pemakaian refresh token lama DIHAPUS dan diganti
//     baru (reuse detection sederhana: token lama tak berlaku lagi).
//   - Access token JWT membawa jti; logout menaruh jti pada denylist dengan
//     TTL = sisa umur token sehingga denylist otomatis bersih.
package tokenauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrInvalidToken dikembalikan bila refresh token tidak dikenal/kadaluarsa.
var ErrInvalidToken = errors.New("invalid refresh token")

// RedisCmd adalah subset perintah Redis yang dipakai package ini —
// *redis.Client memenuhinya; unit test memakai fake.
type RedisCmd interface {
	Set(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.StatusCmd
	Get(ctx context.Context, key string) *redis.StringCmd
	Del(ctx context.Context, keys ...string) *redis.IntCmd
	Exists(ctx context.Context, keys ...string) *redis.IntCmd
}

// Payload adalah identitas yang di-replay saat refresh (harus konsisten dengan
// klaim JWT agar token baru setara token lama).
type Payload struct {
	UserID      uint64  `json:"user_id"`
	CompanyCode string  `json:"company_code"`
	Email       string  `json:"email"`
	Role        string  `json:"role"`
	VehicleIDs  []int64 `json:"vehicle_ids,omitempty"`
}

// Manager operasi refresh/denylist pada satu prefix key Redis.
type Manager struct {
	rdb    RedisCmd
	prefix string // mis. "adatrack_gps:" → adatrack_gps:auth:refresh:<hash>
}

// New creates a Manager. prefix example: "adatrack_gps:".
func New(rdb RedisCmd, prefix string) *Manager {
	return &Manager{rdb: rdb, prefix: prefix}
}

func (m *Manager) refreshKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return m.prefix + "auth:refresh:" + fmt.Sprintf("%x", sum)
}

func (m *Manager) denyKey(jti string) string {
	return m.prefix + "auth:deny:" + jti
}

// newOpaqueToken generates a 256-bit URL-safe random token.
func newOpaqueToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// IssueRefresh stores a fresh refresh token for payload and returns it.
func (m *Manager) IssueRefresh(ctx context.Context, p Payload, ttl time.Duration) (string, error) {
	tok, err := newOpaqueToken()
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}
	if err := m.rdb.Set(ctx, m.refreshKey(tok), string(raw), ttl).Err(); err != nil {
		return "", fmt.Errorf("store refresh token: %w", err)
	}
	return tok, nil
}

// ResolveRefresh validates an opaque refresh token and returns its payload.
func (m *Manager) ResolveRefresh(ctx context.Context, token string) (*Payload, error) {
	val, err := m.rdb.Get(ctx, m.refreshKey(token)).Result()
	if errors.Is(err, redis.Nil) {
		return nil, ErrInvalidToken
	}
	if err != nil {
		return nil, fmt.Errorf("resolve refresh token: %w", err)
	}
	var p Payload
	if err := json.Unmarshal([]byte(val), &p); err != nil {
		return nil, fmt.Errorf("corrupt refresh payload: %w", err)
	}
	return &p, nil
}

// RotateRefresh atomically replaces oldToken with a new one for the same
// payload (rotation). Old token becomes unusable immediately.
func (m *Manager) RotateRefresh(ctx context.Context, oldToken string, ttl time.Duration) (string, error) {
	p, err := m.ResolveRefresh(ctx, oldToken)
	if err != nil {
		return "", err
	}
	newTok, err := m.IssueRefresh(ctx, *p, ttl)
	if err != nil {
		return "", err
	}
	if err := m.rdb.Del(ctx, m.refreshKey(oldToken)).Err(); err != nil {
		// Token baru sudah valid; kegagalan hapus lama dilaporkan agar tak silent.
		return "", fmt.Errorf("revoke old refresh token: %w", err)
	}
	return newTok, nil
}

// RevokeRefresh deletes a refresh token (logout / admin force-logout).
func (m *Manager) RevokeRefresh(ctx context.Context, token string) error {
	return m.rdb.Del(ctx, m.refreshKey(token)).Err()
}

// DenyJTI blacklists an access-token jti until it naturally expires.
func (m *Manager) DenyJTI(ctx context.Context, jti string, ttl time.Duration) error {
	if jti == "" {
		return errors.New("empty jti")
	}
	if ttl <= 0 {
		ttl = time.Minute // sudah hampir kadaluarsa — cukup sesaat
	}
	return m.rdb.Set(ctx, m.denyKey(jti), "1", ttl).Err()
}

// IsJTIDenied reports whether the jti has been revoked.
func (m *Manager) IsJTIDenied(ctx context.Context, jti string) (bool, error) {
	if jti == "" {
		return false, nil
	}
	n, err := m.rdb.Exists(ctx, m.denyKey(jti)).Result()
	_ = n
	return n > 0, err
}
