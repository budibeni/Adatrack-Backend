package tenant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/redis/go-redis/v9"

	"ajb_gps/internal/dialect"
)

var (
	// ErrIMEINotRegistered is returned when the IMEI is not found in the
	// master vehicle_imei_map (anti-spoofing: reject & log unknown devices).
	ErrIMEINotRegistered = errors.New("tenant: IMEI not registered")

	// ErrCompanyNotFound is returned when a company_code has no pool.
	ErrCompanyNotFound = errors.New("tenant: company not found")
)

// Company is a tenant registered in master.companies.
type Company struct {
	Code      string `json:"code"`
	Name      string `json:"name"`
	LegalName string `json:"legal_name,omitempty"` // enterprise-standard legal entity name (migration 010)
	IsActive  bool   `json:"is_active"`
	DBName    string `json:"db_name"`
}

// DeviceInfo is the IMEI→(tenant, vehicle) mapping from master.vehicle_imei_map.
// VehicleID refers to vehicles.id in the company database (denormalized, no
// cross-DB FK). It may be 0 when not yet linked to a vehicle.
type DeviceInfo struct {
	CompanyCode string `json:"company_code"`
	VehicleID   int64  `json:"vehicle_id"`
}

// Cache implements the optional IMEI→company_code cache (Redis).
type Cache interface {
	redis.Cmdable
}

// Manager owns the master pool plus one pool per active company DB
// (pre-warmed at startup, roadmap task #4).
type Manager struct {
	cfg Config

	master     *sql.DB
	defaultDB  *sql.DB
	pools      map[string]*sql.DB // key: uppercase company_code
	replicas   map[string]*sql.DB // READ REPLICA pools (B4 HA), same key
	breakers   map[string]*breakerState
	companies  map[string]Company // key: uppercase company_code
	cache      Cache              // optional
	metrics    *tenantMetrics
	mu         sync.RWMutex
	closedOnce sync.Once
}

// New connects to the master DB, pre-warms a pool for every active company
// database, and (when a Redis client is provided) enables IMEI caching.
// registry is optional; when non-nil, tenant metrics are registered in it.
func New(ctx context.Context, cfg Config, cache Cache, registry metricsRegisterer) (*Manager, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	m := &Manager{
		cfg:       cfg,
		pools:     make(map[string]*sql.DB),
		replicas:  make(map[string]*sql.DB),
		breakers:  make(map[string]*breakerState),
		companies: make(map[string]Company),
		cache:     cache,
	}

	// Master pool (auth + vehicle_imei_map lookup).
	master, err := m.openPool(cfg.MasterDSN())
	if err != nil {
		return nil, fmt.Errorf("tenant: open master pool: %w", err)
	}
	m.master = master

	// Default database — fallback before any tenant is registered
	// (PRD §6.1 / Key Decision 6). Missing default DB is logged, not fatal;
	// Health() reports it.
	defaultName := m.cfg.CompanyDBName("default")
	if def, err := m.openPool(m.cfg.DBDSN(defaultName)); err == nil {
		m.defaultDB = def
	} else {
		slog.Warn("tenant: default DB not available", "db", defaultName, "error", err)
	}

	// Pre-warm every active company database (ideal for ≤50 tenants).
	if err := m.loadCompanies(ctx); err != nil {
		slog.Warn("tenant: failed listing companies", "error", err)
	}

	if registry != nil {
		m.metrics = newTenantMetrics(registry)
	}
	m.refreshMetrics()
	return m, nil
}

