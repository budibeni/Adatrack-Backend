package tenant

// tenant_it_test.go — integration coverage for the multi-tenant foundation
// (PRD §6.2): pool routing, IMEI anti-spoofing resolution (+ Redis cache), the
// health probe and REAL tenant provisioning (schema + company migrations +
// master row). Opt-in via ADATRACK_IT=1 like the service suites; the provision
// fixture tenant is dropped in t.Cleanup.

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"adatrack_gps/internal"
)

// itCode builds a unique, provision-safe tenant code for this process.
func itCode() string { return "ITPROV" + strconv.Itoa(os.Getpid()%1000) }

// itIMEI is the unique device marker of this process.
func itIMEI() string { return "it-tenant-" + strconv.Itoa(os.Getpid()) }

// skipNoDB gates the IT suite behind ADATRACK_IT=1.
func skipNoDB(t *testing.T) {
	t.Helper()
	if v, ok := os.LookupEnv("ADATRACK_IT"); ok && v == "1" {
		return
	}
	t.Skip("integration test — set ADATRACK_IT=1 with live PostgreSQL (127.0.0.1:5533)")
}

// envOr reads an env override or the documented dev default.
func envOr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

// itManager wires the tenant manager on the live dev database (DEV001 seeded),
// with a real Redis-backed IMEI cache.
func itManager(t *testing.T) (*Manager, *internal.Config, *internal.RedisClient) {
	t.Helper()
	skipNoDB(t)

	internal.LoadProjectEnv()
	t.Setenv("POSTGRES_HOST", envOr("ADATRACK_IT_PG_HOST", "127.0.0.1"))
	t.Setenv("POSTGRES_PORT", envOr("ADATRACK_IT_PG_PORT", "5533"))
	t.Setenv("REDIS_HOST", envOr("ADATRACK_IT_REDIS_HOST", "127.0.0.1"))
	t.Setenv("REDIS_PORT", envOr("ADATRACK_IT_REDIS_PORT", "6380"))

	cfg := internal.LoadConfig()
	red, err := internal.NewRedisClient(cfg)
	if err != nil {
		t.Fatalf("redis unavailable: %v", err)
	}
	t.Cleanup(func() { _ = red.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tcfg := ConfigFromEnv(cfg)
	m, err := New(ctx, cfg, tcfg, red, prometheus.NewRegistry())
	if err != nil {
		t.Fatalf("tenant manager: %v", err)
	}
	t.Cleanup(m.Close)
	return m, cfg, red
}

func TestITManagerRouting(t *testing.T) {
	m, cfg, _ := itManager(t)
	ctx := context.Background()

	if m.Master() == nil || m.Config().MasterSchema != cfg.Migrate.MasterSchema {
		t.Fatal("the master pool/config must be exposed")
	}

	// Routing is case-insensitive and blank codes are rejected.
	pool, err := m.DB("dev001")
	if err != nil || pool == nil {
		t.Fatalf("DB(dev001): %v", err)
	}
	if _, err := m.DB(""); err != ErrCompanyNotFound {
		t.Errorf("DB(\"\") = %v, want ErrCompanyNotFound", err)
	}
	if _, err := m.DB("NO_SUCH_TENANT_ZZ"); err == nil {
		t.Error("an unknown tenant must fail")
	}

	// The schema name follows the company code, and the discovered registry
	// exposes the schema of a known tenant.
	if got := m.Config().CompanySchema("Dev001"); got != "adatrack_gps_dev001" {
		t.Errorf("CompanySchema = %q", got)
	}
	if got := m.Schema("dev001"); got != "adatrack_gps_dev001" {
		t.Errorf("Schema(dev001) = %q, want the discovered schema", got)
	}
	if got := m.Schema("nope"); got != "" {
		t.Errorf("Schema(unknown) = %q, want empty", got)
	}

	// Refresh re-discovers; Health aggregates master + every pool.
	if err := m.Refresh(ctx); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if err := m.Health(ctx); err != nil {
		t.Fatalf("Health: %v", err)
	}

	// Run exits when its context ends.
	runCtx, runCancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(runCtx); close(done) }()
	runCancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run must return when the context is cancelled")
	}
}

