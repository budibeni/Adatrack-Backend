package internal

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"ajb_gps/internal/dialect"
)

// Config reads environment variables with safe defaults.
// Follows PRD §7 configuration conventions.
type Config struct {
	// Server settings
	Server struct {
		Addr string
	}

	// TCP Ingestion settings
	TCP struct {
		Port string
		// MaxConnections is the maximum number of concurrent TCP connections
		MaxConnections int
		// TeltonikaPort is a dedicated listener for Teltonika/FM devices
		// (Codec 8/8E/7/6). Empty/\"0\" disables it (each protocol gets its own
		// port — GT06 on TCP_PORT, Teltonika here, TK103 on TK103Port).
		TeltonikaPort string
		// TK103Port is a dedicated listener for the TK-103 family (provisional).
		TK103Port string
	}

	// NATS settings
	NATS struct {
		URL string
		// ClusterID is the NATS cluster ID for JetStream
		ClusterID string
		// ClientID is the unique client ID for the NATS connection
		ClientID string
		// SubjectPrefix is the leading namespace token for the telemetry.*
		// subject family (PRD §7 NATS_SUBJECT_PREFIX). Default "telemetry"
		// yields telemetry.raw.<IMEI> / telemetry.live.<IMEI> /
		// telemetry.error.<IMEI>. Empty disables the prefix.
		// NOTE (GAP2 resolution): alert.* and notify.* subjects intentionally
		// remain unprefixed to preserve the documented convention
		// (alert.geofence.*, alert.sos.*, notify.alert.<vehicle_id>).
		SubjectPrefix string
	}

	// DatabaseProvider selects the persistent DB engine (PRD §7.1.1):
	// "mysql" (default) or "postgres". The rest of the stack (tenant
	// manager, DB client, batch insert, migrations, controllers) branches on
	// this via internal/dialect.Dialect. MySQL is ALWAYS preserved as a
	// selectable provider; switching back is just DATABASE_PROVIDER=mysql.
	DatabaseProvider string

	// MySQL settings
	MySQL struct {
		// DSN is the Data Source Name for MySQL connection
		DSN string
		// MaxOpenConns is the maximum number of open connections to the database
		MaxOpenConns int
		// MaxIdleConns is the maximum number of idle connections in the pool
		MaxIdleConns int
		// ConnMaxLifetime is the maximum amount of time a connection may be reused
		ConnMaxLifetime time.Duration
	}

	// PostgreSQL settings (used when DatabaseProvider == "postgres").
	// Mirrors the POSTGRES_* env keys documented in PRD §7.
	Postgres struct {
		// DSN is the resolved PostgreSQL URL DSN (connstring or URL form).
		DSN string
		// MaxOpenConns / MaxIdleConns are shared with MySQL for pooling parity.
		MaxOpenConns    int
		MaxIdleConns    int
		ConnMaxLifetime time.Duration
	}

	// Redis settings
	Redis struct {
		// Addr is the address to connect to Redis
		Addr string
		// Password is the password for Redis authentication
		Password string
		// DB is the database number to use
		DB int
		// PoolSize is the maximum number of connections in the pool
		PoolSize int
		// KeyPrefix is the namespace prefix for keys (PRD §7 "adatrack_gps:").
		KeyPrefix string
	}

	// WebSocket settings
	WebSocket struct {
		// ReadBufferSize is the size of the buffer for reading WebSocket messages
		ReadBufferSize int
		// WriteBufferSize is the size of the buffer for writing WebSocket messages
		WriteBufferSize int
		// PongWait specifies the maximum amount of time allowed to wait for
		// a pong message from the client before force disconnecting
		PongWait time.Duration
		// WriteWait specifies the maximum amount of time allowed to wait for
		// a writer to flush before dropping the connection
		WriteWait time.Duration
		// MaxMessageSize is the maximum size a message may be when receiving
		// Must be less than or equal to the max message size configured on the server
		MaxMessageSize int
		// MaxConnections is the maximum number of concurrent WebSocket
		// connections accepted by service-websocket (FR-5.4).
		MaxConnections int
		// MaxQueue is the per-connection outbound message queue size.
		// When exceeded the oldest message is dropped + logged (FR-5.4).
		MaxQueue int
		// HeartbeatInterval is how often the server pings clients (FR-5.3).
		HeartbeatInterval time.Duration
	}

	// HTTP settings for the service-websocket main HTTP server
	// (REST API + WebSocket + /healthz + /metrics).
	HTTP struct {
		// Addr is the main listen address (REST + WS). Read from HTTP_ADDR or
		// built from HTTP_PORT (PRD §7).
		Addr string
		// MetricsAddr is where /metrics is served (separate port so many
		// services can run on the same host without clashing with :8080).
		MetricsAddr string
		// CORSOrigins is the comma-separated allowed origin list.
		CORSOrigins []string
		ReadTimeout time.Duration
		// WriteTimeout for the HTTP service.
		WriteTimeout time.Duration
		// TLS enables HTTPS (REST + WSS) for production (PRD §8.2 / B4 hardening).
		TLS struct {
			Enabled  bool   // TLS_ENABLED (default false -> dev HTTP; set true for prod HTTPS/WSS)
			CertFile string // TLS_CERT_FILE (path to PEM cert chain)
			KeyFile  string // TLS_KEY_FILE  (path to PEM private key)
		}
	}

	// JWT settings (service-websocket auth, GAP #2).
	JWT struct {
		// Secret is the HMAC-SHA256 signing secret.
		Secret string
		// Expiry is the token lifetime (PRD §7 JWT_EXPIRY_HOURS, default 24h).
		Expiry time.Duration
		// RefreshExpiry is the refresh-token lifetime
		// (JWT_REFRESH_EXPIRY_HOURS, default 168h = 7 hari).
		RefreshExpiry time.Duration
		// RevocationEnabled mengaktifkan denylist jti (logout/revoke) —
		// JWT_REVOCATION_ENABLED, default true.
		RevocationEnabled bool
	}

	// RateLimit settings (service-websocket, api-vehicle, GAP #12).
	RateLimit struct {
		// LoginMaxAttempts is the max failed login attempts per window per IP.
		LoginMaxAttempts int
		// LoginWindow is the window used to count login failures.
		LoginWindow time.Duration
		// APIMaxPerMinute is the per-user authenticated API request limit.
		APIMaxPerMinute int
		// Account lockout (enterprise security): after LoginLockoutThreshold
		// failed password attempts within LoginLockoutWindow, the user's
		// master.users row is locked via locked_until.
		LoginLockoutThreshold int           // env: LOGIN_LOCKOUT_THRESHOLD (default 5)
		LoginLockoutWindow    time.Duration // env: LOGIN_LOCKOUT_WINDOW_MIN (default 15)
	}

	// SOS settings (worker-alert, B3 automatic escalation).
	SOS struct {
		// EscalationMinutes is how long an OPEN SOS alert may stay
		// un-acknowledged before worker-alert escalates it automatically.
		EscalationMinutes time.Duration
		// EscalationMax is the maximum number of automatic escalation
		// rounds performed for a single alert.
		EscalationMax int
	}
}

