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
