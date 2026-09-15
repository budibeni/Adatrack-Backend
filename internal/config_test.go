package internal

import (
	"strings"
	"testing"
	"time"
)

// TestLoadConfigDefaults verifies every default documented in PRD §7.2.
func TestLoadConfigDefaults(t *testing.T) {
	clearEnv(t)
	cfg := LoadConfig()

	if cfg.NATS.SubjectPrefix != "telemetry" {
		t.Errorf("NATS_SUBJECT_PREFIX default = %q, want telemetry", cfg.NATS.SubjectPrefix)
	}
	if cfg.Server.MetricsAddr != ":8090" {
		t.Errorf("METRICS_ADDR default = %q, want :8090", cfg.Server.MetricsAddr)
	}
	if cfg.Persistence.BatchSize != 500 {
		t.Errorf("BATCH_SIZE default = %d, want 500 (FR-3.1)", cfg.Persistence.BatchSize)
	}
	if cfg.Persistence.BatchTimeout != 5*time.Second {
		t.Errorf("BATCH_TIMEOUT_SEC default = %s, want 5s (FR-3.1)", cfg.Persistence.BatchTimeout)
	}
	if cfg.Redis.TTL != 5*time.Minute {
		t.Errorf("REDIS_TTL_SEC default = %s, want 5m (FR-2.1)", cfg.Redis.TTL)
	}
	if cfg.Live.OfflineAfterMinutes != 3 {
		t.Errorf("OFFLINE_AFTER_MINUTES default = %d, want 3 (FR-2.2)", cfg.Live.OfflineAfterMinutes)
	}
	if cfg.TCP.MaxConnections != 5000 {
		t.Errorf("TCP_MAX_CONNECTIONS default = %d, want 5000 (FR-1.1)", cfg.TCP.MaxConnections)
	}
	if cfg.Migrate.LedgerTable != "tm_schema_migrations" {
		t.Errorf("MIGRATION_LEDGER_TABLE default = %q", cfg.Migrate.LedgerTable)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("the default configuration must validate, got %v", err)
	}
}

// TestLoadConfigFromEnv verifies env overrides win over the defaults.
func TestLoadConfigFromEnv(t *testing.T) {
	clearEnv(t)
	t.Setenv("BATCH_SIZE", "42")
	t.Setenv("NATS_SUBJECT_PREFIX", "adatrack")
	t.Setenv("JETSTREAM_MAX_AGE_HOURS", "12")
	t.Setenv("RETRY_BACKOFF_MS", "100,250")

	cfg := LoadConfig()
	if cfg.Persistence.BatchSize != 42 {
		t.Errorf("BATCH_SIZE = %d, want 42", cfg.Persistence.BatchSize)
	}
	if cfg.NATS.JetStreamMaxAgeHours != 12 {
		t.Errorf("JETSTREAM_MAX_AGE_HOURS = %d, want 12", cfg.NATS.JetStreamMaxAgeHours)
	}
	if len(cfg.Persistence.Backoff) != 2 ||
		cfg.Persistence.Backoff[0] != 100*time.Millisecond ||
		cfg.Persistence.Backoff[1] != 250*time.Millisecond {
		t.Errorf("RETRY_BACKOFF_MS parsed as %v, want [100ms 250ms]", cfg.Persistence.Backoff)
	}
}

// TestValidateRejectsBrokenConfig documents fail-fast boot behaviour.
func TestValidateRejectsBrokenConfig(t *testing.T) {
	clearEnv(t)
	cfg := LoadConfig()
	cfg.Server.MetricsAddr = ""
	cfg.Postgres.PoolMax = 1
	cfg.Postgres.PoolMin = 10

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected a validation error")
	}
	for _, want := range []string{"METRICS_ADDR", "POSTGRES_POOL_MAX"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("validation error %q does not mention %s", err, want)
		}
	}
}

// TestSubjectPrefixing verifies the telemetry namespace helpers (FR-1.6).
func TestSubjectPrefixing(t *testing.T) {
	clearEnv(t)
	cfg := LoadConfig()

	if got := cfg.Subject("raw", "864201040512345"); got != "telemetry.raw.864201040512345" {
		t.Errorf("Subject = %q", got)
	}
	if got := cfg.Subject("raw", ">"); got != "telemetry.raw.>" {
		t.Errorf("Subject wildcard = %q", got)
	}
	// alert.*/notify.* keep the documented layout without the telemetry prefix.
	if got := cfg.SubjectPlain("alert", "SOS", "DEV001"); got != "alert.SOS.DEV001" {
		t.Errorf("SubjectPlain = %q", got)
	}

	t.Setenv("NATS_SUBJECT_PREFIX", "")
	cfg = LoadConfig()
	if got := cfg.Subject("raw", "123"); got != "raw.123" {
		t.Errorf("an empty prefix must disable namespacing, got %q", got)
	}
}

// TestPostgresDSNForcesSearchPath is the regression guard for the multi-tenant
// bug where a tenant pool silently falls back to the default schema: the
// per-tenant search_path must ALWAYS be present, including with DATABASE_URL.
func TestPostgresDSNForcesSearchPath(t *testing.T) {
	clearEnv(t)
	cfg := LoadConfig()

	dsn := cfg.PostgresDSN("adatrack_gps_dev001")
	if !strings.Contains(dsn, "search_path=adatrack_gps_dev001") {
		t.Errorf("DSN without tenant search_path: %s", dsn)
	}
	if !strings.Contains(dsn, "sslmode=") {
		t.Errorf("DSN without sslmode: %s", dsn)
	}

	t.Setenv("DATABASE_URL", "postgres://user:pw@db:5432/adatrack_gps_db?sslmode=require")
	dsn = cfg.PostgresDSN("adatrack_gps_acme")
	if !strings.Contains(dsn, "search_path=adatrack_gps_acme") {
		t.Errorf("DATABASE_URL path lost the tenant search_path: %s", dsn)
	}
	if strings.Count(dsn, "sslmode=") != 1 {
		t.Errorf("sslmode must not be duplicated: %s", dsn)
	}

	// An unparsable URL still gets the search_path appended.
	t.Setenv("DATABASE_URL", "not-a-url")
	dsn = cfg.PostgresDSN("adatrack_gps_acme")
	if !strings.Contains(dsn, "search_path=adatrack_gps_acme") {
		t.Errorf("fallback concatenation lost the search_path: %s", dsn)
	}
}