// LoadConfig loads configuration from environment variables.
// Returns a configured Config struct with safe defaults.
func LoadConfig() *Config {
	c := &Config{}

	// Muat file .env (ENV_FILE > .env.<adatrack_ENV> > .env) terlebih dahulu.
	// Environment proses yang sudah ter-set tetap menang (tidak ditimpa).
	if loaded := LoadEnvFiles(); len(loaded) > 0 {
		slog.Debug("loaded dotenv files", "files", strings.Join(loaded, ","))
	}

	// Server
	c.Server.Addr = getEnv("SERVER_ADDR", ":8080")

	// TCP Ingestion
	c.TCP.Port = getEnv("TCP_PORT", "9000")
	c.TCP.MaxConnections = getEnvInt("TCP_MAX_CONNECTIONS", 5000)
	// Dedicated per-protocol listeners; default disabled (\"0\").
	c.TCP.TeltonikaPort = getEnv("TELTONIKA_TCP_PORT", "0")
	c.TCP.TK103Port = getEnv("TK103_TCP_PORT", "0")

	// NATS
	// PRD §7: nats://localhost:4222 -> default host dev (service berjalan di host,
	// container NATS compose mem-publish 4222 ke host).
	c.NATS.URL = getEnv("NATS_URL", "127.0.0.1:4222")
	c.NATS.ClusterID = getEnv("NATS_CLUSTER_ID", "")
	c.NATS.ClientID = getEnv("NATS_CLIENT_ID", "")
	// NATS_SUBJECT_PREFIX default "telemetry" (PRD §7) — applied to telemetry.*.
	c.NATS.SubjectPrefix = getEnv("NATS_SUBJECT_PREFIX", "telemetry")

	// Database provider (PRD §7.1.1: DATABASE_PROVIDER=postgres|mysql).
	// Default PROYEK = "postgres" (keputusan 2026-08-25); backend/.env selalu
	// jadi sumber efektif — nilai ini hanya fallback bila key absen.
	c.DatabaseProvider = getEnv("DATABASE_PROVIDER", "postgres")

	// MySQL
	if c.DatabaseProvider == "mysql" {
		// Baca env granular (PRD §7): MYSQL_HOST/PORT/USER/PASSWORD/DB.
		// Prioritas MYSQL_DSN bila di-set. Default menyesuaikan compose dev (adatrack_gps_db).
		if dsn := os.Getenv("MYSQL_DSN"); dsn != "" {
			c.MySQL.DSN = dsn
		} else {
			mysqlHost := getEnv("MYSQL_HOST", "127.0.0.1")
			mysqlPort := getEnv("MYSQL_PORT", "3306")
			mysqlUser := getEnv("MYSQL_USER", "adatrack_gps_user")
			mysqlPass := getEnv("MYSQL_PASSWORD", "user@gps2608")
			mysqlDB := getEnv("MYSQL_DB", "adatrack_gps_db")
			c.MySQL.DSN = fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=True&multiStatements=True",
				mysqlUser, mysqlPass, mysqlHost, mysqlPort, mysqlDB)
		}
		c.MySQL.MaxOpenConns = getEnvInt("MYSQL_MAX_OPEN_CONNS", 50)
		c.MySQL.MaxIdleConns = getEnvInt("MYSQL_MAX_IDLE_CONNS", 20)
		c.MySQL.ConnMaxLifetime = time.Duration(getEnvInt("MYSQL_CONN_MAX_LIFETIME", 30)) * time.Second
	} else {
		// PostgreSQL: build a pgx stdlib URL DSN from POSTGRES_* env (PRD §7).
		// DATABASE_URL (already URL-encoded in .env) takes priority when set.
		c.Postgres.DSN = postgresDSN()
		c.Postgres.MaxOpenConns = getEnvInt("POSTGRES_POOL_MAX", getEnvInt("MYSQL_MAX_OPEN_CONNS", 50))
		c.Postgres.MaxIdleConns = getEnvInt("POSTGRES_POOL_MIN", getEnvInt("MYSQL_MAX_IDLE_CONNS", 20))
		c.Postgres.ConnMaxLifetime = time.Duration(getEnvInt("MYSQL_CONN_MAX_LIFETIME", 30)) * time.Second
	}

	// HTTP (service-websocket main server; PRD §7: HTTP_PORT, default 8080)
	if addr := os.Getenv("HTTP_ADDR"); addr != "" {
		c.HTTP.Addr = addr
	} else {
		c.HTTP.Addr = ":" + getEnv("HTTP_PORT", "8080")
	}
	c.HTTP.MetricsAddr = getEnv("METRICS_ADDR", ":9090")
	c.HTTP.ReadTimeout = time.Duration(getEnvInt("HTTP_READ_TIMEOUT", 15)) * time.Second
	c.HTTP.WriteTimeout = time.Duration(getEnvInt("HTTP_WRITE_TIMEOUT", 30)) * time.Second

	// TLS (HTTPS/WSS) configuration for REST + WebSocket (B4 / PRD §8.2).
	// Default off so local dev keeps plain HTTP; enable in prod via env.
	c.HTTP.TLS.Enabled = getEnvBool("TLS_ENABLED", false)
	c.HTTP.TLS.CertFile = getEnv("TLS_CERT_FILE", "certs/dev.crt")
	c.HTTP.TLS.KeyFile = getEnv("TLS_KEY_FILE", "certs/dev.key")
	if origins := os.Getenv("CORS_ALLOWED_ORIGINS"); origins != "" {
		for _, o := range strings.Split(origins, ",") {
			if o = strings.TrimSpace(o); o != "" {
				c.HTTP.CORSOrigins = append(c.HTTP.CORSOrigins, o)
			}
		}
	} else {
		c.HTTP.CORSOrigins = []string{"*"}
	}

	// JWT (PRD §7: JWT_SECRET, JWT_EXPIRY_HOURS=24)
	c.JWT.Secret = getEnv("JWT_SECRET", "change-me-in-production")
	c.JWT.Expiry = time.Duration(getEnvInt("JWT_EXPIRY_HOURS", 24)) * time.Hour
	// B4 hardening: refresh token + revocation (denylist jti).
	c.JWT.RefreshExpiry = time.Duration(getEnvInt("JWT_REFRESH_EXPIRY_HOURS", 168)) * time.Hour
	c.JWT.RevocationEnabled = getEnvBool("JWT_REVOCATION_ENABLED", true)

	// Redis
	// PRD §7: REDIS_HOST/REDIS_PORT (compose mem-publish 6379 -> host).
	c.Redis.Addr = getEnv("REDIS_ADDR", "127.0.0.1:6379")
	c.Redis.Password = getEnv("REDIS_PASSWORD", "")
	c.Redis.DB = getEnvInt("REDIS_DB", 0)
	c.Redis.PoolSize = getEnvInt("REDIS_POOL_SIZE", 10)
	// Key prefix namespace (PRD §7 format adatrack_gps:{company}:...).
	c.Redis.KeyPrefix = getEnv("REDIS_KEY_PREFIX", "adatrack_gps:")

	// WebSocket
	c.WebSocket.ReadBufferSize = getEnvInt("WS_READ_BUFFER", 4096)
	c.WebSocket.WriteBufferSize = getEnvInt("WS_WRITE_BUFFER", 4096)
	pongWait := getEnvInt("WS_PONG_WAIT", 30)
	c.WebSocket.PongWait = time.Duration(pongWait) * time.Second
	writeWait := getEnvInt("WS_WRITE_WAIT", 10)
	c.WebSocket.WriteWait = time.Duration(writeWait) * time.Second
	c.WebSocket.MaxMessageSize = getEnvInt("WS_MAX_MESSAGE_SIZE", 256*1024)                            // 256KB
	c.WebSocket.MaxConnections = getEnvInt("WS_MAX_CONNECTIONS", 5000)                                 // FR-5.4
	c.WebSocket.MaxQueue = getEnvInt("WS_MAX_QUEUE", 1000)                                             // FR-5.4
	c.WebSocket.HeartbeatInterval = time.Duration(getEnvInt("WS_HEARTBEAT_SECONDS", 30)) * time.Second // FR-5.3 ping ~30s

	// Rate limit (GAP #12)
	c.RateLimit.LoginMaxAttempts = getEnvInt("RATE_LIMIT_LOGIN_ATTEMPTS", 5)
	c.RateLimit.LoginWindow = time.Duration(getEnvInt("RATE_LIMIT_LOGIN_WINDOW_MIN", 15)) * time.Minute
	c.RateLimit.APIMaxPerMinute = getEnvInt("RATE_LIMIT_API_PER_MIN", 100)
	// Account lockout (enterprise security, GAP #12)
	c.RateLimit.LoginLockoutThreshold = getEnvInt("LOGIN_LOCKOUT_THRESHOLD", 5)
	c.RateLimit.LoginLockoutWindow = time.Duration(getEnvInt("LOGIN_LOCKOUT_WINDOW_MIN", 15)) * time.Minute

	// SOS escalation (B3: automatic escalation when un-acknowledged)
	c.SOS.EscalationMinutes = time.Duration(getEnvInt("SOS_ESCALATION_MINUTES", 2)) * time.Minute
	c.SOS.EscalationMax = getEnvInt("SOS_ESCALATION_MAX", 3)

	return c
}