// loadCompanies reads master.companies and opens a warm pool for each.
// A company whose DB is unreachable is logged loudly (no silent drop) but
// does not block the healthy tenants; Health() reports it.
func (m *Manager) loadCompanies(ctx context.Context) error {
	rows, err := m.master.QueryContext(ctx,
		`SELECT code, name, COALESCE(legal_name, ''), COALESCE(is_active, TRUE)
		 FROM companies
		 WHERE deleted_at IS NULL
		 ORDER BY code`)
	if err != nil {
		return fmt.Errorf("tenant: query companies: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var code, name, legalName string
		var active bool
		if err := rows.Scan(&code, &name, &legalName, &active); err != nil {
			return err
		}
		key := strings.ToUpper(strings.TrimSpace(code))
		dbName := m.cfg.CompanyDBName(code)
		cp := Company{Code: code, Name: name, LegalName: legalName, IsActive: active, DBName: dbName}

		m.mu.Lock()
		m.companies[key] = cp
		m.mu.Unlock()

		if !active {
			slog.Info("tenant: skipping inactive company", "company", code)
			continue
		}
		pool, err := m.openPool(m.cfg.DBDSN(dbName))
		if err != nil {
			// Log loudly; keep manager usable for healthy tenants.
			slog.Error("tenant: failed to warm company pool", "company", code, "db", dbName, "error", err)
			continue
		}
		m.mu.Lock()
		m.pools[key] = pool
		m.mu.Unlock()
		slog.Info("tenant: company pool warmed", "company", code, "db", dbName)

		// B4 HA read/write split: warm READ REPLICA pool (best-effort).
		m.warmReplica(key, dbName)
	}
	return rows.Err()
}

// ProvisionCompanyInput holds the required fields when registering a new
// company tenant (PRD §6.1 auto-provision, Phase B2/B3). Optional
// enterprise-standard fields (migration 010) may be supplied for the tenant
// registry (legal name, contacts, tax id).
type ProvisionCompanyInput struct {
	Code        string // e.g. "ABLE01" — uppercased & trimmed internally
	Name        string // human-readable company name
	CountryCode string // ISO 3166-1 alpha-2, e.g. "ID"
	Timezone    string // IANA timezone, defaults to Asia/Jakarta
	// --- Enterprise-standard optional fields (migration 010) ---
	LegalName    string // legal entity name (may differ from display name)
	CompanyEmail string // official business contact email
	Website      string // company website URL
	TaxID        string // tax identification number (NPWP, VAT, etc.)
	PostalCode   string // postal code for company address
}

// CompanyProvisionResult describes the outcome of a provisioning attempt.
type CompanyProvisionResult struct {
	Code              string
	DBName            string
	MigrationsApplied int
}

// ProvisionCompany registers a new company in master.companies, creates its
// database adatrack_gps_{company_code}, applies all company migrations, and warms
// the connection pool. This is the auto-provision logic triggered when an admin
// registers a new company (PRD §6.1, B2/B3).
//
// If the company already exists (by code), the row is updated and migrations
// are re-applied (idempotent). The connection pool is replaced if present.
func (m *Manager) ProvisionCompany(ctx context.Context, input ProvisionCompanyInput) (*CompanyProvisionResult, error) {
	code := strings.ToUpper(strings.TrimSpace(input.Code))
	if code == "" {
		return nil, fmt.Errorf("tenant: company code is required")
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = code
	}
	countryCode := strings.ToUpper(strings.TrimSpace(input.CountryCode))
	if countryCode == "" {
		countryCode = "ID"
	}
	tz := strings.TrimSpace(input.Timezone)
	if tz == "" {
		tz = "Asia/Jakarta"
	}

	dbName := m.cfg.CompanyDBName(code)
	key := code
	d := m.cfg.Dialect()

	// 1) Upsert company in master.companies.
	// activated_at is set only on first insert (tenant activation timestamp);
	// re-provisioning an existing tenant does not reset it. Optional enterprise
	// fields (migration 010) are stored via NULLIF so empty input becomes NULL.
	// The INSERT shape is identical for mysql/postgres (? placeholders; pgx
	// rewrites them). Only the trailing upsert clause is dialect-specific.
	companySetExprs := []string{
		"name = " + d.ValuesExpr("name"),
		"country_code = " + d.ValuesExpr("country_code"),
		"timezone = " + d.ValuesExpr("timezone"),
		"is_active = TRUE",
		"legal_name = COALESCE(NULLIF(?, ''), legal_name)",
		"company_email = COALESCE(NULLIF(?, ''), company_email)",
		"website = COALESCE(NULLIF(?, ''), website)",
		"tax_id = COALESCE(NULLIF(?, ''), tax_id)",
		"postal_code = COALESCE(NULLIF(?, ''), postal_code)",
	}
	upsertClause := d.Upsert([]string{"code"}, companySetExprs)
	if _, err := m.master.ExecContext(ctx,
		`INSERT INTO companies `+
			`(code, name, country_code, timezone, is_active, activated_at,`+
			` legal_name, company_email, website, tax_id, postal_code)`+
			` VALUES (?, ?, ?, ?, TRUE, NOW(), `+
			`NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''))`+
			upsertClause,
		code, name, countryCode, tz,
		input.LegalName, input.CompanyEmail, input.Website, input.TaxID, input.PostalCode,
		input.LegalName, input.CompanyEmail, input.Website, input.TaxID, input.PostalCode); err != nil {
		return nil, fmt.Errorf("tenant: upsert company %s: %w", code, err)
	}
	slog.Info("tenant: company registered in master", "company", code, "provider", m.cfg.Provider)

	// 2) Create the tenant database (mysql) / schema (postgres) — idempotent.
	//    mysql: CREATE DATABASE IF NOT EXISTS `db` (...)
	//    pg:    CREATE SCHEMA IF NOT EXISTS "schema"
	if _, err := m.master.ExecContext(ctx, m.createTenantObjectDDL(dbName)); err != nil {
		return nil, fmt.Errorf("tenant: create database/schema %s: %w", dbName, err)
	}

	// 3) Open a pool to the new database.
	pool, err := m.openPool(m.cfg.DBDSN(dbName))
	if err != nil {
		return nil, fmt.Errorf("tenant: open pool for %s: %w", dbName, err)
	}

	// 4) Apply company migrations (if configured). Provider-specific dir is
	// selected via MigrationsDirFor (postgres → *_pg variant).
	applied := 0
	if m.cfg.MigrationsDir != "" {
		applied, err = applyCompanyMigrations(ctx, pool, m.cfg.MigrationsDirFor())
		if err != nil {
			pool.Close()
			return nil, fmt.Errorf("tenant: apply migrations for %s: %w", dbName, err)
		}
	}

	// 5) Warm the company pool + update in-memory registry.
	m.mu.Lock()
	prev := m.pools[key]
	m.pools[key] = pool
	m.companies[key] = Company{Code: code, Name: name, IsActive: true, DBName: dbName}
	m.mu.Unlock()
	if prev != nil {
		prev.Close()
	}

	// B4 HA read/write split: warm READ REPLICA pool for the new tenant
	// (best-effort — replika mungkin belum mereplikasi DB baru; fallback
	// primary tetap jalan dan warm-up berikutnya akan menambahkannya).
	if prevRep := func() *sql.DB {
		m.mu.RLock()
		defer m.mu.RUnlock()
		return m.replicas[key]
	}(); prevRep != nil {
		_ = prevRep.Close()
	}
	m.warmReplica(key, dbName)

	slog.Info("tenant: company provisioned", "company", code, "db", dbName, "migrations", applied)
	m.refreshMetrics()
	return &CompanyProvisionResult{Code: code, DBName: dbName, MigrationsApplied: applied}, nil
}

// createTenantObjectDDL returns the DDL that creates the tenant-level object
// used as the database (mysql) or schema (postgres) for a company.
//
//	mysql: CREATE DATABASE IF NOT EXISTS `adatrack_gps_able01` CHARACTER SET=utf8mb4 COLLATE=utf8mb4_unicode_ci
//	pg:    CREATE SCHEMA IF NOT EXISTS "adatrack_gps_able01"
func (m *Manager) createTenantObjectDDL(dbName string) string {
	d := m.cfg.Dialect()
	quoted := d.QuoteIdent(dbName)
	if m.cfg.Provider == "postgres" {
		return "CREATE SCHEMA IF NOT EXISTS " + quoted
	}
	return fmt.Sprintf("CREATE DATABASE IF NOT EXISTS %s CHARACTER SET=utf8mb4 COLLATE=utf8mb4_unicode_ci", quoted)
}

// applyCompanyMigrations runs every *.sql file in dir against db in lexical
// (versioned) order. Files are expected to be idempotent DDL
// (CREATE TABLE IF NOT EXISTS, INSERT ... ON CONFLICT / IGNORE).
//
// A file is split into individual statements before execution so the runner
// works on BOTH providers: MySQL opens with multiStatements=true (still fine),
// while the pgx v5 extended protocol forbids multiple statements in one Exec
// (see internal/dialect.SplitSQLStatements).
func applyCompanyMigrations(ctx context.Context, db *sql.DB, dir string) (int, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil {
		return 0, fmt.Errorf("glob %s: %w", dir, err)
	}
	sort.Strings(matches)

	applied := 0
	for _, f := range matches {
		content, err := os.ReadFile(f)
		if err != nil {
			return applied, fmt.Errorf("read %s: %w", filepath.Base(f), err)
		}
		stmts := dialect.SplitSQLStatements(string(content))
		for _, stmt := range stmts {
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				return applied, fmt.Errorf("exec %s (%s): %w", filepath.Base(f), truncate(stmt, 96), err)
			}
		}
		applied++
		slog.Info("tenant: company migration applied", "file", filepath.Base(f), "statements", len(stmts))
	}
	return applied, nil
}

