// Read/write split (PRD §13): route read queries to the per-tenant standby with a
// one-shot fallback to the primary, guarded by a per-tenant circuit breaker.
//
// Design notes:
//   - DISABLED unless POSTGRES_REPLICA_HOST is set — with no replica every read
//     keeps using the primary, i.e. exactly the pre-B4 behaviour.
//   - Writes NEVER travel this path: callers keep using Manager.DB()/Master(), so
//     a mis-routed statement cannot reach a standby (which would fail at best and
//     diverge at worst).
//   - ReadQueryRow executes eagerly: a *sql.Row cannot be re-executed, so the
//     fallback needs the row in hand before it is handed to the caller.
package tenant

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"adatrack_gps/internal"
)

// Route label values of db_read_queries_total (PRD §13).
const (
	RouteReplica = "replica"
	RoutePrimary = "primary"
)

// Replica breaker tuning: 3 failures open the breaker for 30 s, after which a
// single read is allowed through (half-open) and a success closes it again.
const (
	replicaFailThreshold = 3
	replicaOpenFor       = 30 * time.Second
)

// Row is the subset of *sql.Row the read router returns. An interface (instead of
// *sql.Row) is required because *sql.Row can neither be re-executed nor built by
// hand, and the split needs to retry the read on the primary.
type Row interface{ Scan(dest ...any) error }

// replicaReads is the per-company replica pool plus its breaker state.
type replicaReads struct {
	pool *internal.DBPool

	mu       sync.Mutex
	failures int
	openTill time.Time
}

// ReplicaEnabled reports whether a read replica is configured
// (POSTGRES_REPLICA_HOST). When false, every read stays on the primary.
func (m *Manager) ReplicaEnabled() bool { return m.baseCfg.Postgres.Replica.Enabled() }

// ReadRoute reports where a read for a company would go right now (metrics and
// tests): RouteReplica when the split applies — replica configured AND its
// breaker closed — else RoutePrimary. It does not depend on whether the replica
// pool has already been opened (opening is lazy).
func (m *Manager) ReadRoute(companyCode string) string {
	if m.replicaAvailable(strings.ToUpper(strings.TrimSpace(companyCode))) {
		return RouteReplica
	}
	return RoutePrimary
}

// ReadQuery executes a read for one company using the split routing: the replica
// first (when configured and its breaker allows it), the primary on the first
// error. The statement MUST be read-only — writes keep using DB().
func (m *Manager) ReadQuery(ctx context.Context, companyCode, query string, args ...any) (*sql.Rows, error) {
	rows, _, err := m.readQuery(ctx, companyCode, query, args...)
	return rows, err
}

// ReadQueryRow is ReadQuery for a single row. Scan reports sql.ErrNoRows exactly
// like *sql.Row does.
func (m *Manager) ReadQueryRow(ctx context.Context, companyCode, query string, args ...any) Row {
	rows, _, err := m.readQuery(ctx, companyCode, query, args...)
	if err != nil {
		return errRow{err: err}
	}
	return &bufferedRow{rows: rows}
}

// readQuery is the shared implementation; it also returns the route that served
// the read so tests (and future tooling) can assert the split without scraping
// metrics.
func (m *Manager) readQuery(ctx context.Context, companyCode, query string, args ...any) (*sql.Rows, string, error) {
	code := strings.ToUpper(strings.TrimSpace(companyCode))
	if code == "" {
		return nil, "", ErrCompanyNotFound
	}

	if m.replicaAvailable(code) {
		pool, err := m.replicaPool(code)
		switch {
		case err != nil:
			// Could not open the standby: record the failure and use the primary.
			m.recordReplicaResult(code, err)
			slog.Debug("tenant: replica pool unavailable, using primary", "company", code, "error", err)
		case pool != nil:
			rows, rerr := pool.DB.QueryContext(ctx, query, args...)
			if rerr == nil {
				m.recordReplicaResult(code, nil)
				observeRead(code, RouteReplica)
				return rows, RouteReplica, nil
			}
			// One-shot fallback: a replica blip must never fail a read.
			m.recordReplicaResult(code, rerr)
			dbReplicaFallbacks.Inc()
			slog.Debug("tenant: replica read failed, falling back to primary", "company", code, "error", rerr)
		}
	}

	primary, err := m.DB(code)
	if err != nil {
		return nil, "", err
	}
	rows, perr := primary.DB.QueryContext(ctx, query, args...)
	if perr != nil {
		return nil, RoutePrimary, fmt.Errorf("tenant: read query (primary): %w", perr)
	}
	observeRead(code, RoutePrimary)
	return rows, RoutePrimary, nil
}

// replicaState returns (creating on first use) the breaker state of a company.
func (m *Manager) replicaState(code string) *replicaReads {
	m.readMu.Lock()
	defer m.readMu.Unlock()
	st := m.reads[code]
	if st == nil {
		st = &replicaReads{}
		m.reads[code] = st
	}
	return st
}