// getEnv reads an environment variable or returns a default value.
func getEnv(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists && value != "" {
		return value
	}
	return defaultValue
}

// EnvOr returns the environment variable value if set & non-empty, otherwise
// fallback. Exported for service entry points (e.g. per-service bind address).
func EnvOr(key, fallback string) string {
	return getEnv(key, fallback)
}

// getEnvInt reads an environment variable as integer or returns a default.
func getEnvInt(key string, defaultValue int) int {
	if value, exists := os.LookupEnv(key); exists {
		result, err := strconv.Atoi(value)
		if err == nil {
			return result
		}
	}
	return defaultValue
}

// getEnvBool reads an environment variable as a boolean; "1", "t", "true"
// (case-insensitive) are parsed as true. Falls back to defaultValue on a
// missing/empty variable or an unparseable value.
func getEnvBool(key string, defaultValue bool) bool {
	if value, exists := os.LookupEnv(key); exists && value != "" {
		b, err := strconv.ParseBool(value)
		if err == nil {
			return b
		}
	}
	return defaultValue
}

// postgresDSN builds a PostgreSQL connection URL for the pgx v5 stdlib driver
// from POSTGRES_* env keys (PRD §7). DATABASE_URL, when set, is used as-is
// (already URL-encoded), which is the canonical .env form for Postgres.
func postgresDSN() string {
	if url := os.Getenv("DATABASE_URL"); url != "" {
		return url
	}
	host := getEnv("POSTGRES_HOST", "127.0.0.1")
	port := getEnv("POSTGRES_PORT", "5432")
	user := getEnv("POSTGRES_USER", "adatrack_gps_pg_user")
	pass := getEnv("POSTGRES_PASSWORD", "")
	db := getEnv("POSTGRES_DB", "adatrack_gps_db")
	sslmode := getEnv("POSTGRES_SSLMODE", "disable")
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s&search_path=%s",
		user, pass, host, port, db, sslmode, getEnv("POSTGRES_SCHEMA", "adatrack_gps"))
}

