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
		// Replica is the optional read-replica endpoint (PRD §13 read/write
		// split). Empty Host disables the split entirely — every read keeps
		// going to the primary, i.e. exactly the pre-B4 behaviour.
		Replica ReplicaPostgres
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

	// Fleet holds the B7 fleet-management accumulators that worker-live maintains
	// from the telemetry stream: odometer + engine hours (FR-2.5) and the
	// trip/stop state machine (FR-2.6).
	Fleet struct {
		// FlushEvery is the accumulator → PostgreSQL flush cadence (FR-2.5: 30 s).
		FlushEvery time.Duration
		// FlushBatch flushes early once this many vehicles carry pending data
		// (FR-2.5: ≥100 vehicles).
		FlushBatch int
		// MaxJumpKM discards a single distance delta larger than this as a GPS
		// jump (FR-2.5: 5 km).
		MaxJumpKM float64
		// MaxGap is the largest device-time gap credited to the odometer
		// (FR-2.5 "interval terlalu lama").
		MaxGap time.Duration
		// EngineMaxGap is the largest gap credited to engine hours.
		EngineMaxGap time.Duration
		// StopGrace is how long a stationary vehicle must stay put before the
		// stop is confirmed (TRIP_STOP_GRACE_SECONDS, 30 s).
		StopGrace time.Duration
		// MinStop is the shortest confirmed stop that becomes a `td_vehicle_stops`
		// row and splits the trip (TRIP_MIN_STOP_SECONDS, 60 s).
		MinStop time.Duration
		// MaxStop auto-closes an open trip after this much standing/silence
		// (TRIP_MAX_STOP_SECONDS, 3600 s).
		MaxStop time.Duration
		// MovingSpeedKMH separates MOVING from STOPPED (0 = any speed above zero,
		// FR-2.2).
		MovingSpeedKMH float64
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
		NotifyRetryMax     int
		NotifyRetryBackoff []time.Duration
		// SMTP settings (§7.3) — empty host disables the email channel.
		SMTP struct {
			Host, Port, Username, Password, From string
			TLS                                  bool
		}
		// SMS settings (§7.3) — provider "none" skips the sms channel.
		SMS struct {
			Provider                    string // none|twilio|aws_sns
			AccountSID, AuthToken, From string
		}
	}

	// Fuel holds the B5a fuel-sensor settings (PRD Module 7 / §5.9.9). Per-vehicle
	// thresholds live in `tm_fuel_configs`; the values here are the global
	// fallback applied when no config row matches, plus the ACC-gate and the tank
	// calibration knobs of FR-7.6/FR-7.8.
	Fuel struct {
		// TankHeightCM calibrates the GT06 0x0D sensor height (cm) into a
		// percentage. 0 = uncalibrated: the raw height is kept and fuel_level
		// stays absent (FR-7.3 absent ≠ zero, FR-7.8 calibration out of scope).
		TankHeightCM float64
		// DropThresholdPercent / RefuelThresholdPercent are the global defaults
		// (percent of the observed level) evaluated inside WindowSeconds.
		DropThresholdPercent   int
		RefuelThresholdPercent int
		// WindowSeconds is the sliding window of the delta evaluation.
		WindowSeconds int
		// RequireACC enables the strict literal ACC gate (FUEL_DROP_REQUIRE_ACC).
		// Default false: the drop detector always runs (anti-siphon while parked).
		RequireACC bool
		// ACCStaleSeconds is the staleness window of the ACC gate (600 s).
		ACCStaleSeconds int
		// Severity is the default FUEL_DROP severity (REFUEL is always `low`).
		Severity string
	}

	// Media holds the B5b dashcam event-media settings (PRD Module 8, Scope A):
	// object-storage backend, upload limits, HMAC handshake and retention.
	Media struct {
		// Addr is the service-media HTTP listener (MEDIA_HTTP_ADDR).
		Addr string
		// MetricsAddr is the /healthz + /metrics listener (MEDIA_METRICS_ADDR).
		MetricsAddr string
		// Backend selects the object store: "mem" (dev/test) or "s3".
		Backend string
		// S3 settings (MEDIA_S3_*): endpoint/region/bucket + static keys.
		S3Endpoint  string
		S3Region    string
		S3Bucket    string
		S3AccessKey string
		S3SecretKey string
		S3UseSSL    bool
		// PresignTTL bounds the presigned GET URLs (MEDIA_PRESIGN_TTL_SEC).
		PresignTTL time.Duration
		// MaxFileMB rejects uploads above the limit (MEDIA_MAX_FILE_MB).
		MaxFileMB int
		// HMACSecret is the dev fallback shared secret; per-company secrets live
		// in master `tm_company_media_config` (MEDIA_HMAC_SECRET).
		HMACSecret string
		// RetentionDays is the retention sweep horizon (MEDIA_RETENTION_DAYS).
		RetentionDays int
		// RetentionSweep is the periodic retention-job cadence.
		RetentionSweep time.Duration
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
	if c.Fleet.FlushEvery <= 0 {
		errs = append(errs, errors.New("FLEET_FLUSH_SECONDS must be > 0"))
	}
	if c.Fleet.FlushBatch <= 0 {
		errs = append(errs, errors.New("FLEET_FLUSH_BATCH must be > 0"))
	}
	if c.Fleet.MaxJumpKM <= 0 {
		errs = append(errs, errors.New("ODOMETER_MAX_JUMP_KM must be > 0"))
	}
	if c.Fleet.MaxGap <= 0 || c.Fleet.EngineMaxGap <= 0 {
		errs = append(errs, errors.New("ODOMETER_MAX_GAP_SECONDS/ENGINE_HOURS_MAX_GAP_SECONDS must be > 0"))
	}
	if c.Fleet.StopGrace <= 0 || c.Fleet.MinStop <= 0 || c.Fleet.MaxStop <= 0 {
		errs = append(errs, errors.New("TRIP_STOP_GRACE_SECONDS/TRIP_MIN_STOP_SECONDS/TRIP_MAX_STOP_SECONDS must be > 0"))
	}
	if c.Fleet.MaxStop < c.Fleet.MinStop {
		errs = append(errs, errors.New("TRIP_MAX_STOP_SECONDS must be >= TRIP_MIN_STOP_SECONDS"))
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

// ReplicaPostgres is the optional read-replica endpoint (PRD §13). Any field left
// empty inherits the primary's value, so a standby that only differs by host/port
// needs just POSTGRES_REPLICA_HOST (+ PORT).
type ReplicaPostgres struct {
	Host, Port, User, Password, DB string
}

// Enabled reports whether a replica endpoint is configured.
func (r ReplicaPostgres) Enabled() bool {
	return strings.TrimSpace(r.Host) != ""
}

// PostgresReplicaDSN builds the replica connection URL for one schema. Unlike
// PostgresDSN it deliberately IGNORES DATABASE_URL: that variable points at the
// primary, and silently sending reads there would defeat the whole split.
func (c *Config) PostgresReplicaDSN(schema string) string {
	r := c.Postgres.Replica
	host := r.Host
	port := firstNonEmpty(r.Port, c.Postgres.Port)
	user := firstNonEmpty(r.User, c.Postgres.User)
	pass := r.Password
	if pass == "" {
		pass = c.Postgres.Password
	}
	db := firstNonEmpty(r.DB, c.Postgres.DB)
	return c.postgresDSNHostCreds(host, port, user, pass, db, schema)
}

// firstNonEmpty returns the first non-empty value (or "" when none).
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// postgresDSNHost builds an explicit host:port DSN for one schema.
func (c *Config) postgresDSNHost(host, port, schema string) string {
	return c.postgresDSNHostCreds(host, port, c.Postgres.User, c.Postgres.Password, c.Postgres.DB, schema)
}

// postgresDSNHostCreds builds a DSN with explicit credentials (shared by the
// primary and the optional replica).
func (c *Config) postgresDSNHostCreds(host, port, user, password, db, schema string) string {
	cred := user
	if password != "" {
		cred += ":" + password
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
	return fmt.Sprintf("postgres://%s@%s:%s/%s?%s", cred, host, port, db, q.Encode())
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
