package tenant

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"backend/internal"
)

// ProvisionResult reports what a provisioning run changed.
type ProvisionResult struct {
	CompanyCode  string
	Schema       string
	BusinessType string
	Migrations   *internal.MigrationResult
	Created      bool
}

// ProvisionOptions describes a tenant to provision (FR-5.5: the API path passes
// country/timezone from the request; the CLI/scaffolding path uses the defaults).
type ProvisionOptions struct {
	Code         string
	Name         string
	BusinessType string // b2b (default) | b2c
	CountryCode  string // ISO-3166 alpha-2, default ID
	Timezone     string // default Asia/Jakarta
}

// Defaults applied when an option is omitted (PRD §7.2 defaults).
const (
	DefaultCountryCode = "ID"
	DefaultTimezone    = "Asia/Jakarta"
)

// Provision creates (or reconciles) a tenant schema with the default country and
// timezone. It is a thin wrapper over ProvisionCompany so existing callers keep
// working unchanged.
func (m *Manager) Provision(ctx context.Context, code, name, businessType string) (*ProvisionResult, error) {
	return m.ProvisionCompany(ctx, ProvisionOptions{Code: code, Name: name, BusinessType: businessType})
}

// ProvisionCompany creates (or reconciles) a tenant schema (PRD §6.2, §14.5 step 4):
//
//  1. master.tm_companies row (idempotent upsert, business_type b2b/b2c)
//  2. CREATE SCHEMA adatrack_gps_{code}
//  3. apply EVERY company migration (ledger + advisory lock, checksum-guarded)
//  4. warm the tenant pool so traffic can be served immediately
//
// It is idempotent: re-provisioning an existing tenant applies any pending
// migration and never overwrites existing data. The default tenant admin account
// (`Admin@123` + must_change_password) is created by the B2 API path (FR-5.5),
// which calls this function and then adds the admin user.
func (m *Manager) ProvisionCompany(ctx context.Context, opts ProvisionOptions) (*ProvisionResult, error) {
	normalized, err := NormalizeCode(opts.Code)
	if err != nil {
		return nil, err
	}
	code, name, businessType := normalized, opts.Name, opts.BusinessType
	country := strings.ToUpper(strings.TrimSpace(opts.CountryCode))
	if country == "" {
		country = DefaultCountryCode
	}
	if len(country) != 2 {
		return nil, fmt.Errorf("tenant: country_code must be a 2-letter ISO code, got %q", opts.CountryCode)
	}
	timezone := strings.TrimSpace(opts.Timezone)
	if timezone == "" {
		timezone = DefaultTimezone
	}
	businessType = strings.ToLower(strings.TrimSpace(businessType))
	if businessType == "" {
		businessType = "b2b"
	}
	if businessType != "b2b" && businessType != "b2c" {
		return nil, fmt.Errorf("tenant: business_type must be b2b|b2c, got %q", businessType)
	}
	if strings.TrimSpace(name) == "" {
		name = code + " Company"
	}

	res := &ProvisionResult{
		CompanyCode:  code,
		Schema:       m.cfg.CompanySchema(code),
		BusinessType: businessType,
	}

	// 1. Master registry row (idempotent).
	var companyID int64
	err = m.master.DB.QueryRowContext(ctx, `
		INSERT INTO tm_companies (code, name, country_code, business_type, timezone, is_active, activated_at)
		VALUES ($1, $2, $3, $4, $5, TRUE, CURRENT_TIMESTAMP)
		ON CONFLICT (code) DO UPDATE SET
			name = EXCLUDED.name,
			country_code = EXCLUDED.country_code,
			business_type = EXCLUDED.business_type,
			timezone = EXCLUDED.timezone,
			is_active = TRUE,
			updated_at = CURRENT_TIMESTAMP
		RETURNING id`, code, name, country, businessType, timezone).Scan(&companyID)
	if err != nil {
		return nil, fmt.Errorf("tenant: register company %s: %w", code, err)
	}
	res.Created = true

	// 2 + 3. Schema + migrations (advisory-locked, ledger-audited).
	migRes, err := m.migrateCompanySchema(ctx, res.Schema)
	if err != nil {
		return nil, err
	}
	res.Migrations = migRes

	// 4. Warm the pool so the new tenant is immediately routable.
	m.mu.Lock()
	if _, ok := m.pools[normalized]; !ok {
		pool, perr := internal.OpenPostgresPool(m.baseCfg, res.Schema, "company:"+res.Schema)
		if perr != nil {
			m.mu.Unlock()
			return nil, fmt.Errorf("tenant: open pool for %s: %w", res.Schema, perr)
		}
		m.pools[normalized] = pool
	}
	m.companies[normalized] = Company{
		Code:         normalized,
		Name:         name,
		BusinessType: businessType,
		IsActive:     true,
		Schema:       res.Schema,
	}
	companyPoolCount.Set(float64(len(m.pools)))
	m.mu.Unlock()

	slog.Info("tenant provisioned", "company", normalized, "schema", res.Schema,
		"business_type", businessType, "migrations_applied", res.Migrations.Applied)
	return res, nil
}

// migrateCompanySchema applies every company migration to a tenant schema and
// verifies the ledger afterwards (PRD §14.5 step 4 + 5).
func (m *Manager) migrateCompanySchema(ctx context.Context, schema string) (*internal.MigrationResult, error) {
	res, err := internal.ApplyMigrations(ctx, m.master.DB, "company:"+schema, schema,
		m.cfg.MigrationsDir, m.cfg.LedgerTable, m.cfg.MigrateLockTimeout)
	if err != nil {
		return nil, fmt.Errorf("tenant: migrate %s: %w", schema, err)
	}
	if err := verifyLedger(ctx, m.master.DB, schema, m.cfg.LedgerTable, m.cfg.MigrationsDir); err != nil {
		return nil, err
	}
	return res, nil
}

// verifyLedger asserts the tenant ledger is complete (§14.5 step 5): the number
// of successful rows must equal the number of migration files.
func verifyLedger(ctx context.Context, db *sql.DB, schema, ledgerTable, dir string) error {
	files, err := internalMigrationCount(dir)
	if err != nil {
		return err
	}
	applied, failures, err := internal.LedgerStatus(ctx, db, schema, ledgerTable)
	if err != nil {
		return fmt.Errorf("tenant: ledger check %s: %w", schema, err)
	}
	if failures > 0 {
		return fmt.Errorf("tenant: %s has %d failed migrations", schema, failures)
	}
	if applied < files {
		return fmt.Errorf("tenant: %s ledger incomplete (%d/%d applied)", schema, applied, files)
	}
	return nil
}

// internalMigrationCount counts the *.sql files of a migrations directory.
func internalMigrationCount(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("tenant: read migrations dir %s: %w", dir, err)
	}
	count := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			count++
		}
	}
	if count == 0 {
		return 0, errors.New("tenant: no migration files found in " + dir)
	}
	return count, nil
}