// Dialect returns the SQL dialect derived from DatabaseProvider.
// Exported accessor so callers need not import the dialect package directly.
func (c *Config) Dialect() dialect.Dialect {
	return dialect.FromProvider(c.DatabaseProvider)
}

// DriverName returns the database/sql driver name for the configured provider.
func (c *Config) DriverName() string {
	return c.Dialect().DriverName()
}

// DSN returns the active database DSN (mysql or postgres) based on provider.
func (c *Config) DSN() string {
	if c.DatabaseProvider == "postgres" {
		return c.Postgres.DSN
	}
	return c.MySQL.DSN
}

// Validate checks if the config has valid values.
func (c *Config) Validate() error {
	if c.Server.Addr == "" {
		return fmt.Errorf("server address is required")
	}
	if c.TCP.Port == "" {
		return fmt.Errorf("tcp port is required")
	}
	if c.NATS.URL == "" {
		return fmt.Errorf("nats url is required")
	}
	if c.DatabaseProvider == "mysql" {
		if c.MySQL.DSN == "" {
			return fmt.Errorf("mysql dsn is required")
		}
	} else if c.DatabaseProvider == "postgres" {
		if c.Postgres.DSN == "" {
			return fmt.Errorf("postgres dsn is required")
		}
	} else {
		return fmt.Errorf("unsupported DATABASE_PROVIDER=%q (use mysql|postgres)", c.DatabaseProvider)
	}
	if c.Redis.Addr == "" {
		return fmt.Errorf("redis addr is required")
	}
	if c.JWT.Secret == "" {
		return fmt.Errorf("jwt secret is required")
	}
	// TLS: when enabled the cert & key files must be provided.
	if c.HTTP.TLS.Enabled {
		if c.HTTP.TLS.CertFile == "" || c.HTTP.TLS.KeyFile == "" {
			return fmt.Errorf("tls enabled but tls cert/key file not set")
		}
	}
	return nil
}

