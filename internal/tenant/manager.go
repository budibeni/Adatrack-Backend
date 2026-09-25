package tenant

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"backend/internal"
)

// Company represents a loaded tenant in the Manager.
type Company struct {
	Code         string
	Name         string
	BusinessType string
	IsActive     bool
	Schema       string
}

// Manager orchestrates multi-tenant routing, provisioning, and connection pools.
type Manager struct {
	cfg       Config
	baseCfg   *internal.Config
	master    *internal.DBPool
	
	mu        sync.RWMutex
	pools     map[string]*internal.DBPool
	companies map[string]Company
}

// NewManager initializes the tenant manager, loads the master schema pool,
// and pre-warms pools for all active companies.
func NewManager(ctx context.Context, baseCfg *internal.Config) (*Manager, error) {
	cfg := ConfigFromEnv(baseCfg)
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	masterPool, err := internal.OpenPostgresPool(baseCfg, cfg.MasterSchema, "master")
	if err != nil {
		return nil, fmt.Errorf("tenant: failed to open master pool: %w", err)
	}

	m := &Manager{
		cfg:       cfg,
		baseCfg:   baseCfg,
		master:    masterPool,
		pools:     make(map[string]*internal.DBPool),
		companies: make(map[string]Company),
	}

	if err := m.warmPools(ctx); err != nil {
		slog.Warn("tenant: partial pre-warm failure", "err", err)
	}

	return m, nil
}

// warmPools queries the master registry for active companies and opens a DBPool
// for each one.
func (m *Manager) warmPools(ctx context.Context) error {
	rows, err := m.master.DB.QueryContext(ctx, "SELECT code, name, business_type FROM tm_companies WHERE is_active = TRUE AND deleted_at IS NULL")
	if err != nil {
		return fmt.Errorf("query tm_companies: %w", err)
	}
	defer rows.Close()

	var errs []error
	for rows.Next() {
		var code, name, businessType string
		if err := rows.Scan(&code, &name, &businessType); err != nil {
			errs = append(errs, err)
			continue
		}

		schema := m.cfg.CompanySchema(code)
		pool, err := internal.OpenPostgresPool(m.baseCfg, schema, "company:"+schema)
		if err != nil {
			errs = append(errs, fmt.Errorf("pool %s: %w", code, err))
			continue
		}

		m.mu.Lock()
		m.pools[code] = pool
		m.companies[code] = Company{
			Code:         code,
			Name:         name,
			BusinessType: businessType,
			IsActive:     true,
			Schema:       schema,
		}
		m.mu.Unlock()
	}
	
	if len(errs) > 0 {
		return fmt.Errorf("%d warm errors: %v", len(errs), errs[0])
	}
	return nil
}

// GetPool returns the DBPool for a specific company code if active.
func (m *Manager) GetPool(code string) (*internal.DBPool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	pool, ok := m.pools[code]
	if !ok {
		return nil, fmt.Errorf("tenant: company %q not found or inactive", code)
	}
	return pool, nil
}

// Master returns the master DBPool.
func (m *Manager) Master() *internal.DBPool {
	return m.master
}

// Close gracefully closes all database pools.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	if m.master != nil && m.master.DB != nil {
		m.master.DB.Close()
	}
	for _, pool := range m.pools {
		if pool != nil && pool.DB != nil {
			pool.DB.Close()
		}
	}
}
