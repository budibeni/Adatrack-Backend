package tenant

import (
	"context"
	"fmt"
	"sync"
	"time"

	"backend/internal/config"
	"backend/internal/dbclient"
	"backend/internal/logger"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CircuitBreakerState int

const (
	StateClosed CircuitBreakerState = iota
	StateOpen
	StateHalfOpen
)

type TenantPool struct {
	companyCode string
	ReadPool    *pgxpool.Pool
	failCount   int
	state       CircuitBreakerState
	lastFailed  time.Time
	mu          sync.RWMutex
}

var (
	managerMu sync.RWMutex
	pools     map[string]*TenantPool
	globalCfg *config.Config
)

func InitManager(cfg *config.Config) {
	globalCfg = cfg
	pools = make(map[string]*TenantPool)
	go prober()
}

func prober() {
	ticker := time.NewTicker(30 * time.Second)
	for range ticker.C {
		managerMu.RLock()
		for code, p := range pools {
			p.mu.Lock()
			if p.state == StateOpen && time.Since(p.lastFailed) >= 30*time.Second {
				p.state = StateHalfOpen
				logger.Log.Info("Circuit breaker half-open", "company", code)
			}
			p.mu.Unlock()
		}
		managerMu.RUnlock()
	}
}

func getTenantPool(ctx context.Context, companyCode string) (*TenantPool, error) {
	managerMu.RLock()
	p, ok := pools[companyCode]
	managerMu.RUnlock()
	if ok {
		return p, nil
	}

	managerMu.Lock()
	defer managerMu.Unlock()
	p, ok = pools[companyCode]
	if ok {
		return p, nil
	}

	// Assuming read replica is configured via DBHost + "-replica" or we just fallback to primary
	// In local, it might fail to connect to replica, so we just use the primary config
	// The PRD mentions per-tenant warm pool
	dsn := fmt.Sprintf("postgres://%s:%s@%s-replica:%s/%s?sslmode=disable",
		globalCfg.DBUser, globalCfg.DBPass, globalCfg.DBHost, globalCfg.DBPort, globalCfg.DBName)
	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	poolConfig.MaxConns = 10
	poolConfig.MinConns = 1

	rp, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		logger.Log.Warn("Failed to create read pool, will fallback to primary", "company", companyCode, "err", err)
	}

	tp := &TenantPool{
		companyCode: companyCode,
		ReadPool:    rp,
		state:       StateClosed,
	}
	pools[companyCode] = tp
	return tp, nil
}

type ReadRouter struct {
	companyCode string
}

func NewReadRouter(companyCode string) *ReadRouter {
	return &ReadRouter{companyCode: companyCode}
}

func (r *ReadRouter) reportSuccess(p *TenantPool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state == StateHalfOpen {
		p.state = StateClosed
		p.failCount = 0
		logger.Log.Info("Circuit breaker closed", "company", r.companyCode)
	} else if p.state == StateClosed {
		p.failCount = 0
	}
}

func (r *ReadRouter) reportFailure(p *TenantPool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state == StateHalfOpen {
		p.state = StateOpen
		p.lastFailed = time.Now()
		logger.Log.Warn("Circuit breaker open from half-open", "company", r.companyCode)
	} else if p.state == StateClosed {
		p.failCount++
		if p.failCount >= 3 {
			p.state = StateOpen
			p.lastFailed = time.Now()
			logger.Log.Warn("Circuit breaker open", "company", r.companyCode)
		}
	}
}

func (r *ReadRouter) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if globalCfg == nil || r.companyCode == "" {
		return dbclient.Pool.Query(ctx, sql, args...)
	}
	
	p, err := getTenantPool(ctx, r.companyCode)
	if err == nil && p.ReadPool != nil {
		p.mu.RLock()
		state := p.state
		p.mu.RUnlock()
		
		if state == StateClosed || state == StateHalfOpen {
			rows, err := p.ReadPool.Query(ctx, sql, args...)
			if err != nil {
				r.reportFailure(p)
				// Fallback to primary
				return dbclient.Pool.Query(ctx, sql, args...)
			}
			r.reportSuccess(p)
			return rows, nil
		}
	}
	
	// Fallback to primary
	return dbclient.Pool.Query(ctx, sql, args...)
}

func (r *ReadRouter) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if globalCfg == nil || r.companyCode == "" {
		return dbclient.Pool.QueryRow(ctx, sql, args...)
	}

	p, err := getTenantPool(ctx, r.companyCode)
	if err == nil && p.ReadPool != nil {
		p.mu.RLock()
		state := p.state
		p.mu.RUnlock()

		if state == StateClosed || state == StateHalfOpen {
			return p.ReadPool.QueryRow(ctx, sql, args...)
		}
	}
	return dbclient.Pool.QueryRow(ctx, sql, args...)
}
