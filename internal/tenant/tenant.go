package tenant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"adatrack_gps/internal"
)

// Errors returned by tenant resolution (anti-spoofing: FR-1.4).
var (
	// ErrIMEINotRegistered means the device is not in tm_vehicle_imei_map.
	ErrIMEINotRegistered = errors.New("tenant: IMEI not registered")
	// ErrCompanyNotFound means no pool/schema exists for the company code.
	ErrCompanyNotFound = errors.New("tenant: company not found")
)

// Cache is the optional IMEI→tenant cache (implemented by internal.RedisClient:
// Get(ctx, key) (string, error)). A nil Cache disables caching.
type Cache interface {
	Get(ctx context.Context, key string) (string, error)
}

// Company is a tenant row (master.tm_companies).
type Company struct {
	Code         string
	Name         string
	BusinessType string
	IsActive     bool
	Schema       string
}

// DeviceInfo is the IMEI→(tenant, vehicle) mapping from master.tm_vehicle_imei_map.
// VehicleID refers to tm_vehicles.id inside the company schema (logical ref).
type DeviceInfo struct {
	CompanyCode string `json:"company_code"`
	VehicleID   int64  `json:"vehicle_id"`
}

// Manager owns the master pool plus one pre-warmed pool per active company.
type Manager struct {
	baseCfg *internal.Config
	cfg     Config

	master    *internal.DBPool
	pools     map[string]*internal.DBPool // key: uppercase company code
	companies map[string]Company

	cache Cache
	mu    sync.RWMutex

	closedOnce sync.Once
}

// New connects to the master schema and pre-warms a pool per active company
// (≤50 tenants per PRD §4.2, so pre-warming is cheap and removes per-request
// pool creation from the hot path).
func New(ctx context.Context, baseCfg *internal.Config, cfg Config, cache Cache, reg prometheus.Registerer) (*Manager, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	RegisterMetrics(reg)
	m := &Manager{
		baseCfg:   baseCfg,
		cfg:       cfg,
		pools:     make(map[string]*internal.DBPool),
		companies: make(map[string]Company),
		cache:     cache,
	}

	master, err := internal.OpenPostgresPool(baseCfg, cfg.MasterSchema, "master")
	if err != nil {
		return nil, fmt.Errorf("tenant: open master pool: %w", err)
	}
	m.master = master

	if err := m.Refresh(ctx); err != nil {
		// Discovery failure is not fatal: routing simply has no companies yet and
		// /healthz reports it (no silent degradation).
		slog.Warn("tenant: company discovery failed", "error", err)
	}
	return m, nil
}

// Master exposes the master pool (auth + IMEI map + reference data).
func (m *Manager) Master() *internal.DBPool { return m.master }

// Config returns the effective tenant configuration.
func (m *Manager) Config() Config { return m.cfg }

// DB resolves a company code to its pool, opening it on demand when a tenant was
// provisioned after startup.
func (m *Manager) DB(companyCode string) (*internal.DBPool, error) {
	code := strings.ToUpper(strings.TrimSpace(companyCode))
	if code == "" {
		return nil, ErrCompanyNotFound
	}

	m.mu.RLock()
	pool, ok := m.pools[code]
	m.mu.RUnlock()
	if ok {
		return pool, nil
	}

	// Unknown code: re-discover once (covers tenants provisioned at runtime).
	if err := m.Refresh(context.Background()); err != nil {
		slog.Warn("tenant: refresh on cache miss failed", "company", code, "error", err)
	}
	m.mu.RLock()
	pool, ok = m.pools[code]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrCompanyNotFound, code)
	}
	return pool, nil
}

// Schema returns the schema name of a company ("" when unknown).
func (m *Manager) Schema(companyCode string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.companies[strings.ToUpper(strings.TrimSpace(companyCode))].Schema
}

