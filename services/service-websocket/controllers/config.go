// Package controllers implements service-websocket: the authenticated REST API
// (PRD §8.2) and the real-time WebSocket fan-out (PRD §8.3, FR-5.1..FR-5.7) with
// row-level RBAC (PRD §3.1, §9.2).
package controllers

import (
	"errors"
	"os"
	"strings"
	"time"

	"adatrack_gps/internal"
)

// Settings holds the service-websocket specific configuration (PRD §7.2, §9).
// Every value comes from an environment variable with a dev-safe default; the
// JWT secret has NO default (fail-fast: a service must never start with an
// implicit signing key).
type Settings struct {
	// HTTPAddr serves REST + WebSocket + /healthz + /metrics (PRD §14.2: 8082).
	HTTPAddr string

	// --- Authentication (PRD §9.1) -----------------------------------------
	JWTSecret         string
	JWTIssuer         string
	AccessExpiry      time.Duration
	RefreshExpiry     time.Duration
	RevocationEnabled bool
	ClockSkew         time.Duration
	RefreshPrefix     string // Redis key prefix for refresh (hash) records
	DenylistPrefix    string // Redis key prefix for revoked access tokens (jti)
	BcryptCost        int

	// --- Login protection (PRD §8.4, §9.1) ---------------------------------
	LoginRateLimit        int
	LoginRateWindow       time.Duration
	LoginLockoutThreshold int
	LoginLockoutDuration  time.Duration

	// --- API protection (PRD §8.4) -----------------------------------------
	APIRateLimit  int
	APIRateWindow time.Duration

	// --- Pagination / validation (PRD §8.5) --------------------------------
	DefaultPageSize     int
	MaxPageSize         int
	HistoryMaxRangeDays int
	MaxBodyBytes        int64

	// --- WebSocket (FR-5.3, FR-5.4) ----------------------------------------
	WSMaxConnections int
	WSSendBufferSize int
	WSMaxQueueSize   int
	WSPingInterval   time.Duration
	WSWriteTimeout   time.Duration
	WSPongTimeout    time.Duration
	WSReadLimit      int64
	WSSubscribeMax   int
	AllowedOrigins   []string
	AllowEmptyOrigin bool

	// --- Provisioning (FR-5.5) --------------------------------------------
	DefaultAdminPassword string
	AdminEmailDomain     string // admin@{code}.local

	// --- Audit (PRD §9.4) --------------------------------------------------
	AuditEnabled    bool
	AuditQueueSize  int
	AuditBatchSize  int
	AuditFlushEvery time.Duration
}

