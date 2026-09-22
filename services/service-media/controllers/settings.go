// Package controllers implements service-media: the dashcam event-media ingest,
// catalog and presigned-access API of PRD Module 8 (Scope A, phase B5b).
package controllers

import (
	"errors"
	"strings"
	"time"

	"adatrack_gps/internal"
)

// MinJWTSecretLen mirrors service-websocket/api-vehicle (fail-closed §9.1).
const MinJWTSecretLen = 32

// Settings is the resolved service-media configuration (PRD §7.2, MEDIA_* keys).
// Object-storage connection values live in internal.Config.Media; the fields here
// are the service's own behaviour (ingest limits, retention, auth interop).
type Settings struct {
	// HTTPAddr serves REST + /healthz + /metrics (PRD §14.2: 8095).
	HTTPAddr string
	// MetricsAddr is the dedicated /healthz + /metrics listener (8096); the main
	// listener serves both too, so PRD §14.2 (healthz on 8095) and the scrape
	// targets on 8096 keep working.
	MetricsAddr string

	// Bucket is the configured bucket (diagnostics + storage_objects{bucket}).
	Bucket string
	// MaxFileMB is the upload ceiling when the company config does not override it.
	MaxFileMB int
	// PresignTTL bounds the presigned GET lifetime (FR-8.4: short TTL).
	PresignTTL time.Duration

	// HMACSecret is the dev fallback shared secret (MEDIA_HMAC_SECRET); the
	// authoritative per-company secret is master `tm_company_media_config`.
	HMACSecret string
	// HMACMaxSkew rejects a replayed request whose X-Timestamp is older (§9.6).
	HMACMaxSkew time.Duration

	// RetentionDays is the default retention horizon (FR-8.7, 30 days).
	RetentionDays int
	// CleanupCron schedules the retention sweep (MEDIA_CLEANUP_CRON, "0 3 * * *").
	CleanupCron string
	// SweepOverrideSeconds > 0 replaces the cron schedule with a fixed interval
	// (dev/E2E: a dense sweep so the retention path is verifiable in seconds).
	SweepOverrideSeconds int
	// PendingTTLHours expires uploads that never completed (JSON flow abandoned).
	PendingTTLHours int

	// ConfigCacheTTL caches tm_company_media_config lookups.
	ConfigCacheTTL time.Duration

	// --- JWT interop (same secret/claims as service-websocket) --------------
	JWTSecret         string
	JWTIssuer         string
	JWTClockSkew      time.Duration
	RevocationEnabled bool
	DenylistPrefix    string

	// --- API protection (PRD §8.4/§8.5) ------------------------------------
	APIRateLimit  int
	APIRateWindow time.Duration
	// IngestRateLimit bounds the HMAC ingest tier per client IP (0 = off). The
	// ingest path buffers the multipart body to verify the signature, so an
	// unauthenticated flood must be cheap to reject (audit finding 2026-09-22).
	IngestRateLimit  int
	IngestRateWindow time.Duration
	DefaultPageSize  int
	MaxPageSize      int
	MaxBodyBytes     int64
	AllowedOrigins   []string
	AllowEmptyOrigin bool

	// AuditEnabled toggles the tm_audit_logs writes (§9.4).
	AuditEnabled bool
}

