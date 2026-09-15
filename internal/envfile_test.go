package internal

import (
	"os"
	"path/filepath"
	"testing"
)

// clearEnv unsets every variable LoadConfig reads so defaults are observable.
func clearEnv(t *testing.T) {
	t.Helper()
	keys := []string{
		"BATCH_SIZE", "BATCH_TIMEOUT_SEC", "RETRY_MAX", "RETRY_BACKOFF_MS",
		"NATS_SUBJECT_PREFIX", "NATS_URL", "JETSTREAM_MAX_AGE_HOURS", "JETSTREAM_MAX_BYTES",
		"METRICS_ADDR", "REDIS_HOST", "REDIS_PORT", "REDIS_DB", "REDIS_TTL_SEC",
		"REDIS_KEY_PREFIX", "POSTGRES_HOST", "POSTGRES_PORT", "POSTGRES_DB", "POSTGRES_USER",
		"POSTGRES_PASSWORD", "POSTGRES_POOL_MIN", "POSTGRES_POOL_MAX", "DATABASE_URL",
		"TCP_PORT", "TELTONIKA_TCP_PORT", "TCP_MAX_CONNECTIONS", "TCP_IDLE_TIMEOUT_SECONDS",
		"OFFLINE_AFTER_MINUTES", "IDLE_AFTER_SECONDS", "LOG_LEVEL", "COMPOSE_VARIANT",
		"MASTER_DB_NAME", "COMPANY_DB_PREFIX", "MASTER_MIGRATIONS_DIR", "COMPANY_MIGRATIONS_DIR",
		"MIGRATION_LEDGER_TABLE", "MIGRATE_ON_BOOT", "LIVE_MAX_BATCH", "LIVE_BATCH_INTERVAL_MS",
		"FOUNDATION_METRICS_ADDR", "INGESTION_METRICS_ADDR", "LIVE_METRICS_ADDR",
		"PERSISTENCE_METRICS_ADDR",
	}
	for _, k := range keys {
		if err := os.Unsetenv(k); err != nil {
			t.Fatalf("unset %s: %v", k, err)
		}
	}
}

// TestLoadProjectEnvParsing verifies the .env parser: comments, `export`
// prefixes, quotes, and the rule that the real environment always wins.
func TestLoadProjectEnvParsing(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env.testvariant")
	content := "# comment\n\nBATCH_SIZE=17\nexport LOG_LEVEL=\"debug\"\nQUOTED='single'\n"
	if err := os.WriteFile(envFile, []byte(content), 0o600); err != nil {
		t.Fatalf("write env file: %v", err)
	}

	clearEnv(t)
	t.Setenv("COMPOSE_VARIANT", "testvariant")
	// A pre-existing variable must NOT be overwritten by the file (no mixed config).
	t.Setenv("LOG_LEVEL", "error")

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(cwd) }()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	LoadProjectEnv()

	if got := os.Getenv("BATCH_SIZE"); got != "17" {
		t.Errorf("BATCH_SIZE = %q, want 17", got)
	}
	if got := os.Getenv("LOG_LEVEL"); got != "error" {
		t.Errorf("LOG_LEVEL = %q, want error (the environment must win)", got)
	}
	if got := os.Getenv("QUOTED"); got != "single" {
		t.Errorf("QUOTED = %q, want single (quotes stripped)", got)
	}
}

// TestRedisAddrJoin checks the host:port helper.
func TestRedisAddrJoin(t *testing.T) {
	clearEnv(t)
	t.Setenv("REDIS_HOST", "cache.local")
	t.Setenv("REDIS_PORT", "6380")

	cfg := LoadConfig()
	if got := cfg.RedisAddr(); got != "cache.local:6380" {
		t.Errorf("RedisAddr = %q, want cache.local:6380", got)
	}
}

// TestResolvePathAgainstProjectRoot verifies that relative migration paths are
// resolved against the detected backend root (services started from their own
// subdirectory must still find database/migrations).
func TestResolvePathAgainstProjectRoot(t *testing.T) {
	previous := projectRoot
	defer func() { projectRoot = previous }()

	projectRoot = "/opt/adatrack/backend"
	if got := ResolvePath("./database/migrations/master_pg"); got != "/opt/adatrack/backend/database/migrations/master_pg" {
		t.Errorf("ResolvePath = %q", got)
	}
	// Absolute paths are returned untouched.
	if got := ResolvePath("/etc/adatrack"); got != "/etc/adatrack" {
		t.Errorf("ResolvePath(absolute) = %q", got)
	}
	// Unknown root: the path stays relative (documented fallback).
	projectRoot = ""
	if got := ResolvePath("./x"); got != "./x" {
		t.Errorf("ResolvePath without a root = %q, want ./x", got)
	}
}

// TestProjectRootDetection checks that walking up finds the backend root by the
// presence of database/migrations.
func TestProjectRootDetection(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "backend")
	if err := os.MkdirAll(filepath.Join(root, "database", "migrations"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	nested := filepath.Join(root, "services", "ingestion-tcp")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}

	if !isProjectRoot(root) {
		t.Error("isProjectRoot must accept a directory containing database/migrations")
	}
	if isProjectRoot(nested) {
		t.Error("isProjectRoot must reject a nested service directory")
	}
}