// replicaAvailable reports whether the breaker lets a replica read through right
// now: closed, or half-open after the open window elapsed. It is deliberately
// independent of pool state so routing decisions do not depend on whether the
// standby connection has been opened yet.
func (m *Manager) replicaAvailable(code string) bool {
	if !m.ReplicaEnabled() {
		return false
	}
	st := m.replicaState(code)
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.openTill.IsZero() || time.Now().After(st.openTill)
}

// replicaPool opens (once) the replica pool for a company schema. It returns
// nil, nil when the read/write split is disabled.
func (m *Manager) replicaPool(code string) (*internal.DBPool, error) {
	if !m.ReplicaEnabled() {
		return nil, nil
	}

	st := m.replicaState(code)
	st.mu.Lock()
	if st.pool != nil {
		pool := st.pool
		st.mu.Unlock()
		return pool, nil
	}
	st.mu.Unlock()

	schema := m.Schema(code)
	if schema == "" {
		// Company provisioned after startup: derive the schema like Refresh does.
		schema = m.cfg.CompanySchema(code)
	}
	pool, err := internal.OpenPostgresPoolDSN(m.baseCfg, m.baseCfg.PostgresReplicaDSN(schema), "replica:"+strings.ToLower(code))
	if err != nil {
		return nil, err
	}

	st.mu.Lock()
	st.pool = pool
	st.mu.Unlock()
	dbReplicaUp.WithLabelValues(code).Set(1)
	slog.Info("tenant: read replica pool ready", "company", code, "schema", schema)
	return pool, nil
}

// recordReplicaResult drives the breaker and the db_replica_up gauge.
func (m *Manager) recordReplicaResult(code string, err error) {
	m.readMu.RLock()
	st := m.reads[code]
	m.readMu.RUnlock()
	if st == nil {
		return
	}
	st.mu.Lock()
	if err == nil {
		st.failures = 0
		st.openTill = time.Time{}
		st.mu.Unlock()
		dbReplicaUp.WithLabelValues(code).Set(1)
		return
	}
	st.failures++
	if st.failures >= replicaFailThreshold {
		st.openTill = time.Now().Add(replicaOpenFor)
	}
	st.mu.Unlock()
	dbReplicaUp.WithLabelValues(code).Set(0)
}

// probeReplicas opens (first time) and pings the replica pool of every known
// company, so db_replica_up is accurate within one tick and an open breaker can
// close without waiting for user traffic (PRD §13 "prober berkala").
func (m *Manager) probeReplicas(ctx context.Context) {
	if !m.ReplicaEnabled() {
		return
	}

	codes := make(map[string]struct{})
	m.mu.RLock()
	for code := range m.companies {
		codes[code] = struct{}{}
	}
	m.mu.RUnlock()
	m.readMu.RLock()
	for code := range m.reads { // companies opened lazily at runtime
		codes[code] = struct{}{}
	}
	m.readMu.RUnlock()

	for code := range codes {
		pool, err := m.replicaPool(code)
		if err != nil {
			m.recordReplicaResult(code, err)
			slog.Warn("tenant: read replica pool unavailable", "company", code, "error", err)
			continue
		}
		if pool == nil {
			continue
		}
		pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		perr := pool.Ping(pctx)
		cancel()
		if perr != nil {
			m.recordReplicaResult(code, perr)
			slog.Warn("tenant: replica probe failed", "company", code, "error", perr)
			continue
		}
		m.recordReplicaResult(code, nil)
		pool.PublishPoolMetrics()
	}
}

// closeReplicas releases every replica pool (called from Manager.Close).
func (m *Manager) closeReplicas() {
	m.readMu.Lock()
	states := make(map[string]*replicaReads, len(m.reads))
	for code, st := range m.reads {
		states[code] = st
	}
	m.readMu.Unlock()

	for code, st := range states {
		if st == nil {
			continue
		}
		st.mu.Lock()
		pool := st.pool
		st.pool = nil
		st.mu.Unlock()
		if pool != nil {
			_ = pool.Close()
		}
		dbReplicaUp.WithLabelValues(code).Set(0)
	}
}

// observeRead counts a read by route.
func observeRead(companyCode, route string) {
	dbReadQueries.WithLabelValues(companyCode, route).Inc()
}

// errRow is a Row that always reports err (from a failed eager query).
type errRow struct{ err error }

// Scan implements Row.
func (e errRow) Scan(...any) error { return e.err }

// bufferedRow adapts *sql.Rows to the single-row contract of *sql.Row.
type bufferedRow struct{ rows *sql.Rows }

// Scan implements Row with *sql.Row semantics (ErrNoRows when empty).
func (b *bufferedRow) Scan(dest ...any) error {
	defer func() { _ = b.rows.Close() }()
	if !b.rows.Next() {
		if err := b.rows.Err(); err != nil {
			return err
		}
		return sql.ErrNoRows
	}
	return b.rows.Scan(dest...)
}

// ErrReadUnavailable is reported when neither the replica nor the primary could
// serve a read (both pools unreachable).
var ErrReadUnavailable = errors.New("tenant: no database route available for read")
