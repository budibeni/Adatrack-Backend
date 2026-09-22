package internal

import (
	"os"
	"time"
)

// LoadConfig reads the environment (PRD §7.2) with dev-safe defaults.
func LoadConfig() *Config {
	c := &Config{}
	loadCoreConfig(c)
	loadPipelineConfig(c)
	loadAlertConfig(c)
	loadFuelConfig(c)
	loadMediaConfig(c)
	return c
}

// loadCoreConfig fills the shared infrastructure settings (HTTP/metrics, NATS,
// PostgreSQL, Redis, migrations).
func loadCoreConfig(c *Config) {
	c.Server.MetricsAddr = EnvOr("METRICS_ADDR", ":8090")

	c.NATS.URL = EnvOr("NATS_URL", "nats://127.0.0.1:4222")
	// An explicitly empty NATS_SUBJECT_PREFIX disables namespacing (documented
	// behaviour), so this variable distinguishes "unset" from "empty" — unlike
	// the other settings where an empty value means "use the default".
	if v, ok := os.LookupEnv("NATS_SUBJECT_PREFIX"); ok {
		c.NATS.SubjectPrefix = v
	} else {
		c.NATS.SubjectPrefix = "telemetry"
	}
	c.NATS.JetStreamMaxAgeHours = envInt("JETSTREAM_MAX_AGE_HOURS", 48)
	c.NATS.JetStreamMaxBytes = envInt("JETSTREAM_MAX_BYTES", 4*1024*1024*1024)
	c.NATS.MaxPendingPercent = envInt("NATS_BACKPRESSURE_WARN_PERCENT", 50)

	c.Postgres.Host = EnvOr("POSTGRES_HOST", "127.0.0.1")
	c.Postgres.Port = EnvOr("POSTGRES_PORT", "5432")
	c.Postgres.DB = EnvOr("POSTGRES_DB", "adatrack_gps_db")
	c.Postgres.User = EnvOr("POSTGRES_USER", "adatrack")
	c.Postgres.Password = EnvOr("POSTGRES_PASSWORD", "adatrack_dev_pw")
	c.Postgres.SSLMode = EnvOr("POSTGRES_SSLMODE", "disable")
	c.Postgres.PoolMin = envInt("POSTGRES_POOL_MIN", 20)
	c.Postgres.PoolMax = envInt("POSTGRES_POOL_MAX", 50)
	c.Postgres.ConnMaxLifetime = time.Duration(envInt("POSTGRES_CONN_MAX_LIFETIME_MIN", 5)) * time.Minute
	c.Postgres.StatementTimeout = time.Duration(envInt("POSTGRES_STATEMENT_TIMEOUT_SEC", 30)) * time.Second
	c.Postgres.ConnectTimeout = time.Duration(envInt("POSTGRES_CONNECT_TIMEOUT_SEC", 10)) * time.Second
	// Optional read replica (PRD §13). Empty host = read/write split disabled.
	c.Postgres.Replica.Host = EnvOr("POSTGRES_REPLICA_HOST", "")
	c.Postgres.Replica.Port = EnvOr("POSTGRES_REPLICA_PORT", "")
	c.Postgres.Replica.User = EnvOr("POSTGRES_REPLICA_USER", "")
	c.Postgres.Replica.Password = EnvOr("POSTGRES_REPLICA_PASSWORD", "")
	c.Postgres.Replica.DB = EnvOr("POSTGRES_REPLICA_DB", "")

	c.Redis.Host = EnvOr("REDIS_HOST", "127.0.0.1")
	c.Redis.Port = EnvOr("REDIS_PORT", "6379")
	c.Redis.Password = EnvOr("REDIS_PASSWORD", "")
	c.Redis.DB = envInt("REDIS_DB", 0)
	c.Redis.KeyPrefix = EnvOr("REDIS_KEY_PREFIX", "adatrack_gps:")
	c.Redis.PoolMin = envInt("REDIS_POOL_MIN", 10)
	c.Redis.PoolSize = envInt("REDIS_POOL_MAX", 30)
	c.Redis.TTL = time.Duration(envInt("REDIS_TTL_SEC", 300)) * time.Second

	c.Migrate.OnBoot = envBool("MIGRATE_ON_BOOT", false)
	c.Migrate.LockTimeout = time.Duration(envInt("MIGRATE_LOCK_TIMEOUT_SEC", 60)) * time.Second
	c.Migrate.LedgerTable = EnvOr("MIGRATION_LEDGER_TABLE", "tm_schema_migrations")
	// Migration artifact locations are relative to the backend root (they resolve
	// even when the service is started from its own subdirectory).
	c.Migrate.MasterDir = ResolvePath(EnvOr("MASTER_MIGRATIONS_DIR", "./database/migrations/master_pg"))
	c.Migrate.CompanyDir = ResolvePath(EnvOr("COMPANY_MIGRATIONS_DIR", "./database/migrations/company_pg"))
	c.Migrate.MasterSchema = EnvOr("MASTER_DB_NAME", "adatrack_gps_master")
	c.Migrate.CompanyPrefix = EnvOr("COMPANY_DB_PREFIX", "adatrack_gps_")
}

