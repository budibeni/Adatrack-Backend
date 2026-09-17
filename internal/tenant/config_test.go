package tenant

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"adatrack_gps/internal"
)

// TestNormalizeCodeSanitizesInput validates the schema-name safety rule (PRD
// §9.6 input validation): only [A-Z0-9_] survives and the length is bounded.
func TestNormalizeCodeSanitizesInput(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"acme", "ACME", false},
		{"  dev001 ", "DEV001", false},
		{"ac-me!", "ACME", false}, // punctuation removed
		{"pt.maju/bersama", "PTMAJUBERSAMA", false},
		{"", "", true},                           // empty after sanitising
		{"---", "", true},                        // nothing valid survives
		{"abcdefghijklmnopqrstuvwxyz", "", true}, // 26 chars > 20
	}
	for _, tc := range cases {
		got, err := NormalizeCode(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("NormalizeCode(%q) = %q, want an error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("NormalizeCode(%q) returned an unexpected error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("NormalizeCode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestCompanySchemaDerivation verifies the schema naming convention (PRD §6.2):
// adatrack_gps_{lower(company_code)}.
func TestCompanySchemaDerivation(t *testing.T) {
	cfg := Config{MasterSchema: "adatrack_gps_master", CompanyPrefix: "adatrack_gps_"}
	if got := cfg.CompanySchema("DEV001"); got != "adatrack_gps_dev001" {
		t.Errorf("CompanySchema = %q, want adatrack_gps_dev001", got)
	}
	if got := cfg.CompanySchema("  AcMe  "); got != "adatrack_gps_acme" {
		t.Errorf("CompanySchema trimmed/lowered incorrectly: %q", got)
	}
}

// TestConfigValidation rejects incomplete configurations so a service fails at
// boot instead of mis-routing tenants at runtime.
func TestConfigValidation(t *testing.T) {
	good := Config{MasterSchema: "adatrack_gps_master", CompanyPrefix: "adatrack_gps_", MigrationsDir: "./database/migrations/company_pg"}
	if err := good.Validate(); err != nil {
		t.Fatalf("a complete configuration must validate, got %v", err)
	}

	for name, cfg := range map[string]Config{
		"missing master schema":  {CompanyPrefix: "adatrack_gps_", MigrationsDir: "d"},
		"missing company prefix": {MasterSchema: "m", MigrationsDir: "d"},
		"missing migrations":     {MasterSchema: "m", CompanyPrefix: "p"},
	} {
		if err := cfg.Validate(); err == nil {
			t.Errorf("%s: expected a validation error", name)
		}
	}
}

// TestConfigFromEnvDefaults verifies the tenant config inherits the shared
// defaults (single source of truth for env handling).
func TestConfigFromEnvDefaults(t *testing.T) {
	for _, k := range []string{"MASTER_DB_NAME", "COMPANY_DB_PREFIX", "COMPANY_MIGRATIONS_DIR", "TENANT_CACHE_TTL_SEC"} {
		t.Setenv(k, "")
		_ = k
	}
	t.Setenv("MASTER_DB_NAME", "")
	t.Setenv("COMPANY_DB_PREFIX", "")
	t.Setenv("COMPANY_MIGRATIONS_DIR", "")

	base := internal.LoadConfig()
	cfg := ConfigFromEnv(base)

	if cfg.MasterSchema != "adatrack_gps_master" {
		t.Errorf("MasterSchema = %q, want adatrack_gps_master", cfg.MasterSchema)
	}
	if cfg.CompanyPrefix != "adatrack_gps_" {
		t.Errorf("CompanyPrefix = %q, want adatrack_gps_", cfg.CompanyPrefix)
	}
	if !strings.HasSuffix(cfg.MigrationsDir, "database/migrations/company_pg") {
		t.Errorf("MigrationsDir = %q, want the company migrations dir", cfg.MigrationsDir)
	}
	if cfg.CachePrefix == "" || cfg.CacheTTL <= 0 {
		t.Errorf("cache defaults missing: prefix=%q ttl=%s", cfg.CachePrefix, cfg.CacheTTL)
	}
	if cfg.MigrateLockTimeout <= 0 {
		t.Errorf("MigrateLockTimeout = %s, want > 0 (deploy concurrency)", cfg.MigrateLockTimeout)
	}
}

// TestErrorIdentity documents the sentinel errors callers rely on: ingestion
// counts them as anti-spoofing rejections, persistence dead-letters them.
func TestErrorIdentity(t *testing.T) {
	if !errors.Is(fmt.Errorf("%w: 86001", ErrIMEINotRegistered), ErrIMEINotRegistered) {
		t.Error("wrapped IMEI errors must satisfy errors.Is(ErrIMEINotRegistered)")
	}
	if !errors.Is(fmt.Errorf("%w: DEV001", ErrCompanyNotFound), ErrCompanyNotFound) {
		t.Error("wrapped company errors must satisfy errors.Is(ErrCompanyNotFound)")
	}
}

// TestDeviceInfoJSONRoundTrip verifies the cache payload is stable — a change
// here would silently invalidate cached tenant lookups.
func TestDeviceInfoJSONRoundTrip(t *testing.T) {
	original := DeviceInfo{CompanyCode: "DEV001", VehicleID: 42}
	body, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(body) != `{"company_code":"DEV001","vehicle_id":42}` {
		t.Errorf("cache payload = %s (changing this breaks cached lookups)", body)
	}

	var decoded DeviceInfo
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded != original {
		t.Errorf("round trip = %+v, want %+v", decoded, original)
	}
}
