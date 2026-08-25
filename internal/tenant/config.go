// Package tenant implements the multi-tenant foundation (PRD §6, §7):
// one master database (auth + IMEI→company lookup) plus one database per
// company (adatrack_gps_{company_code}, lowercase).
//
// TenantManager pre-warms connection pools to every active company database,
// resolves IMEI → company_code through master.vehicle_imei_map (with an
// optional Redis cache) and reports PRD §8.1 tenant metrics.
package tenant

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"ajb_gps/internal/dialect"
)

// Config holds the connection parameters for the multi-tenant architecture.
// Values follow the env variable names defined in PRD §7 (MASTER_DB_*,
// COMPANY_DB_PREFIX, MYSQL_POOL_*).
//
// Provider (DATABASE_PROVIDER=mysql|postgres) selects the SQL engine; the
// matching driver is opened via DriverName()/DSN() below. MySQL is the
// historical default and is always retained as a selectable provider.
type Config struct {
	// Provider selects the persistent DB engine: "mysql" (default) or
	// "postgres". The whole tenant layer (DSN, driver, CREATE DATABASE,
	// upsert syntax) branches on this.
	Provider string

	// Master connection (auth + IMEI lookup).
	MasterHost     string
	MasterPort     string
	MasterUser     string
	MasterPassword string
	MasterName     string

	// Company DB naming convention (PRD §6.1).
	CompanyDBPrefix string

	// MigrationsDir is the filesystem path to the company migration SQL files
	// (*.sql), applied automatically during auto-provision (PRD §6.1 / B2/B3).
	// For postgres the driver selects the *_pg variant of this dir automatically
	// (see MigrationsDirFor).
	MigrationsDir string

	// Postgres-specific connection settings (only consulted when Provider ==
	// "postgres"); mysql shares the Master* fields above for connection.
	PostgresHost     string
	PostgresPort     string
	PostgresUser     string
	PostgresPassword string
	PostgresSSLMode  string
	// PostgresDB is the shared physical PostgreSQL database (one DB, many
	// tenant schemas). The per-tenant "database" (CompanyDBName) becomes a
	// schema selected via search_path in the DSN.
	PostgresDB     string
	PostgresSchema string

	// Pool sizing (PRD §7 worker-persistence/api-vehicle defaults 20/50).
	PoolMin         int
	PoolMax         int
	ConnMaxLifetime time.Duration

	// IMEI → company_code Redis cache (optional).
	CachePrefix string        // e.g. "adatrack_tenant:imei:"
	CacheTTL    time.Duration // e.g. 15m

	// Network timeouts for the underlying SQL DSN.
	ConnectTimeout time.Duration
	ReadTimeout    time.Duration
	WriteTimeout   time.Duration

	// --- Read/write split (B4 HA): READ → REPLICA, WRITE → PRIMARY ---
	//
	// ReplicaEnabled mengaktifkan pool read-replica TERPISAH per company DB
	// (replika di-start via deployments/docker-compose.ha.yml: profile
	// "mysql" → mysql-replica, "postgres" → postgres-replica). Query SELECT
	// diarahkan ke replica; INSERT/UPDATE/DELETE SELALU ke primary. Ketika
	// replika mati/unreachable, router otomatis fallback ke primary (breaker,
	// lihat replica.go) sehingga tidak ada silent failure.
	ReplicaEnabled bool
	ReplicaHost    string // kosong → ikut host primary provider
	ReplicaPort    string // MYSQL_REPLICA_PORT / POSTGRES_REPLICA_PORT (compose-ha)
	ReplicaPoolMin int
	ReplicaPoolMax int
	ProbeInterval  time.Duration // periode health-probe replica (breaker gauge)
}

const (
	defaultCompanyDBPrefix = "adatrack_gps_"
	defaultCachePrefix     = "adatrack_tenant:imei:"
)