// loadPipelineConfig fills the ingestion/worker settings (B1).
func loadPipelineConfig(c *Config) {
	c.TCP.Port = EnvOr("TCP_PORT", "9003")
	c.TCP.TeltonikaPort = EnvOr("TELTONIKA_TCP_PORT", "9011")
	c.TCP.MaxConnections = envInt("TCP_MAX_CONNECTIONS", 5000)
	c.TCP.IdleTimeout = time.Duration(envInt("TCP_IDLE_TIMEOUT_SECONDS", 90)) * time.Second
	c.TCP.DateBCD = envBool("GT06_DATE_BCD", false)

	c.Live.BatchInterval = time.Duration(envInt("LIVE_BATCH_INTERVAL_MS", 100)) * time.Millisecond
	c.Live.MaxBatch = envInt("LIVE_MAX_BATCH", 5000)
	c.Live.OfflineAfterMinutes = envInt("OFFLINE_AFTER_MINUTES", 3)
	c.Live.IdleAfter = time.Duration(envInt("IDLE_AFTER_SECONDS", 90)) * time.Second

	c.Persistence.BatchSize = envInt("BATCH_SIZE", 500)
	c.Persistence.BatchTimeout = time.Duration(envInt("BATCH_TIMEOUT_SEC", 5)) * time.Second
	c.Persistence.RetryMax = envInt("RETRY_MAX", 3)
	c.Persistence.Backoff = envDurationList("RETRY_BACKOFF_MS", []time.Duration{
		time.Second, 5 * time.Second, 10 * time.Second,
	})
	c.Persistence.MaxPending = envInt("PERSISTENCE_MAX_PENDING", 5000)
}

// loadFuelConfig fills the B5a fuel-sensor settings (PRD Module 7 / §5.9.9).
// The thresholds here are the GLOBAL fallback: a `tm_fuel_configs` row for the
// vehicle (or the tenant-wide row) always wins inside worker-alert (FR-7.6).
func loadFuelConfig(c *Config) {
	// FR-7.2: the Teltonika IO → fuel mapping is resolved by the ingestion service
	// from TELTONIKA_IO_FUEL_LEVEL / _USED / _TEMP (default 86 for the level
	// channel), so an unset/zero value disables that specific fuel field.
	//
	// GT06 0x0D reports the sensor height in cm; a tank height converts it into
	// the fuel_level percentage of FR-7.3. Without calibration the raw height is
	// kept as the volume proxy and fuel_level stays absent (absent ≠ zero).
	c.Fuel.TankHeightCM = envFloat("FUEL_TANK_HEIGHT_CM", 0)

	c.Fuel.DropThresholdPercent = envInt("FUEL_DROP_THRESHOLD_PERCENT", 15)
	c.Fuel.RefuelThresholdPercent = envInt("FUEL_REFUEL_THRESHOLD_PERCENT", 10)
	c.Fuel.WindowSeconds = envInt("FUEL_WINDOW_SECONDS", 300)
	c.Fuel.Severity = EnvOr("FUEL_DROP_SEVERITY", "critical")

	// ACC-gate decision (2026-08-26): false = detection stays active (anti-siphon
	// while parked); `=true` adds the strict literal ACC gate for FUEL_DROP.
	c.Fuel.RequireACC = envBool("FUEL_DROP_REQUIRE_ACC", false)
	c.Fuel.ACCStaleSeconds = envInt("FUEL_ACC_STALE_SECONDS", 600)
}

