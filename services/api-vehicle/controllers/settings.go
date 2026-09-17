// Package controllers implements the api-vehicle REST service (PRD §8.2 fleet
// management, phase B3): CRUD + assignment APIs with JWT interop, RBAC
// row-level (§3.1) and soft delete + restore (§6.0.1).
package controllers

import (
	"errors"
	"strings"
	"time"

	"adatrack_gps/internal"
)

// MinJWTSecretLen mirrors service-websocket (fail-closed §9.1).
const MinJWTSecretLen = 32

// Settings is the resolved api-vehicle configuration (PRD §7.2).
type Settings struct {
	HTTPAddr string

	// JWT (PRD §9.1): HS256, the SAME secret + claims as service-websocket.
	JWTSecret         string
	JWTIssuer         string
	JWTClockSkew      time.Duration
	RevocationEnabled bool
	DenylistPrefix    string

	// API protection (PRD §8.4).
	APIRateLimit  int
	APIRateWindow time.Duration

	// Pagination + validation bounds (PRD §8.5).
	DefaultPageSize int
	MaxPageSize     int
	MaxBodyBytes    int64

	// CORS allowlist (PRD §9.3).
	AllowedOrigins   []string
	AllowEmptyOrigin bool
}

// OriginAllowed reports whether the Origin header may be echoed back.
func (s Settings) OriginAllowed(origin string) bool {
	for _, o := range s.AllowedOrigins {
		if strings.EqualFold(strings.TrimSpace(o), origin) {
			return true
		}
	}
	return false
}

// LoadSettings reads the environment with dev-safe defaults.
func LoadSettings() Settings {
	return Settings{
		HTTPAddr: internal.EnvOr("API_VEHICLE_HTTP_ADDR", ":8081"),

		JWTSecret:         internal.EnvOr("JWT_SECRET", ""),
		JWTIssuer:         internal.EnvOr("JWT_ISSUER", "adatrack"),
		JWTClockSkew:      time.Duration(internal.EnvIntDefault("JWT_CLOCK_SKEW_SEC", 30)) * time.Second,
		RevocationEnabled: internal.EnvBoolDefault("JWT_REVOCATION_ENABLED", true),
		DenylistPrefix:    internal.EnvOr("AUTH_DENYLIST_PREFIX", "adatrack_gps:auth:denylist:"),

		APIRateLimit:  internal.EnvIntDefault("API_RATE_LIMIT", 100),
		APIRateWindow: time.Duration(internal.EnvIntDefault("API_RATE_WINDOW_SEC", 60)) * time.Second,

		DefaultPageSize: internal.EnvIntDefault("API_DEFAULT_PAGE_SIZE", 100),
		MaxPageSize:     internal.EnvIntDefault("API_MAX_PAGE_SIZE", 1000),
		MaxBodyBytes:    int64(internal.EnvIntDefault("API_MAX_BODY_BYTES", 1<<20)),

		AllowedOrigins:   splitList(internal.EnvOr("WS_ALLOWED_ORIGINS", "http://localhost:3000,http://127.0.0.1:3000")),
		AllowEmptyOrigin: internal.EnvBoolDefault("WS_ALLOW_EMPTY_ORIGIN", true),
	}
}

// Validate rejects a configuration that cannot serve traffic safely: the JWT
// secret is fail-closed (no implicit signing key, PRD §9.1/§10.2).
func (s Settings) Validate() error {
	var errs []error
	if strings.TrimSpace(s.HTTPAddr) == "" {
		errs = append(errs, errors.New("API_VEHICLE_HTTP_ADDR must not be empty"))
	}
	if len(s.JWTSecret) < MinJWTSecretLen {
		errs = append(errs, errors.New("JWT_SECRET must be set and at least 32 characters (shared with service-websocket)"))
	}
	if s.JWTIssuer == "" {
		errs = append(errs, errors.New("JWT_ISSUER must not be empty"))
	}
	if s.APIRateLimit < 0 || s.MaxPageSize < 0 {
		errs = append(errs, errors.New("rate limit / page size must not be negative"))
	}
	return errors.Join(errs...)
}

// splitList parses a comma separated env list.
func splitList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