// NewConfigFromEnv builds a Config from environment variables with safe
// dev-local defaults (PRD §7).
//
// DATABASE_PROVIDER=postgres|mysql selects the engine. postgres adalah default
// proyek (keputusan 2026-08-25) via POSTGRES_*/DATABASE_URL + pgx v5 stdlib;
// mysql tetap selectable penuh via MASTER_DB_* + MYSQL_POOL_* env & go-sql-driver.
func NewConfigFromEnv() Config {
	provider := strings.ToLower(strings.TrimSpace(getEnv("DATABASE_PROVIDER", "postgres")))
	if provider == "" {
		provider = "postgres"
	}

	c := Config{
		Provider:        provider,
		CompanyDBPrefix: getEnv("COMPANY_DB_PREFIX", defaultCompanyDBPrefix),
		MigrationsDir:   getEnv("COMPANY_MIGRATIONS_DIR", ""),
		CachePrefix:     getEnv("TENANT_CACHE_KEY_PREFIX", defaultCachePrefix),
		CacheTTL:        time.Duration(getEnvInt("TENANT_CACHE_TTL_SEC", 900)) * time.Second,
		ConnectTimeout:  time.Duration(getEnvInt("MYSQL_CONNECT_TIMEOUT_SEC", 5)) * time.Second,
		ReadTimeout:     time.Duration(getEnvInt("MYSQL_READ_TIMEOUT_SEC", 5)) * time.Second,
		WriteTimeout:    time.Duration(getEnvInt("MYSQL_WRITE_TIMEOUT_SEC", 5)) * time.Second,
		PoolMin:         getEnvInt("MYSQL_POOL_MIN", 10),
		PoolMax:         getEnvInt("MYSQL_POOL_MAX", 50),
		ConnMaxLifetime: time.Duration(getEnvInt("MYSQL_CONN_MAX_LIFETIME_MIN", 30)) * time.Minute,
	}

	// Read/write split envs (B4 HA). Default DISABLED agar dev lokal tanpa
	// replika tetap normal; set DB_REPLICA_ENABLED=true saat replika dari
	// deployments/docker-compose.ha.yml ikut up. Nama port mengikuti env
	// compose-ha agar satu sumber kebenaran.
	c.ReplicaEnabled = getEnvBool("DB_REPLICA_ENABLED", false)
	c.ReplicaHost = getEnv("DB_REPLICA_HOST", "")
	if c.Provider == "postgres" {
		c.ReplicaPort = getEnv("POSTGRES_REPLICA_PORT", "5433")
		if c.ReplicaHost == "" {
			c.ReplicaHost = c.PostgresHost
		}
	} else {
		c.ReplicaPort = getEnv("MYSQL_REPLICA_PORT", "3407")
		if c.ReplicaHost == "" {
			c.ReplicaHost = c.MasterHost
		}
	}
	c.ReplicaPoolMin = getEnvInt("DB_REPLICA_POOL_MIN", 5)
	c.ReplicaPoolMax = getEnvInt("DB_REPLICA_POOL_MAX", c.PoolMax)
	c.ProbeInterval = time.Duration(getEnvInt("DB_REPLICA_PROBE_SECONDS", 10)) * time.Second

	if c.Provider == "postgres" {
		c.PostgresHost = getEnv("POSTGRES_HOST", "127.0.0.1")
		c.PostgresPort = getEnv("POSTGRES_PORT", "5432")
		c.PostgresUser = getEnv("POSTGRES_USER", "adatrack_gps_pg_user")
		c.PostgresPassword = getEnv("POSTGRES_PASSWORD", "")
		c.PostgresSSLMode = getEnv("POSTGRES_SSLMODE", "disable")
		c.PostgresDB = getEnv("POSTGRES_DB", "adatrack_gps_db")
		// MasterName is reused as the master schema name for postgres.
		c.MasterName = getEnv("POSTGRES_MASTER_SCHEMA", "adatrack_gps_master")
	} else {
		// mysql (default) — MASTER_DB_*
		c.MasterHost = getEnv("MASTER_DB_HOST", "127.0.0.1")
		c.MasterPort = getEnv("MASTER_DB_PORT", "3306")
		c.MasterUser = getEnv("MASTER_DB_USER", "root")
		c.MasterPassword = getEnv("MASTER_DB_PASSWORD", "")
		c.MasterName = getEnv("MASTER_DB_NAME", "adatrack_gps_master")
	}
	return c
}

// DriverName returns the database/sql driver for Provider. For postgres this
// is the placeholder-transpiling pgx wrapper (see internal/dialect/pgxdriver.go).
func (c Config) DriverName() string {
	return c.Dialect().DriverName()
}

// Dialect returns the SQL dialect for the configured provider.
func (c Config) Dialect() dialect.Dialect {
	return dialect.FromProvider(c.Provider)
}