// LoadSettings resolves the environment (dev-safe defaults, cfg.Media for storage).
func LoadSettings(cfg *internal.Config) Settings {
	return Settings{
		HTTPAddr:    internal.EnvOr("MEDIA_HTTP_ADDR", ":8095"),
		MetricsAddr: internal.EnvOr("MEDIA_METRICS_ADDR", ":8096"),

		Bucket:     cfg.Media.S3Bucket,
		MaxFileMB:  cfg.Media.MaxFileMB,
		PresignTTL: cfg.Media.PresignTTL,

		HMACSecret:  cfg.Media.HMACSecret,
		HMACMaxSkew: time.Duration(internal.EnvIntDefault("MEDIA_HMAC_MAX_SKEW_SEC", 300)) * time.Second,

		RetentionDays:        cfg.Media.RetentionDays,
		CleanupCron:          internal.EnvOr("MEDIA_CLEANUP_CRON", "0 3 * * *"),
		SweepOverrideSeconds: internal.EnvIntDefault("MEDIA_RETENTION_SWEEP_SEC", 0),
		PendingTTLHours:      internal.EnvIntDefault("MEDIA_PENDING_TTL_HOURS", 24),

		ConfigCacheTTL: time.Duration(internal.EnvIntDefault("MEDIA_CONFIG_CACHE_SEC", 60)) * time.Second,

		JWTSecret:         internal.EnvOr("JWT_SECRET", ""),
		JWTIssuer:         internal.EnvOr("JWT_ISSUER", "adatrack"),
		JWTClockSkew:      time.Duration(internal.EnvIntDefault("JWT_CLOCK_SKEW_SEC", 30)) * time.Second,
		RevocationEnabled: internal.EnvBoolDefault("JWT_REVOCATION_ENABLED", true),
		DenylistPrefix:    internal.EnvOr("AUTH_DENYLIST_PREFIX", "adatrack_gps:auth:denylist:"),

		APIRateLimit:     internal.EnvIntDefault("API_RATE_LIMIT", 100),
		APIRateWindow:    time.Duration(internal.EnvIntDefault("API_RATE_WINDOW_SEC", 60)) * time.Second,
		IngestRateLimit:  internal.EnvIntDefault("MEDIA_INGEST_RATE_LIMIT", 600),
		IngestRateWindow: time.Duration(internal.EnvIntDefault("MEDIA_INGEST_RATE_WINDOW_SEC", 60)) * time.Second,
		DefaultPageSize:  internal.EnvIntDefault("API_DEFAULT_PAGE_SIZE", 100),
		MaxPageSize:      internal.EnvIntDefault("API_MAX_PAGE_SIZE", 1000),
		MaxBodyBytes:     int64(internal.EnvIntDefault("MEDIA_MAX_BODY_BYTES", 128<<20)),
		AllowedOrigins:   splitList(internal.EnvOr("WS_ALLOWED_ORIGINS", "http://localhost:3000,http://127.0.0.1:3000")),
		AllowEmptyOrigin: internal.EnvBoolDefault("WS_ALLOW_EMPTY_ORIGIN", true),

		AuditEnabled: internal.EnvBoolDefault("AUDIT_ENABLED", true),
	}
}

// Validate rejects a configuration that cannot serve traffic safely (§10.2).
func (s Settings) Validate() error {
	var errs []error
	if strings.TrimSpace(s.HTTPAddr) == "" {
		errs = append(errs, errors.New("MEDIA_HTTP_ADDR must not be empty"))
	}
	if len(s.JWTSecret) < MinJWTSecretLen {
		errs = append(errs, errors.New("JWT_SECRET must be set and at least 32 characters (shared with service-websocket)"))
	}
	if s.JWTIssuer == "" {
		errs = append(errs, errors.New("JWT_ISSUER must not be empty"))
	}
	if s.MaxFileMB <= 0 {
		errs = append(errs, errors.New("MEDIA_MAX_FILE_MB must be > 0"))
	}
	if s.PresignTTL <= 0 {
		errs = append(errs, errors.New("MEDIA_PRESIGN_TTL_SEC must be > 0"))
	}
	if s.RetentionDays <= 0 {
		errs = append(errs, errors.New("MEDIA_RETENTION_DAYS must be > 0"))
	}
	if s.SweepOverrideSeconds <= 0 {
		if _, err := parseCron(s.CleanupCron); err != nil {
			errs = append(errs, err)
		}
	}
	if s.HMACSecret != "" && len(s.HMACSecret) < 16 {
		errs = append(errs, errors.New("MEDIA_HMAC_SECRET must be at least 16 characters when set"))
	}
	return errors.Join(errs...)
}

// MaxFileBytes converts a (possibly per-company) ceiling into bytes.
func MaxFileBytes(maxFileMB int) int64 {
	if maxFileMB <= 0 {
		return 0
	}
	return int64(maxFileMB) << 20
}

// OriginAllowed reports whether a browser Origin may be echoed (PRD §9.3).
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

// splitList parses a comma-separated env list.
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