// truncate shortens a SQL statement for error logging (never logs full DDL/seed).
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func (m *Manager) openPool(dsn string) (*sql.DB, error) {
	db, err := sql.Open(m.cfg.DriverName(), dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(m.cfg.PoolMax)
	db.SetMaxIdleConns(m.cfg.PoolMin)
	db.SetConnMaxLifetime(m.cfg.ConnMaxLifetime)

	// Verify reachability with retry + exponential backoff (global rule #8).
	var lastErr error
	delay := 1 * time.Second
	for attempt := 1; attempt <= 3; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), m.cfg.ConnectTimeout)
		lastErr = db.PingContext(ctx)
		cancel()
		if lastErr == nil {
			return db, nil
		}
		slog.Warn("tenant: MySQL ping failed, retrying", "attempt", attempt, "delay", delay, "error", lastErr)
		time.Sleep(delay)
		delay *= 3
	}
	db.Close()
	return nil, lastErr
}

// Master returns the master DB pool (auth authority + IMEI map).
func (m *Manager) Master() *sql.DB { return m.master }

// DefaultDB returns the fallback pool (adatrack_gps_default) or nil.
func (m *Manager) DefaultDB() *sql.DB { return m.defaultDB }

// Companies returns the snapshot of registered companies.
func (m *Manager) Companies() []Company {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Company, 0, len(m.companies))
	for _, c := range m.companies {
		out = append(out, c)
	}
	return out
}