// MigrationsDirFor returns the migration SQL directory for the provider.
// For postgres the *_pg suffix dir is used when present so postgres-flavoured
// DDL is selected; otherwise the base dir is reused.
func (c Config) MigrationsDirFor() string {
	if c.Provider != "postgres" || c.MigrationsDir == "" {
		return c.MigrationsDir
	}
	pg := c.MigrationsDir + "_pg"
	if _, err := os.Stat(pg); err == nil {
		return pg
	}
	return c.MigrationsDir
}

// CompanyDBName converts a company_code into its physical database name using
// the lowercase convention (PRD §6.1 / Key Decision 11).
// Example: prefix "adatrack_gps_", code "ABLE01" → "adatrack_gps_able01".
func (c Config) CompanyDBName(code string) string {
	return CompanyDBName(c.CompanyDBPrefix, code)
}

// CompanyDBPrefix returns prefix + LOWERCASE(trimmed code).
func CompanyDBName(prefix, code string) string {
	return prefix + strings.ToLower(strings.TrimSpace(code))
}

// MasterDSN builds the driver DSN for the master database.
// Provider-aware: mysql uses the go-sql-driver format; postgres uses a pgx URL.
func (c Config) MasterDSN() string {
	if c.Provider == "postgres" {
		return c.postgresDSN(c.MasterName)
	}
	return mysqlDSN(c.MasterUser, c.MasterPassword, c.MasterHost, c.MasterPort, c.MasterName, c)
}

// DBDSN builds the driver DSN for a concrete database name.
// Provider-aware (see MasterDSN).
func (c Config) DBDSN(dbName string) string {
	if c.Provider == "postgres" {
		return c.postgresDSN(dbName)
	}
	return mysqlDSN(c.MasterUser, c.MasterPassword, c.MasterHost, c.MasterPort, dbName, c)
}

// ReplicaEndpoint returns the resolved host:port of the READ REPLICA
// (DB_REPLICA_HOST, kosong → host primary provider).
func (c Config) ReplicaEndpoint() (string, string) {
	host := strings.TrimSpace(c.ReplicaHost)
	if host == "" {
		if c.Provider == "postgres" {
			host = c.PostgresHost
		} else {
			host = c.MasterHost
		}
	}
	return host, c.ReplicaPort
}

// ReplicaDSN builds the driver DSN for the READ REPLICA copy of dbName.
// Provider-aware; postgres memakai search_path schema yang sama karena
// streaming replication meng-clone seluruh cluster. DATABASE_URL TIDAK
// berlaku untuk replika (lihat postgresDSNH).
func (c Config) ReplicaDSN(dbName string) string {
	host, port := c.ReplicaEndpoint()
	if c.Provider == "postgres" {
		return c.postgresDSNH(host, port, dbName)
	}
	return mysqlDSN(c.MasterUser, c.MasterPassword, host, port, dbName, c)
}

// Validate returns an error for missing/invalid settings.
// An empty Provider normalizes to "mysql" (the historical default) so that
// struct literals constructed without an explicit provider still validate.
func (c Config) Validate() error {
	provider := c.Provider
	if provider == "" {
		provider = "mysql"
	}
	if provider != "mysql" && provider != "postgres" {
		return fmt.Errorf("tenant: DATABASE_PROVIDER must be mysql or postgres (got %q)", c.Provider)
	}
	if c.CompanyDBPrefix == "" || !strings.HasSuffix(c.CompanyDBPrefix, "_") {
		return fmt.Errorf("tenant: COMPANY_DB_PREFIX must end with '_' (e.g. adatrack_gps_)")
	}
	if c.PoolMin < 0 || c.PoolMax <= 0 || c.PoolMin > c.PoolMax {
		return fmt.Errorf("tenant: invalid pool sizing (min=%d max=%d)", c.PoolMin, c.PoolMax)
	}

	if provider == "mysql" {
		if c.MasterHost == "" {
			return fmt.Errorf("tenant: MASTER_DB_HOST is required")
		}
		if c.MasterPort == "" {
			return fmt.Errorf("tenant: MASTER_DB_PORT is required")
		}
		if c.MasterName == "" {
			return fmt.Errorf("tenant: MASTER_DB_NAME is required")
		}
	} else { // postgres
		if c.PostgresHost == "" {
			return fmt.Errorf("tenant: POSTGRES_HOST is required")
		}
		if c.PostgresPort == "" {
			return fmt.Errorf("tenant: POSTGRES_PORT is required")
		}
		if c.PostgresUser == "" {
			return fmt.Errorf("tenant: POSTGRES_USER is required")
		}
	}

	// Read/write split validation (B4 HA).
	if c.ReplicaEnabled {
		if strings.TrimSpace(c.ReplicaPort) == "" {
			return fmt.Errorf("tenant: DB_REPLICA_ENABLED=true requires MYSQL_REPLICA_PORT/POSTGRES_REPLICA_PORT")
		}
		if c.ReplicaPoolMax <= 0 || c.ReplicaPoolMin < 0 || c.ReplicaPoolMin > c.ReplicaPoolMax {
			return fmt.Errorf("tenant: invalid replica pool sizing (min=%d max=%d)", c.ReplicaPoolMin, c.ReplicaPoolMax)
		}
	}
	return nil
}

