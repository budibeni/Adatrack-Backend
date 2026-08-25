package internal

import (
	"os"
	"testing"
	"time"

	"ajb_gps/internal/dialect"
)

// withEnv sets a list of env vars and returns a restore func.
func withEnv(vars map[string]string) func() {
	old := make(map[string]string)
	for k, v := range vars {
		old[k] = os.Getenv(k)
		if v == "" {
			os.Unsetenv(k)
		} else {
			os.Setenv(k, v)
		}
	}
	return func() {
		for k, v := range old {
			if v == "" {
				os.Unsetenv(k)
			} else {
				os.Setenv(k, v)
			}
		}
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	restore := withEnv(map[string]string{
		"DATABASE_PROVIDER": "mysql",
		"SERVER_ADDR":       "",
		"TCP_PORT":          "",
		"NATS_URL":          "",
		"MYSQL_DSN":         "",
		"REDIS_ADDR":        "",
		"JWT_SECRET":        "",
	})
	defer restore()

	c := LoadConfig()

	if c.TCP.Port != "9000" {
		t.Errorf("default TCP_PORT = %q, want 9000", c.TCP.Port)
	}
	if c.TCP.MaxConnections != 5000 {
		t.Errorf("default MaxConnections = %d, want 5000", c.TCP.MaxConnections)
	}
	if c.NATS.URL != "127.0.0.1:4222" {
		t.Errorf("default NATS.URL = %q, want 127.0.0.1:4222", c.NATS.URL)
	}
	if c.MySQL.MaxOpenConns != 50 {
		t.Errorf("default MaxOpenConns = %d, want 50", c.MySQL.MaxOpenConns)
	}
	if c.MySQL.MaxIdleConns != 20 {
		t.Errorf("default MaxIdleConns = %d, want 20", c.MySQL.MaxIdleConns)
	}
	if c.Redis.Addr != "127.0.0.1:6379" {
		t.Errorf("default Redis.Addr = %q, want 127.0.0.1:6379", c.Redis.Addr)
	}
	if c.JWT.Expiry != 24*time.Hour {
		t.Errorf("default JWT.Expiry = %v, want 24h", c.JWT.Expiry)
	}
	if c.WebSocket.MaxConnections != 5000 {
		t.Errorf("default WS MaxConnections = %d, want 5000", c.WebSocket.MaxConnections)
	}
	if c.RateLimit.LoginMaxAttempts != 5 {
		t.Errorf("default LoginMaxAttempts = %d, want 5", c.RateLimit.LoginMaxAttempts)
	}
}

func TestLoadConfigOverrides(t *testing.T) {
	restore := withEnv(map[string]string{
		"DATABASE_PROVIDER":      "mysql",
		"TCP_PORT":               "9100",
		"TCP_MAX_CONNECTIONS":    "100",
		"NATS_URL":               "nats://nats:4222",
		"MYSQL_DSN":              "user:pass@tcp(db:3306)/adatrack_gps_db?parseTime=True",
		"REDIS_ADDR":             "redis:6379",
		"REDIS_POOL_SIZE":        "20",
		"JWT_SECRET":             "my-secret-key",
		"JWT_EXPIRY_HOURS":       "48",
		"WS_MAX_CONNECTIONS":     "200",
		"WS_HEARTBEAT_SECONDS":   "15",
		"RATE_LIMIT_API_PER_MIN": "200",
	})
	defer restore()

	c := LoadConfig()

	if c.TCP.Port != "9100" {
		t.Errorf("TCP.Port = %q, want 9000", c.TCP.Port)
	}
	if c.TCP.MaxConnections != 100 {
		t.Errorf("MaxConnections = %d, want 100", c.TCP.MaxConnections)
	}
	if c.NATS.URL != "nats://nats:4222" {
		t.Errorf("NATS.URL = %q", c.NATS.URL)
	}
	if c.MySQL.DSN != "user:pass@tcp(db:3306)/adatrack_gps_db?parseTime=True" {
		t.Errorf("MySQL.DSN = %q", c.MySQL.DSN)
	}
	if c.Redis.Addr != "redis:6379" {
		t.Errorf("Redis.Addr = %q", c.Redis.Addr)
	}
	if c.Redis.PoolSize != 20 {
		t.Errorf("Redis.PoolSize = %d, want 20", c.Redis.PoolSize)
	}
	if c.JWT.Secret != "my-secret-key" {
		t.Errorf("JWT.Secret = %q", c.JWT.Secret)
	}
	if c.JWT.Expiry != 48*time.Hour {
		t.Errorf("JWT.Expiry = %v, want 48h", c.JWT.Expiry)
	}
	if c.WebSocket.MaxConnections != 200 {
		t.Errorf("WS.MaxConnections = %d, want 200", c.WebSocket.MaxConnections)
	}
	if c.WebSocket.HeartbeatInterval != 15*time.Second {
		t.Errorf("WS.HeartbeatInterval = %v, want 15s", c.WebSocket.HeartbeatInterval)
	}
	if c.RateLimit.APIMaxPerMinute != 200 {
		t.Errorf("RateLimit.APIMaxPerMinute = %d, want 200", c.RateLimit.APIMaxPerMinute)
	}
}

func TestLoadConfigMYSQLHost(t *testing.T) {
	restore := withEnv(map[string]string{
		"DATABASE_PROVIDER": "mysql",
		"MYSQL_DSN":         "",
		"MYSQL_HOST":        "dbhost",
		"MYSQL_PORT":        "3307",
		"MYSQL_USER":        "myuser",
		"MYSQL_PASSWORD":    "mypass",
		"MYSQL_DB":          "mydb",
	})
	defer restore()

	c := LoadConfig()
	expected := "myuser:mypass@tcp(dbhost:3307)/mydb?parseTime=True&multiStatements=True"
	if c.MySQL.DSN != expected {
		t.Errorf("MySQL.DSN = %q, want %q", c.MySQL.DSN, expected)
	}
}

func TestLoadConfigCORSEnv(t *testing.T) {
	restore := withEnv(map[string]string{
		"CORS_ALLOWED_ORIGINS": "https://app.example.com,https://dash.example.com",
	})
	defer restore()

	c := LoadConfig()
	if len(c.HTTP.CORSOrigins) != 2 {
		t.Fatalf("expected 2 CORS origins, got %d", len(c.HTTP.CORSOrigins))
	}
	if c.HTTP.CORSOrigins[0] != "https://app.example.com" {
		t.Errorf("CORSOrigins[0] = %q", c.HTTP.CORSOrigins[0])
	}
}

func TestConfigValidateMissingFields(t *testing.T) {
	c := &Config{}
	if err := c.Validate(); err == nil {
		t.Error("expected error for empty config, got nil")
	}
}

func TestConfigValidateValid(t *testing.T) {
	restore := withEnv(map[string]string{
		"DATABASE_PROVIDER": "mysql",
		"SERVER_ADDR":       ":8081",
		"TCP_PORT":          "9000",
		"NATS_URL":          "127.0.0.1:4222",
		"MYSQL_DSN":         "u:p@tcp(127.0.0.1:3306)/adatrack_gps_db?parseTime=True",
		"REDIS_ADDR":        "127.0.0.1:6379",
		"JWT_SECRET":        "test-secret",
	})
	defer restore()

	c := LoadConfig()
	if err := c.Validate(); err != nil {
		t.Errorf("unexpected validation error: %v", err)
	}
}

func TestConfigGetSpeedLimit(t *testing.T) {
	restore := withEnv(map[string]string{"SPEED_LIMIT": "100"})
	defer restore()
	c := LoadConfig()
	if c.GetSpeedLimit() != 100 {
		t.Errorf("GetSpeedLimit = %v, want 100", c.GetSpeedLimit())
	}
}

func TestConfigGetSpeedLimitDefault(t *testing.T) {
	restore := withEnv(map[string]string{"SPEED_LIMIT": ""})
	defer restore()
	c := LoadConfig()
	if c.GetSpeedLimit() != 80 {
		t.Errorf("GetSpeedLimit = %v, want 80", c.GetSpeedLimit())
	}
}

func TestConfigGetGraceMarginDefault(t *testing.T) {
	restore := withEnv(map[string]string{"SPEED_GRACE_MARGIN": ""})
	defer restore()
	c := LoadConfig()
	if c.GetGraceMargin() != 10 {
		t.Errorf("GetGraceMargin = %v, want 10", c.GetGraceMargin())
	}
}

func TestGetEnvIntInvalid(t *testing.T) {
	restore := withEnv(map[string]string{"BAD_INT_VAL": "not-a-number"})
	defer restore()
	// getEnvInt falls back to default for invalid input
	os.Setenv("BAD_INT_VAL2", "abc")
	defer os.Unsetenv("BAD_INT_VAL2")
	val := getEnvInt("BAD_INT_VAL2", 42)
	if val != 42 {
		t.Errorf("getEnvInt(invalid) = %d, want 42", val)
	}
}

func TestLoadConfigPostgresProvider(t *testing.T) {
	restore := withEnv(map[string]string{
		"DATABASE_PROVIDER": "postgres",
		"DATABASE_URL":      "postgres://adatrack_gps_pg_user:secret@postgres:5432/adatrack_gps_db?sslmode=disable&search_path=adatrack_gps",
		"SERVER_ADDR":       ":8080",
		"TCP_PORT":          "9000",
		"NATS_URL":          "127.0.0.1:4222",
		"REDIS_ADDR":        "127.0.0.1:6379",
		"JWT_SECRET":        "test-secret",
	})
	defer restore()

	c := LoadConfig()
	if c.DatabaseProvider != "postgres" {
		t.Fatalf("provider = %q, want postgres", c.DatabaseProvider)
	}
	if c.Dialect() != dialect.Postgres {
		t.Errorf("dialect = %s, want postgres", c.Dialect())
	}
	if c.DriverName() != dialect.PgxadatrackDriverName {
		t.Errorf("driver = %q, want %q", c.DriverName(), dialect.PgxadatrackDriverName)
	}
	if c.Postgres.DSN != "postgres://adatrack_gps_pg_user:secret@postgres:5432/adatrack_gps_db?sslmode=disable&search_path=adatrack_gps" {
		t.Errorf("postgres DSN = %q", c.Postgres.DSN)
	}
	if c.DSN() != c.Postgres.DSN {
		t.Error("DSN() mismatch")
	}
	// MySQL branch must be empty when postgres is selected.
	if c.MySQL.DSN != "" {
		t.Errorf("mysql DSN should be empty for postgres provider, got %q", c.MySQL.DSN)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("validate postgres: %v", err)
	}
}

func TestConfigValidateRejectsUnsupportedProvider(t *testing.T) {
	restore := withEnv(map[string]string{
		"DATABASE_PROVIDER": "oracle",
		"SERVER_ADDR":       ":8080",
		"TCP_PORT":          "9000",
		"NATS_URL":          "127.0.0.1:4222",
		"REDIS_ADDR":        "127.0.0.1:6379",
		"JWT_SECRET":        "test-secret",
		"MYSQL_DSN":         "u:p@tcp(127.0.0.1:3306)/adatrack_gps_db?parseTime=True",
	})
	defer restore()
	c := LoadConfig()
	if err := c.Validate(); err == nil {
		t.Error("expected error for unsupported provider")
	}
}

func TestPostgresDSNFromDiscreteEnv(t *testing.T) {
	restore := withEnv(map[string]string{
		"POSTGRES_HOST":     "pg-host",
		"POSTGRES_PORT":     "6543",
		"POSTGRES_USER":     "discrete",
		"POSTGRES_PASSWORD": "pw",
		"POSTGRES_DB":       "discdb",
		"POSTGRES_SSLMODE":  "require",
		"POSTGRES_SCHEMA":   "myschema",
	})
	defer restore()

	os.Unsetenv("DATABASE_URL")
	os.Unsetenv("DATABASE_PROVIDER")
	got := postgresDSN()
	want := "postgres://discrete:pw@pg-host:6543/discdb?sslmode=require&search_path=myschema"
	if got != want {
		t.Errorf("discrete postgres DSN = %q, want %q", got, want)
	}
}