// DB resolves a company_code to its pre-warmed pool. Codes are matched
// case-insensitively. Unknown companies return ErrCompanyNotFound
// (cross-tenant access is denied — PRD §6).
func (m *Manager) DB(companyCode string) (*sql.DB, error) {
	key := strings.ToUpper(strings.TrimSpace(companyCode))
	m.mu.RLock()
	pool, ok := m.pools[key]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrCompanyNotFound, companyCode)
	}
	return pool, nil
}

// ResolveCompanyByIMEI returns the company_code owning the IMEI via
// master.vehicle_imei_map. Results are cached in Redis when a cache is
// configured. Unknown IMEIs are rejected (anti-spoofing, PRD §3.2 / B1).
func (m *Manager) ResolveCompanyByIMEI(ctx context.Context, imei string) (string, error) {
	di, err := m.ResolveDeviceByIMEI(ctx, imei)
	if err != nil {
		return "", err
	}
	return di.CompanyCode, nil
}

// ResolveDeviceByIMEI resolves an IMEI to its owning company and vehicle id
// via master.vehicle_imei_map, caching the result in Redis when configured.
// Unknown IMEIs are rejected; every lookup/latency is recorded on the PRD §8.1
// tenant metrics (tenant_resolution_duration_ms, tenant_lookup_errors_total).
func (m *Manager) ResolveDeviceByIMEI(ctx context.Context, imei string) (DeviceInfo, error) {
	const op = "resolver.imei"
	start := time.Now()
	defer func() { m.observe(op, time.Since(start)) }()

	imei = strings.TrimSpace(imei)
	if imei == "" {
		m.metrics.incLookupErrors()
		return DeviceInfo{}, fmt.Errorf("tenant: empty imei")
	}

	key := m.cfg.CachePrefix + imei

	// 1) Redis cache hit.
	if m.cache != nil {
		di, ok := m.deviceInfoFromCache(ctx, key)
		if ok {
			if _, err := m.DB(di.CompanyCode); err != nil {
				// Cached company no longer exists → fall through to master.
				_ = m.cache.Del(ctx, key).Err()
			} else {
				return di, nil
			}
		}
	}

	// 2) Master lookup.
	var di DeviceInfo
	var vehicleID sql.NullInt64
	err := m.master.QueryRowContext(ctx,
		"SELECT company_code, vehicle_id FROM vehicle_imei_map WHERE imei = ?", imei).
		Scan(&di.CompanyCode, &vehicleID)
	if errors.Is(err, sql.ErrNoRows) {
		m.metrics.incLookupErrors()
		return DeviceInfo{}, fmt.Errorf("%w: %s", ErrIMEINotRegistered, imei)
	}
	if err != nil {
		m.metrics.incLookupErrors()
		return DeviceInfo{}, fmt.Errorf("tenant: resolve imei %s: %w", imei, err)
	}
	if vehicleID.Valid {
		di.VehicleID = vehicleID.Int64
	}

	// 3) Cache for the next lookups.
	if m.cache != nil {
		if payload, jerr := json.Marshal(di); jerr == nil {
			if serr := m.cache.Set(ctx, key, payload, m.cfg.CacheTTL).Err(); serr != nil {
				slog.Warn("tenant: caching imei mapping failed", "imei", imei, "error", serr)
			}
		} else {
			slog.Warn("tenant: encode imei mapping failed", "imei", imei, "error", jerr)
		}
	}
	return di, nil
}

