// Package tenant implements the multi-tenant foundation (PRD §6, §7):
// ONE physical PostgreSQL database holding one SCHEMA per tenant
// (`adatrack_gps_master` + `adatrack_gps_{company_code}`), selected through the
// DSN search_path.
//
// The Manager owns the master pool plus a pre-warmed pool per active company,
// resolves IMEI → tenant through `master.tm_vehicle_imei_map` (anti-spoofing,
// FR-1.4, with an optional Redis cache) and provisions new tenants
// (schema + every company migration + seeds).
package tenant

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"ajb_gps/internal"
)

// Config holds the tenant routing parameters (PRD §7.2 MASTER_DB_*/COMPANY_DB_*).
type Config struct {
	// MasterSchema is the schema holding auth + reference + IMEI map.
	MasterSchema string
	// CompanyPrefix + code builds a company schema name (adatrack_gps_{code}).
	CompanyPrefix string
	// MigrationsDir is the company migration directory used by ProvisionTenant.
	MigrationsDir string
	// MasterMigrationsDir is used when a service migrates the master schema.
	MasterMigrationsDir string
	// LedgerTable is the migration ledger table name (tm_schema_migrations).
	LedgerTable string
	// CachePrefix/CacheTTL configure the optional IMEI cache.
	CachePrefix string
	CacheTTL    time.Duration
	// MigrateLockTimeout bounds the advisory lock wait (deploy concurrency).
	MigrateLockTimeout time.Duration
}

// NewConfigFromEnv resolves the tenant configuration from the environment,
// reusing the shared Config loader so there is a single source of defaults.
func NewConfigFromEnv() Config {
	c := internal.LoadConfig()
	return ConfigFromEnv(c)
}

// ConfigFromEnv derives the tenant config from a loaded shared config.
func ConfigFromEnv(c *internal.Config) Config {
	return Config{
		MasterSchema:        c.Migrate.MasterSchema,
		CompanyPrefix:       c.Migrate.CompanyPrefix,
		MigrationsDir:       c.Migrate.CompanyDir,
		MasterMigrationsDir: c.Migrate.MasterDir,
		LedgerTable:         c.Migrate.LedgerTable,
		CachePrefix:         internal.EnvOr("TENANT_CACHE_PREFIX", "adatrack_tenant:imei:"),
		CacheTTL:            time.Duration(internal.EnvIntDefault("TENANT_CACHE_TTL_SEC", 900)) * time.Second,
		MigrateLockTimeout:  c.Migrate.LockTimeout,
	}
}

// Validate rejects a configuration that cannot route tenants.
func (c Config) Validate() error {
	var errs []error
	if strings.TrimSpace(c.MasterSchema) == "" {
		errs = append(errs, errors.New("MASTER_DB_NAME must be set"))
	}
	if strings.TrimSpace(c.CompanyPrefix) == "" {
		errs = append(errs, errors.New("COMPANY_DB_PREFIX must be set"))
	}
	if strings.TrimSpace(c.MigrationsDir) == "" {
		errs = append(errs, errors.New("COMPANY_MIGRATIONS_DIR must be set"))
	}
	return errors.Join(errs...)
}

// CompanySchema returns the schema name for a company code (lowercased, PRD §6.2):
// `adatrack_gps_{lower(code)}`.
func (c Config) CompanySchema(code string) string {
	return c.CompanyPrefix + strings.ToLower(strings.TrimSpace(code))
}

// NormalizeCode uppercases and sanitises a company code for storage:
// only [A-Z0-9_] survives (schema-name safety, §9.6 input validation).
func NormalizeCode(code string) (string, error) {
	var sb strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(code)) {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			sb.WriteRune(r)
		}
	}
	out := sb.String()
	if out == "" {
		return "", fmt.Errorf("tenant: invalid company code %q (allowed: A-Z, 0-9, _)", code)
	}
	if len(out) > 20 {
		return "", fmt.Errorf("tenant: company code %q exceeds 20 characters", code)
	}
	return out, nil
}
