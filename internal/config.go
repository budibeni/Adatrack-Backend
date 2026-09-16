// Package internal holds the shared backend foundation (PRD §7 configuration,
// §10 observability, §6 multi-tenant routing) used by every service.
//
// Engineering conventions (PRD §7, .agent/01-global-rules.md):
//   - every value comes from an environment variable with a safe dev default,
//   - logging is structured JSON (slog) with level debug|info|warn|error,
//   - every service exposes /healthz (readiness) and /metrics (Prometheus),
//   - PostgreSQL is the ONLY persistent engine (pgx via database/sql).
package internal

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the resolved service configuration (PRD §7.2).
type Config struct {
	Server struct {
		// MetricsAddr is the /healthz + /metrics listener.
		MetricsAddr string
	}

	TCP struct {
		// Port serves GT06/Concox (dev override 9003, canonical 5001).
		Port string
		// TeltonikaPort serves Teltonika Codec 8/8E (own reference, 9011/5027).
		TeltonikaPort string
		// MaxConnections bounds concurrent device connections (FR-1.1).
		MaxConnections int
		// IdleTimeout drops silent device connections (FR-1.3).
		IdleTimeout time.Duration
		// DateBCD toggles GT06 date decoding (plain-hex default).
		DateBCD bool
	}

	NATS struct {
		URL string
		// SubjectPrefix namespaces the telemetry.* family (NATS_SUBJECT_PREFIX).
		SubjectPrefix string
		// JetStream retention (§7.6): MaxAge/MaxBytes with DiscardOld.
		JetStreamMaxAgeHours int
		JetStreamMaxBytes    int
		// MaxPendingPercent implements FR-1.5: >50% warn, >90% drop.
		MaxPendingPercent int
	}

	Postgres struct {
		Host, Port, DB, User, Password string
		SSLMode                        string
		PoolMin, PoolMax               int
		ConnMaxLifetime                time.Duration
		StatementTimeout               time.Duration
		ConnectTimeout                 time.Duration
	}

	Redis struct {
		Host, Port, Password string
		DB                   int
		// KeyPrefix namespaces live state (§FR-2.1): adatrack_gps:
		KeyPrefix string
		PoolMin   int
		PoolSize  int
		// TTL for live-state keys (offline detection, FR-2.1).
		TTL time.Duration
	}

	Live struct {
		// BatchInterval is the live-state flush cadence (FR-2.3, 100 ms).
		BatchInterval time.Duration
		// MaxBatch bounds the pending live-state buffer (FR-4.4).
		MaxBatch int
		// OfflineAfterMinutes is the OFFLINE threshold (FR-2.2).
		OfflineAfterMinutes int
		// IdleAfter is the ONLINE→IDLE threshold (FR-2.2, 90 s).
		IdleAfter time.Duration
	}

	Persistence struct {
		// BatchSize / BatchTimeout follow FR-3.1 (500 records or 5 s).
		BatchSize    int
		BatchTimeout time.Duration
		// RetryMax + Backoff implement FR-3.4 step 5/6 (1s,5s,10s).
		RetryMax int
		Backoff  []time.Duration
		// MaxPending bounds the in-memory batch buffer (FR-4.4).
		MaxPending int
	}

	Migrate struct {
		OnBoot        bool
		LockTimeout   time.Duration
		LedgerTable   string
		MasterDir     string
		CompanyDir    string
		MasterSchema  string
		CompanyPrefix string
	}

	// Alert holds the worker-alert engine settings (B3, PRD §5.9/§7.2/§7.3).
	Alert struct {
		// MetricsAddr is the /healthz + /metrics listener (ALERT_METRICS_ADDR).
		MetricsAddr string
		// DedupWindow suppresses repeated alerts of the same dedup identity
		// while an open alert exists + repeat window (ALERT_DEDUP_WINDOW_SEC).
		DedupWindow time.Duration
		// OfflineAfterMinutes is the OFFLINE staleness threshold (FR-2.2).
		OfflineAfterMinutes int
		// OfflineSweepInterval is the OFFLINE sweeper cadence.
		OfflineSweepInterval time.Duration
		// BatteryLowPercent is the BATTERY_LOW threshold (default 20).
		BatteryLowPercent int
		// RouteDeviationThresholdM is the ROUTE_DEVIATION distance threshold (200 m).
		RouteDeviationThresholdM float64
		// RouteDeviationRefresh is the route-assignment cache refresh cadence (30 s).
		RouteDeviationRefresh time.Duration
		// GeoFenceRefresh is the geofence/config cache refresh cadence (30 s).
		GeoFenceRefresh time.Duration
		// SOSEscalationMinutes: an open SOS older than this escalates.
		SOSEscalationMinutes int
		// SOSEscalationMax caps the escalation counter per alert.
		SOSEscalationMax int
		// SOSEscalationInterval is the escalation re-check cadence (30 s).
		SOSEscalationInterval time.Duration
		// SOSCooldownSeconds dedups repeated SOS triggers from one device.
		SOSCooldownSeconds int
		// NotifyRateLimitPerMin caps notifications per company per minute (0 = off).
		NotifyRateLimitPerMin int
		// NotifyRetryMax + NotifyRetryBackoff for external delivery.
		NotifyRetryMax    int
		NotifyRetryBackoff []time.Duration
		// SMTP settings (§7.3) — empty host disables the email channel.
		SMTP struct {
			Host, Port, Username, Password, From string
			TLS                                  bool
		}
		// SMS settings (§7.3) — provider "none" skips the sms channel.
		SMS struct {
			Provider string // none|twilio|aws_sns
			AccountSID, AuthToken, From string
		}
	}
}