// mysqlDSN builds the go-sql-driver/mysql DSN.
func mysqlDSN(user, password, host, port, db string, cfg Config) string {
	return fmt.Sprintf(
		"%s:%s@tcp(%s:%s)/%s?parseTime=true&timeout=%s&readTimeout=%s&writeTimeout=%s&charset=utf8mb4",
		user, password, host, port, db,
		cfg.ConnectTimeout, cfg.ReadTimeout, cfg.WriteTimeout,
	)
}

// postgresDSN builds a pgx stdlib URL DSN selecting `schema` as the
// search_path (schema = the per-tenant "db name": adatrack_gps_master, or
// adatrack_gps_{company}). The physical PostgreSQL database comes from
// PostgresDB (default adatrack_gps_db).
//
// DATABASE_URL (when set) supplies the connection credentials for the PRIMARY
// — BUT the per-tenant search_path is ALWAYS forced onto it. Earlier versions
// used DATABASE_URL as-is (no search_path) which silently made every tenant
// pool resolve unqualified tables against the default schema ("public"),
// causing "relation ... does not exist" at runtime (fixed B4 2026-08-25;
// verified live: batch INSERT telemetry_logs schema adatrack_gps_dev001).
func (c Config) postgresDSN(schema string) string {
	if url := os.Getenv("DATABASE_URL"); url != "" {
		return ensureSearchPath(url, schema, c.PostgresSSLMode)
	}
	return c.postgresDSNH(c.PostgresHost, c.PostgresPort, schema)
}

// ensureSearchPath parses a PostgreSQL URL and forces search_path=<schema>.
// Si URL tidak valid (jarang), query param ditambahkan via string append yang
// aman (tidak pernah membuang kredensial). sslmode ditambahkan hanya bila belum
// tercantum eksplisit di URL.
func ensureSearchPath(rawURL, schema, sslmode string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		sep := "?"
		if strings.Contains(rawURL, "?") {
			sep = "&"
		}
		out := rawURL + sep + "search_path=" + url.QueryEscape(schema)
		if sslmode != "" && !strings.Contains(rawURL, "sslmode=") {
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

// postgresDSNH builds a pgx URL against an EXPLICIT host:port (primary or
// replica). Dipakai oleh postgresDSN (primary) dan ReplicaDSN (read replica);
// replika tidak boleh memakai DATABASE_URL agar tidak diam-diam mengarah
// ke primary.
func (c Config) postgresDSNH(host, port, schema string) string {
	cred := c.PostgresUser
	if pw := c.PostgresPassword; pw != "" {
		cred = cred + ":" + pw
	}
	db := c.PostgresDB
	if db == "" {
		db = "adatrack_gps_db"
	}
	return fmt.Sprintf("postgres://%s@%s:%s/%s?sslmode=%s&search_path=%s",
		cred, host, port, db, c.PostgresSSLMode, schema)
}

func getEnv(key, defaultValue string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return defaultValue
}

// getEnvBool parses boolean envs ("1", "true", "yes", "on" → true,
// case-insensitive); missing/invalid → defaultValue.
func getEnvBool(key string, defaultValue bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return defaultValue
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "t", "yes", "y", "on":
		return true
	case "0", "false", "f", "no", "n", "off":
		return false
	default:
		return defaultValue
	}
}

func getEnvInt(key string, defaultValue int) int {
	if v, ok := os.LookupEnv(key); ok {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return defaultValue
}