// loadMediaConfig fills the B5b media settings (PRD Module 8, FR-8.1..FR-8.8).
func loadMediaConfig(c *Config) {
	c.Media.Addr = EnvOr("MEDIA_HTTP_ADDR", ":8095")
	c.Media.MetricsAddr = EnvOr("MEDIA_METRICS_ADDR", ":8096")
	c.Media.Backend = EnvOr("MEDIA_BACKEND", "mem")
	c.Media.S3Endpoint = EnvOr("MEDIA_S3_ENDPOINT", "")
	c.Media.S3Region = EnvOr("MEDIA_S3_REGION", "us-east-1")
	c.Media.S3Bucket = EnvOr("MEDIA_S3_BUCKET", "adatrack-media")
	c.Media.S3AccessKey = EnvOr("MEDIA_S3_ACCESS_KEY", "")
	c.Media.S3SecretKey = EnvOr("MEDIA_S3_SECRET_KEY", "")
	c.Media.S3UseSSL = envBool("MEDIA_S3_USE_SSL", true)
	c.Media.PresignTTL = time.Duration(envInt("MEDIA_PRESIGN_TTL_SEC", 300)) * time.Second
	c.Media.MaxFileMB = envInt("MEDIA_MAX_FILE_MB", 100)
	c.Media.HMACSecret = EnvOr("MEDIA_HMAC_SECRET", "")
	c.Media.RetentionDays = envInt("MEDIA_RETENTION_DAYS", 30)
	c.Media.RetentionSweep = time.Duration(envInt("MEDIA_RETENTION_SWEEP_SEC", 300)) * time.Second
}

// loadAlertConfig fills the worker-alert engine settings (B3, PRD §5.9/§7.2).
func loadAlertConfig(c *Config) {
	c.Alert.MetricsAddr = EnvOr("ALERT_METRICS_ADDR", ":8094")
	c.Alert.DedupWindow = time.Duration(envInt("ALERT_DEDUP_WINDOW_SEC", 300)) * time.Second
	c.Alert.OfflineAfterMinutes = envInt("OFFLINE_AFTER_MINUTES", 3)
	c.Alert.OfflineSweepInterval = time.Duration(envInt("ALERT_OFFLINE_SWEEP_SEC", 30)) * time.Second
	c.Alert.BatteryLowPercent = envInt("BATTERY_LOW_PERCENT", 20)
	c.Alert.RouteDeviationThresholdM = envFloat("ROUTE_DEVIATION_THRESHOLD_M", 200)
	c.Alert.RouteDeviationRefresh = time.Duration(envInt("ROUTE_DEVIATION_REFRESH_SEC", 30)) * time.Second
	c.Alert.GeoFenceRefresh = time.Duration(envInt("GEOFENCE_REFRESH_SEC", 30)) * time.Second
	c.Alert.SOSEscalationMinutes = envInt("SOS_ESCALATION_MINUTES", 5)
	c.Alert.SOSEscalationMax = envInt("SOS_ESCALATION_MAX", 3)
	c.Alert.SOSEscalationInterval = time.Duration(envInt("SOS_ESCALATION_INTERVAL_SEC", 30)) * time.Second
	c.Alert.SOSCooldownSeconds = envInt("SOS_COOLDOWN_SECONDS", 60)
	c.Alert.NotifyRateLimitPerMin = envInt("NOTIFY_RATE_LIMIT_PER_MIN", 600)
	c.Alert.NotifyRetryMax = envInt("NOTIFICATION_RETRY_MAX", 3)
	c.Alert.NotifyRetryBackoff = envDurationList("NOTIFICATION_RETRY_BACKOFF_MS", []time.Duration{
		2 * time.Second, 6 * time.Second, 12 * time.Second,
	})

	c.Alert.SMTP.Host = EnvOr("SMTP_HOST", "")
	c.Alert.SMTP.Port = EnvOr("SMTP_PORT", "587")
	c.Alert.SMTP.Username = EnvOr("SMTP_USERNAME", "")
	c.Alert.SMTP.Password = EnvOr("SMTP_PASSWORD", "")
	c.Alert.SMTP.From = EnvOr("SMTP_FROM", "notifications@adatrackgps.io")
	c.Alert.SMTP.TLS = envBool("SMTP_TLS", true)

	c.Alert.SMS.Provider = EnvOr("SMS_PROVIDER", "none")
	c.Alert.SMS.AccountSID = EnvOr("SMS_TWILIO_ACCOUNT_SID", "")
	c.Alert.SMS.AuthToken = EnvOr("SMS_AUTH_TOKEN", "")
	c.Alert.SMS.From = EnvOr("SMS_FROM", "")
}