// deviceInfoFromCache reads and decodes a cached DeviceInfo JSON value.
func (m *Manager) deviceInfoFromCache(ctx context.Context, key string) (DeviceInfo, bool) {
	val, err := m.cache.Get(ctx, key).Result()
	if err != nil || val == "" {
		return DeviceInfo{}, false
	}
	var di DeviceInfo
	if err := json.Unmarshal([]byte(val), &di); err != nil || di.CompanyCode == "" {
		return DeviceInfo{}, false
	}
	slog.Debug("tenant: IMEI cache hit", "key", key, "company", di.CompanyCode)
	return di, true
}

// Health pings master, default DB, and every warmed company pool. It returns
// one aggregated error describing every failure (used by /healthz).
func (m *Manager) Health(ctx context.Context) error {
	var errs []error

	if m.master != nil {
		if err := m.master.PingContext(ctx); err != nil {
			errs = append(errs, fmt.Errorf("master: %w", err))
		}
	}
	if m.defaultDB != nil {
		if err := m.defaultDB.PingContext(ctx); err != nil {
			errs = append(errs, fmt.Errorf("default: %w", err))
		}
	}
	m.mu.RLock()
	keys := make([]string, 0, len(m.pools))
	for k := range m.pools {
		keys = append(keys, k)
	}
	m.mu.RUnlock()

	for _, key := range keys {
		pool, _ := m.DB(key)
		if pool == nil {
			errs = append(errs, fmt.Errorf("company %s: pool missing", key))
			continue
		}
		if err := pool.PingContext(ctx); err != nil {
			errs = append(errs, fmt.Errorf("company %s: %w", key, err))
		}
	}

	if len(errs) > 0 {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		return fmt.Errorf("tenant health: %s", strings.Join(msgs, "; "))
	}
	return nil
}

// refreshMetrics updates gauges with per-pool connection statistics.
func (m *Manager) refreshMetrics() {
	if m.metrics == nil {
		return
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for key, pool := range m.pools {
		stats := pool.Stats()
		m.metrics.setCompany(key, 1, float64(stats.InUse), float64(stats.OpenConnections))
	}
}

// observe records a resolved/lookup latency observation (PRD §8.1).
func (m *Manager) observe(_ string, d time.Duration) {
	if m.metrics != nil {
		m.metrics.observeResolution(d)
	}
}

// Run periodically refreshes pool metrics until ctx is cancelled. Ketika
// read replica enabled (B4 HA), prober kesehatan replika juga dijalankan.
func (m *Manager) Run(ctx context.Context) {
	if m.cfg.ReplicaEnabled {
		go m.runReplicaProbes(ctx)
	}
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.refreshMetrics()
		}
	}
}

// Close closes every pool exactly once.
func (m *Manager) Close() {
	m.closedOnce.Do(func() {
		var err error
		if m.master != nil {
			err = m.master.Close()
		}
		m.mu.RLock()
		for _, pool := range m.pools {
			if e := pool.Close(); e != nil && err == nil {
				err = e
			}
		}
		for _, rep := range m.replicas { // B4 HA: READ REPLICA pools
			if e := rep.Close(); e != nil && err == nil {
				err = e
			}
		}
		m.mu.RUnlock()
		if m.defaultDB != nil {
			_ = m.defaultDB.Close()
		}
		if err != nil {
			slog.Warn("tenant: error closing pools", "error", err)
		}
	})
}