// Refresh reloads the company registry from master and pre-warms new pools.
func (m *Manager) Refresh(ctx context.Context) error {
	rows, err := m.master.DB.QueryContext(ctx, `
		SELECT code, name, business_type, is_active
		FROM tm_companies
		WHERE deleted_at IS NULL
		ORDER BY code`)
	if err != nil {
		return fmt.Errorf("tenant: list companies: %w", err)
	}
	defer func() { _ = rows.Close() }()

	found := make(map[string]Company)
	for rows.Next() {
		var c Company
		if err := rows.Scan(&c.Code, &c.Name, &c.BusinessType, &c.IsActive); err != nil {
			return fmt.Errorf("tenant: scan company: %w", err)
		}
		c.Code = strings.ToUpper(c.Code)
		c.Schema = m.cfg.CompanySchema(c.Code)
		found[c.Code] = c
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("tenant: iterate companies: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.companies = found
	for code, c := range found {
		if !c.IsActive {
			continue
		}
		if _, ok := m.pools[code]; ok {
			continue
		}
		pool, err := internal.OpenPostgresPool(m.baseCfg, c.Schema, "company:"+c.Schema)
		if err != nil {
			// A missing schema for an active company is a real problem → report.
			slog.Warn("tenant: company pool unavailable", "company", code, "schema", c.Schema, "error", err)
			continue
		}
		m.pools[code] = pool
		slog.Info("tenant: company pool ready", "company", code, "schema", c.Schema)
	}
	companyPoolCount.Set(float64(len(m.pools)))
	return nil
}

// ResolveDeviceByIMEI maps an IMEI to its tenant + vehicle (FR-1.4). It is the
// anti-spoofing gate: unknown or disabled IMEIs are rejected and counted in
// tenant_lookup_errors_total. A Redis cache (when configured) short-circuits the
// master query; cache payloads are still validated before use.
func (m *Manager) ResolveDeviceByIMEI(ctx context.Context, imei string) (DeviceInfo, error) {
	imei = strings.TrimSpace(imei)
	if imei == "" {
		incLookupError()
		return DeviceInfo{}, fmt.Errorf("%w: empty IMEI", ErrIMEINotRegistered)
	}

	start := time.Now()
	defer func() { observeResolution(time.Since(start)) }()

	if m.cache != nil {
		if di, ok := m.deviceInfoFromCache(ctx, imei); ok {
			cacheHits.Inc()
			return di, nil
		}
		cacheMisses.Inc()
	}

	var di DeviceInfo
	var isActive bool
	err := m.master.DB.QueryRowContext(ctx, `
		SELECT company_code, COALESCE(vehicle_id, 0), is_active
		FROM tm_vehicle_imei_map
		WHERE imei = $1 AND deleted_at IS NULL`, imei).Scan(&di.CompanyCode, &di.VehicleID, &isActive)
	if err != nil {
		incLookupError()
		if errors.Is(err, sql.ErrNoRows) {
			return DeviceInfo{}, fmt.Errorf("%w: %s", ErrIMEINotRegistered, imei)
		}
		return DeviceInfo{}, fmt.Errorf("tenant: IMEI lookup failed for %s: %w", imei, err)
	}
	if !isActive {
		incLookupError()
		return DeviceInfo{}, fmt.Errorf("%w: IMEI %s disabled", ErrIMEINotRegistered, imei)
	}
	di.CompanyCode = strings.ToUpper(strings.TrimSpace(di.CompanyCode))

	m.cacheDeviceInfo(ctx, imei, di)
	return di, nil
}

// deviceInfoFromCache reads and validates a cached mapping.
func (m *Manager) deviceInfoFromCache(ctx context.Context, imei string) (DeviceInfo, bool) {
	val, err := m.cache.Get(ctx, m.cfg.CachePrefix+imei)
	if err != nil || val == "" {
		return DeviceInfo{}, false
	}
	var di DeviceInfo
	if err := json.Unmarshal([]byte(val), &di); err != nil || di.CompanyCode == "" {
		return DeviceInfo{}, false
	}
	return di, true
}

// cacheDeviceInfo stores a mapping best-effort (a cache failure never fails a
// lookup — caching is an optimisation only).
func (m *Manager) cacheDeviceInfo(ctx context.Context, imei string, di DeviceInfo) {
	type setter interface {
		Set(ctx context.Context, key string, value any, ttl time.Duration) error
	}
	s, ok := m.cache.(setter)
	if !ok || m.cfg.CacheTTL <= 0 {
		return
	}
	body, err := json.Marshal(di)
	if err != nil {
		return
	}
	if err := s.Set(ctx, m.cfg.CachePrefix+imei, string(body), m.cfg.CacheTTL); err != nil {
		slog.Debug("tenant: cache write failed", "imei", imei, "error", err)
	}
}

// Health pings the master schema and every pre-warmed company pool; the
// aggregated error drives /healthz (PRD §10.2 liveness).
func (m *Manager) Health(ctx context.Context) error {
	var errs []error
	if err := m.master.Ping(ctx); err != nil {
		errs = append(errs, fmt.Errorf("master: %w", err))
	}

	m.mu.RLock()
	names := make([]string, 0, len(m.pools))
	for name := range m.pools {
		names = append(names, name)
	}
	m.mu.RUnlock()

	for _, name := range names {
		pool, err := m.DB(name)
		if err != nil {
			errs = append(errs, fmt.Errorf("company %s: %w", name, err))
			continue
		}
		if perr := pool.Ping(ctx); perr != nil {
			errs = append(errs, fmt.Errorf("company %s: %w", name, perr))
		}
	}
	return errors.Join(errs...)
}

// Run periodically refreshes pool metrics until ctx ends.
func (m *Manager) Run(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.master.PublishPoolMetrics()
			m.mu.RLock()
			for _, p := range m.pools {
				p.PublishPoolMetrics()
			}
			m.mu.RUnlock()
		}
	}
}

// Close releases every pool exactly once.
func (m *Manager) Close() {
	m.closedOnce.Do(func() {
		if m.master != nil {
			_ = m.master.Close()
		}
		m.mu.RLock()
		defer m.mu.RUnlock()
		for _, p := range m.pools {
			_ = p.Close()
		}
	})
}