// Validate rejects configurations that cannot work, so a service fails at boot
// instead of silently degrading at runtime (PRD §10.2 readiness).
func (c *Config) Validate() error {
	var errs []error
	if c.Server.MetricsAddr == "" {
		errs = append(errs, errors.New("METRICS_ADDR must not be empty"))
	}
	if c.NATS.URL == "" {
		errs = append(errs, errors.New("NATS_URL must not be empty"))
	}
	if c.Postgres.Host == "" || c.Postgres.DB == "" || c.Postgres.User == "" {
		errs = append(errs, errors.New("POSTGRES_HOST/POSTGRES_DB/POSTGRES_USER must be set"))
	}
	if c.Postgres.PoolMax < c.Postgres.PoolMin {
		errs = append(errs, errors.New("POSTGRES_POOL_MAX must be >= POSTGRES_POOL_MIN"))
	}
	if c.Redis.Host == "" {
		errs = append(errs, errors.New("REDIS_HOST must not be empty"))
	}
	if c.Persistence.BatchSize <= 0 {
		errs = append(errs, errors.New("BATCH_SIZE must be > 0"))
	}
	if c.Live.MaxBatch <= 0 {
		errs = append(errs, errors.New("LIVE_MAX_BATCH must be > 0"))
	}
	return errors.Join(errs...)
}

// PostgresDSN builds the primary connection URL. DATABASE_URL (when set) wins,
// but the per-schema search_path is ALWAYS forced on top of it — relying on the
// URL alone would silently resolve unqualified tables against the default schema.
func (c *Config) PostgresDSN(schema string) string {
	if raw := os.Getenv("DATABASE_URL"); raw != "" {
		return forceSearchPath(raw, schema, c.Postgres.SSLMode)
	}
	return c.postgresDSNHost(c.Postgres.Host, c.Postgres.Port, schema)
}

// postgresDSNHost builds an explicit host:port DSN for one schema.
func (c *Config) postgresDSNHost(host, port, schema string) string {
	cred := c.Postgres.User
	if c.Postgres.Password != "" {
		cred += ":" + c.Postgres.Password
	}
	q := url.Values{}
	q.Set("sslmode", c.Postgres.SSLMode)
	if schema != "" {
		q.Set("search_path", schema)
	}
	if c.Postgres.StatementTimeout > 0 {
		q.Set("statement_timeout", strconv.FormatInt(c.Postgres.StatementTimeout.Milliseconds(), 10))
	}
	if c.Postgres.ConnectTimeout > 0 {
		q.Set("connect_timeout", strconv.Itoa(int(c.Postgres.ConnectTimeout.Seconds())))
	}
	return fmt.Sprintf("postgres://%s@%s:%s/%s?%s", cred, host, port, c.Postgres.DB, q.Encode())
}

// forceSearchPath rewrites a DATABASE_URL so its search_path is `schema`.
func forceSearchPath(raw, schema, sslmode string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		sep := "?"
		if strings.Contains(raw, "?") {
			sep = "&"
		}
		out := raw + sep + "search_path=" + url.QueryEscape(schema)
		if sslmode != "" && !strings.Contains(raw, "sslmode=") {
			out += "&sslmode=" + url.QueryEscape(sslmode)
		}
		return out
	}
	q := u.Query()
	q.Set("search_path", schema)
	if sslmode != "" && !q.Has("sslmode") {
		q.Set("sslmode", sslmode)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// RedisAddr returns host:port for the Redis client.
func (c *Config) RedisAddr() string { return c.Redis.Host + ":" + c.Redis.Port }

// Subject builds a subject inside the telemetry.* namespace (NATS_SUBJECT_PREFIX):
//
//	Subject("raw", imei)  -> telemetry.raw.<IMEI>
//	Subject("raw", ">")   -> telemetry.raw.>
func (c *Config) Subject(parts ...string) string {
	joined := strings.Join(parts, ".")
	if c.NATS.SubjectPrefix == "" || joined == "" {
		return joined
	}
	return c.NATS.SubjectPrefix + "." + joined
}

// SubjectPlain joins parts without the prefix (alert.*, notify.*, media.* keep
// the documented layout — GAP2 resolution).
func (c *Config) SubjectPlain(parts ...string) string { return strings.Join(parts, ".") }