func TestITResolveDeviceByIMEI(t *testing.T) {
	m, _, red := itManager(t)
	ctx := context.Background()
	imei := itIMEI()

	// Fixture: the only authority mapping IMEI → tenant (FR-1.4).
	if _, err := m.Master().DB.ExecContext(ctx, `
INSERT INTO tm_vehicle_imei_map (imei, company_code, vehicle_id, is_active)
VALUES ($1, 'DEV001', 7, TRUE)
ON CONFLICT (imei) DO UPDATE SET company_code = 'DEV001', vehicle_id = 7, is_active = TRUE, deleted_at = NULL`, imei); err != nil {
		t.Fatalf("imei fixture: %v", err)
	}
	t.Cleanup(func() {
		_, _ = m.Master().DB.ExecContext(context.Background(), `DELETE FROM tm_vehicle_imei_map WHERE imei = $1`, imei)
	})

	// Miss the cache first (fresh key), then hit it on the repeat.
	di, err := m.ResolveDeviceByIMEI(ctx, imei)
	if err != nil {
		t.Fatalf("ResolveDeviceByIMEI: %v", err)
	}
	if di.CompanyCode != "DEV001" || di.VehicleID != 7 {
		t.Errorf("resolved = %+v, want DEV001/7", di)
	}
	di2, err := m.ResolveDeviceByIMEI(ctx, imei)
	if err != nil || di2 != di {
		t.Errorf("cached resolution = (%+v, %v), want the same mapping", di2, err)
	}

	// An unknown IMEI is rejected with the sentinel error.
	if _, err := m.ResolveDeviceByIMEI(ctx, "not-registered-xyz"); err == nil || !strings.HasPrefix(err.Error(), ErrIMEINotRegistered.Error()) {
		t.Errorf("unknown IMEI = %v, want ErrIMEINotRegistered", err)
	}

	// A disabled mapping is also rejected (anti-spoofing).
	if _, err := m.Master().DB.ExecContext(ctx, `UPDATE tm_vehicle_imei_map SET is_active = FALSE WHERE imei = $1`, imei); err != nil {
		t.Fatalf("disable fixture: %v", err)
	}
	_ = red.Del(ctx, m.Config().CachePrefix+imei) // drop the cached copy first
	if _, err := m.ResolveDeviceByIMEI(ctx, imei); err == nil {
		t.Error("a disabled IMEI must be rejected")
	}
}

// TestITProvisionTenant: a REAL tenant provision (master row + schema + every
// company migration + ledger verify) and its idempotent re-run; the fixture
// tenant is dropped in cleanup.
func TestITProvisionTenant(t *testing.T) {
	m, _, _ := itManager(t)
	ctx := context.Background()
	code := itCode()
	schema := m.Config().CompanySchema(code)

	// Input validation first (no side effects).
	if _, err := m.ProvisionCompany(ctx, ProvisionOptions{Code: "!!!", Name: "x"}); err == nil {
		t.Error("an invalid code must be rejected")
	}
	if _, err := m.ProvisionCompany(ctx, ProvisionOptions{Code: code, Name: "x", CountryCode: "IDN"}); err == nil {
		t.Error("a 3-letter country must be rejected")
	}
	if _, err := m.ProvisionCompany(ctx, ProvisionOptions{Code: code, Name: "x", BusinessType: "d2c"}); err == nil {
		t.Error("an unknown business type must be rejected")
	}

	res, err := m.ProvisionCompany(ctx, ProvisionOptions{Code: code, Name: "IT Provision Fixture " + code, BusinessType: "b2c"})
	if err != nil {
		t.Fatalf("ProvisionCompany: %v", err)
	}
	if res.CompanyCode != code || res.Schema != schema || res.BusinessType != "b2c" {
		t.Errorf("result = %+v", res)
	}
	if res.Migrations == nil || res.Migrations.Applied == 0 {
		t.Errorf("migrations = %+v, want the full company set applied", res.Migrations)
	}

	// The schema really exists and the ledger is complete.
	var n int
	if err := m.Master().DB.QueryRowContext(ctx, `
SELECT count(*) FROM information_schema.schemata WHERE schema_name = $1`, schema).Scan(&n); err != nil || n != 1 {
		t.Fatalf("schema %s exists = (%d, %v)", schema, n, err)
	}
	if _, err := m.DB(code); err != nil {
		t.Errorf("the provisioned tenant must be routable: %v", err)
	}

	// Idempotent re-run: no pending migrations, data untouched.
	res2, err := m.ProvisionCompany(ctx, ProvisionOptions{Code: code, Name: "IT Provision Fixture " + code, BusinessType: "b2c"})
	if err != nil {
		t.Fatalf("re-provision: %v", err)
	}
	if res2.Migrations == nil || res2.Migrations.Applied != 0 {
		t.Errorf("re-provision migrations = %+v, want 0 applied", res2.Migrations)
	}

	// Cleanup: drop the fixture tenant completely.
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = m.Master().DB.ExecContext(bg, `DROP SCHEMA IF EXISTS `+schema+` CASCADE`)
		_, _ = m.Master().DB.ExecContext(bg, `DELETE FROM tm_companies WHERE code = $1`, code)
	})
}