// LoadSettings resolves the service settings from the environment (PRD §7.2).
func LoadSettings() Settings {
	return Settings{
		HTTPAddr: internal.EnvOr("HTTP_ADDR", ":8082"),

		JWTSecret:         os.Getenv("JWT_SECRET"),
		JWTIssuer:         internal.EnvOr("JWT_ISSUER", "adatrack"),
		AccessExpiry:      time.Duration(internal.EnvIntDefault("JWT_EXPIRY_HOURS", 24)) * time.Hour,
		RefreshExpiry:     time.Duration(internal.EnvIntDefault("JWT_REFRESH_EXPIRY_HOURS", 168)) * time.Hour,
		RevocationEnabled: internal.EnvBoolDefault("JWT_REVOCATION_ENABLED", true),
		ClockSkew:         time.Duration(internal.EnvIntDefault("JWT_CLOCK_SKEW_SEC", 30)) * time.Second,
		RefreshPrefix:     internal.EnvOr("AUTH_REFRESH_PREFIX", "adatrack_gps:auth:refresh:"),
		DenylistPrefix:    internal.EnvOr("AUTH_DENYLIST_PREFIX", "adatrack_gps:auth:denylist:"),
		BcryptCost:        internal.EnvIntDefault("BCRYPT_COST", 12),

		LoginRateLimit:        internal.EnvIntDefault("LOGIN_RATE_LIMIT", 5),
		LoginRateWindow:       time.Duration(internal.EnvIntDefault("LOGIN_RATE_WINDOW_SEC", 900)) * time.Second,
		LoginLockoutThreshold: internal.EnvIntDefault("LOGIN_LOCKOUT_THRESHOLD", 5),
		LoginLockoutDuration:  time.Duration(internal.EnvIntDefault("LOGIN_LOCKOUT_MINUTES", 15)) * time.Minute,

		APIRateLimit:  internal.EnvIntDefault("API_RATE_LIMIT", 100),
		APIRateWindow: time.Duration(internal.EnvIntDefault("API_RATE_WINDOW_SEC", 60)) * time.Second,

		DefaultPageSize:     internal.EnvIntDefault("API_DEFAULT_PAGE_SIZE", 100),
		MaxPageSize:         internal.EnvIntDefault("API_MAX_PAGE_SIZE", 1000),
		HistoryMaxRangeDays: internal.EnvIntDefault("HISTORY_MAX_RANGE_DAYS", 90),
		MaxBodyBytes:        int64(internal.EnvIntDefault("API_MAX_BODY_BYTES", 1<<20)),

		WSMaxConnections: internal.EnvIntDefault("WS_MAX_CONNECTIONS", 5000),
		WSSendBufferSize: internal.EnvIntDefault("WS_SEND_BUFFER_BYTES", 256*1024),
		WSMaxQueueSize:   internal.EnvIntDefault("WS_MAX_QUEUE", 1000),
		WSPingInterval:   time.Duration(internal.EnvIntDefault("WS_PING_INTERVAL_SEC", 30)) * time.Second,
		WSWriteTimeout:   time.Duration(internal.EnvIntDefault("WS_WRITE_TIMEOUT_SEC", 10)) * time.Second,
		WSPongTimeout:    time.Duration(internal.EnvIntDefault("WS_PONG_TIMEOUT_SEC", 60)) * time.Second,
		WSReadLimit:      int64(internal.EnvIntDefault("WS_MAX_MESSAGE_BYTES", 4096)),
		WSSubscribeMax:   internal.EnvIntDefault("WS_MAX_SUBSCRIPTIONS", 5000),
		AllowedOrigins:   splitList(internal.EnvOr("WS_ALLOWED_ORIGINS", "http://localhost:3000,http://127.0.0.1:3000")),
		AllowEmptyOrigin: internal.EnvBoolDefault("WS_ALLOW_EMPTY_ORIGIN", true),

		DefaultAdminPassword: internal.EnvOr("PASSWORD_DEFAULT_TENANT_ADMIN", "Admin@123"),
		AdminEmailDomain:     internal.EnvOr("TENANT_ADMIN_EMAIL_DOMAIN", "local"),

		AuditEnabled:    internal.EnvBoolDefault("AUDIT_ENABLED", true),
		AuditQueueSize:  internal.EnvIntDefault("AUDIT_QUEUE_SIZE", 1000),
		AuditBatchSize:  internal.EnvIntDefault("AUDIT_BATCH_SIZE", 100),
		AuditFlushEvery: time.Duration(internal.EnvIntDefault("AUDIT_FLUSH_MS", 1000)) * time.Millisecond,
	}
}

// Validate rejects a configuration that cannot serve traffic safely (PRD §10.2
// readiness gate): the service fails at boot instead of degrading silently.
func (s Settings) Validate() error {
	var errs []error
	if strings.TrimSpace(s.HTTPAddr) == "" {
		errs = append(errs, errors.New("HTTP_ADDR must not be empty"))
	}
	if len(s.JWTSecret) < MinJWTSecretLen {
		errs = append(errs, errors.New("JWT_SECRET must be set and at least 32 characters (no implicit signing key)"))
	}
	if s.BcryptCost < 10 || s.BcryptCost > 15 {
		errs = append(errs, errors.New("BCRYPT_COST must be between 10 and 15 (PRD §9.1: 12)"))
	}
	if s.AccessExpiry <= 0 {
		errs = append(errs, errors.New("JWT_EXPIRY_HOURS must be > 0"))
	}
	if s.RefreshExpiry <= 0 {
		errs = append(errs, errors.New("JWT_REFRESH_EXPIRY_HOURS must be > 0"))
	}
	if s.MaxPageSize <= 0 || s.DefaultPageSize <= 0 {
		errs = append(errs, errors.New("API_DEFAULT_PAGE_SIZE/API_MAX_PAGE_SIZE must be > 0"))
	}
	if s.DefaultPageSize > s.MaxPageSize {
		errs = append(errs, errors.New("API_DEFAULT_PAGE_SIZE must be <= API_MAX_PAGE_SIZE"))
	}
	if s.WSMaxQueueSize <= 0 {
		errs = append(errs, errors.New("WS_MAX_QUEUE must be > 0"))
	}
	if s.WSMaxConnections <= 0 {
		errs = append(errs, errors.New("WS_MAX_CONNECTIONS must be > 0"))
	}
	return errors.Join(errs...)
}

// MinJWTSecretLen is the minimum HS256 secret length accepted at boot.
const MinJWTSecretLen = 32

// splitList parses a comma separated env value into a trimmed slice.
func splitList(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// OriginAllowed reports whether a browser Origin header may open a WebSocket
// (FR-5.4: browser origins are validated; non-browser clients send no Origin).
func (s Settings) OriginAllowed(origin string) bool {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return s.AllowEmptyOrigin
	}
	for _, allowed := range s.AllowedOrigins {
		if strings.EqualFold(allowed, origin) {
			return true
		}
	}
	return false
}