// GetSpeedLimit returns the speed limit threshold from environment config.
func (c *Config) GetSpeedLimit() float64 {
	return float64(getEnvInt("SPEED_LIMIT", 80))
}

// GetGraceMargin returns the speed grace margin from environment config.
func (c *Config) GetGraceMargin() float64 {
	return float64(getEnvInt("SPEED_GRACE_MARGIN", 10))
}

// GetSOSEscalationMinutes returns how long an open SOS may stay un-acknowledged
// before worker-alert escalates it automatically (B3).
func (c *Config) GetSOSEscalationMinutes() time.Duration {
	return c.SOS.EscalationMinutes
}

// GetSOSEscalationMax returns the maximum automatic escalation rounds.
func (c *Config) GetSOSEscalationMax() int {
	return c.SOS.EscalationMax
}

// Subject builds a fully-qualified NATS subject in the telemetry.* namespace:
//
//	<NATS_SUBJECT_PREFIX>.<part1>.<part2>…
//
// Default prefix "telemetry" yields the documented subjects:
//
//	Subject("raw", imei)      -> telemetry.raw.<IMEI>
//	Subject("live", imei)     -> telemetry.live.<IMEI>
//	Subject("error", imei)    -> telemetry.error.<IMEI>
//	Subject("raw", ">")       -> telemetry.raw.>
//
// An empty prefix (or empty parts) emits the parts joined without a dot.
// NOTE: alert.* / notify.* subjects are intentionally NOT prefixed (use
// SubjectPlain) to keep the documented subject layout (GAP2 resolution).
func (c *Config) Subject(parts ...string) string {
	joined := strings.Join(parts, ".")
	if c.NATS.SubjectPrefix == "" || joined == "" {
		return joined
	}
	return c.NATS.SubjectPrefix + "." + joined
}

// SubjectPlain joins parts with '.' WITHOUT any prefix. Used for subjects
// outside the telemetry namespace: alert.<type>.<id>, notify.alert.<vehicle>,
// and their wildcard subscriptions.
func (c *Config) SubjectPlain(parts ...string) string {
	return strings.Join(parts, ".")
}
