// Package tenant — read/write split (B4 HA, PRD §7.1.1, keputusan READ
// REPLICA 2026-08-25).
//
// Replika database melayani pembacaan (skala baca: riwayat/laporan/list);
// PRIMARY tetap satu-satunya jalur TULIS. Router memilih replica hanya ketika
// sehat (breaker tertutup). Kegagalan query pada replica:
//  1. fallback SATU kali ke primary untuk request tersebut, dan
//  2. breaker mencatat failure → setelah N kegagalan beruntun trafik baca
//     langsung ke primary selama cooldown (half-open sesudahnya).
//
// Semua keputusan routing ter-metric (db_read_queries_total{route}) dan
// kesehatan replica ter-ping berkala oleh prober dari Run() (db_replica_up).
// Tidak ada silent drop: tiap fallback ter-log warn.
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
)

var (
	// ErrNoPrimary is returned when a router has no usable primary pool.
	ErrNoPrimary = errors.New("tenant: router has no primary pool")

	// Routing decision labels (metric db_read_queries_total.route).
	routeReplica         = "replica"
	routePrimary         = "primary"
	routePrimaryFallback = "primary_fallback"
)

const (
	// replicaTripThreshold: kegagalan beruntun sebelum breaker membuka.
	replicaTripThreshold = 3

	// defaultReplicaCooldown: lama breaker terbuka sebelum half-open mencoba
	// replica lagi. Prober ping mempercepat indikator gauge pulih.
	defaultReplicaCooldown = 30 * time.Second
)

// breakerState adalah circuit-breaker ringkas per tenant key.
type breakerState struct {
	mu       sync.Mutex
	failures int
	openedAt time.Time
	nowFn    func() time.Time // injectable clock (test); nil = time.Now
}

// clock returns the breaker's time source.
func (b *breakerState) clock() time.Time {
	if b != nil && b.nowFn != nil {
		return b.nowFn()
	}
	return time.Now()
}

// allows reports whether the replica may receive traffic right now. Ketika
// breaker terbuka dan cooldown sudah lewat, satu slot half-open dibuka
// (counter diset threshold-1 sehingga satu failure langsung menutup lagi).
func (b *breakerState) allows(now time.Time) bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failures < replicaTripThreshold {
		return true
	}
	if now.Sub(b.openedAt) >= defaultReplicaCooldown {
		b.failures = replicaTripThreshold - 1 // half-open: satu percobaan
		return true
	}
	return false
}

func (b *breakerState) recordSuccess() {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.failures = 0
	b.mu.Unlock()
}

func (b *breakerState) recordFailure() {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.failures++
	if b.failures == replicaTripThreshold {
		b.openedAt = b.clock()
	}
	b.mu.Unlock()
}

// failuresCount returns the current consecutive-failure count (prober).
func (b *breakerState) failuresCount() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.failures
}

// ReadRouter routes READ queries to the read replica with automatic one-shot
// fallback to the primary; WRITE queries ALWAYS hit the primary. Obtain one
// via Manager.ReadRouter, or NewSingleRouter untuk pemakaian primary-only
// (unit test, replica disabled). Aman dipakai konkuren.
type ReadRouter struct {
	key     string
	primary *sql.DB
	replica *sql.DB // nil = disabled / tidak tersedia
	brk     *breakerState
	tm      *Manager // optional: shared breaker registry + metrics (nil di test)

	now func() time.Time // injectable clock for unit tests
}

// NewSingleRouter wraps ONLY a primary pool (replica disabled). Dipakai juga
// sebagai fallback ketika manager-level router tidak tersedia.
func NewSingleRouter(primary *sql.DB) *ReadRouter {
	return &ReadRouter{key: "-", primary: primary, now: time.Now}
}

// ReadRouter resolves the company pools into a read/write routing pair.
func (m *Manager) ReadRouter(companyCode string) (*ReadRouter, error) {
	key := strings.ToUpper(strings.TrimSpace(companyCode))
	m.mu.RLock()
	prim, hasPrim := m.pools[key]
	rep := m.replicas[key]
	m.mu.RUnlock()
	if !hasPrim || prim == nil {
		return nil, fmt.Errorf("%w: %s", ErrCompanyNotFound, companyCode)
	}
	if !m.cfg.ReplicaEnabled {
		rep = nil
	}
	return &ReadRouter{
		key:     key,
		primary: prim,
		replica: rep,
		brk:     m.breakerFor(key),
		tm:      m,
		now:     time.Now,
	}, nil
}

// Primary exposes the write-path pool (INSERT/UPDATE/DELETE authority).
func (r *ReadRouter) Primary() *sql.DB {
	if r == nil {
		return nil
	}
	return r.primary
}

// useReplica reports whether this call may target the replica.
func (r *ReadRouter) useReplica() bool {
	if r == nil || r.replica == nil || r.primary == nil {
		return false
	}
	return r.brk.allows(r.now())
}

// note records the routing outcome: metric counter + breaker transitions +
// warn log on fallback (no silent drop).
func (r *ReadRouter) note(route string, qerr error) {
	if r == nil {
		return
	}
	switch {
	case qerr != nil:
		slog.Warn("tenant: replica read failed, falling back to primary",
			"company", r.key, "error", qerr)
		r.brk.recordFailure()
	case route == routeReplica:
		r.brk.recordSuccess()
	}
	if r != nil && r.tm != nil && r.tm.metrics != nil {
		r.tm.metrics.incReadRoute(r.key, route)
	}
}

// QueryContext runs a SELECT preferring the replica. On replica failure it
// falls back once to the primary (and trips the breaker).
func (r *ReadRouter) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if r == nil || r.primary == nil {
		return nil, ErrNoPrimary
	}
	if r.useReplica() {
		rows, err := r.replica.QueryContext(ctx, query, args...)
		if err == nil {
			r.note(routeReplica, nil)
			return rows, nil
		}
		r.note(routePrimaryFallback, err)
		return r.primary.QueryContext(ctx, query, args...)
	}
	r.note(routePrimary, nil)
	return r.primary.QueryContext(ctx, query, args...)
}

// Query is the context-free variant of QueryContext.
func (r *ReadRouter) Query(query string, args ...any) (*sql.Rows, error) {
	return r.QueryContext(context.Background(), query, args...)
}

// Row is an eagerly-materialised single-row result (database/sql *sql.Row
// tidak bisa dikonstruksi di luar paket). Semantik error mengikuti
// sql.Row.Scan termasuk sql.ErrNoRows untuk hasil kosong.
type Row struct {
	rows *sql.Rows
	err  error
}

// Scan copies the columns of the first row into dest.
func (row *Row) Scan(dest ...any) error {
	if row == nil || row.rows == nil {
		if row != nil && row.err != nil {
			return row.err
		}
		return sql.ErrNoRows
	}
	defer row.rows.Close()
	if row.err != nil {
		return row.err
	}
	if !row.rows.Next() {
		if e := row.rows.Err(); e != nil {
			return e
		}
		return sql.ErrNoRows
	}
	return row.rows.Scan(dest...)
}

// QueryRowContext runs a single-row read preferring the replica. Unlike
// database/sql, connection errors surface eagerly through Row.Scan karena
// query dieksekusi langsung (sehingga fallback primary di atas aktif).
func (r *ReadRouter) QueryRowContext(ctx context.Context, query string, args ...any) *Row {
	rows, err := r.QueryContext(ctx, query, args...)
	return &Row{rows: rows, err: err}
}

// QueryRow is the context-free variant of QueryRowContext.
func (r *ReadRouter) QueryRow(query string, args ...any) *Row {
	return r.QueryRowContext(context.Background(), query, args...)
}

// Exec runs a WRITE against the PRIMARY pool — always, no exceptions
// (replika read-only di sisi engine maupun kontrak aplikasi ini).
func (r *ReadRouter) Exec(query string, args ...any) (sql.Result, error) {
	return r.ExecContext(context.Background(), query, args...)
}

// ExecContext is the context-aware variant of Exec.
func (r *ReadRouter) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if r == nil || r.primary == nil {
		return nil, ErrNoPrimary
	}
	return r.primary.ExecContext(ctx, query, args...)
}

// ---------------------------------------------------------------------------
// Pool selection (Manager level) + health probing
// ---------------------------------------------------------------------------

// breakerFor returns the shared breaker for a tenant key (lazily created).
func (m *Manager) breakerFor(key string) *breakerState {
	m.mu.RLock()
	br, ok := m.breakers[key]
	m.mu.RUnlock()
	if ok && br != nil {
		return br
	}
	br = &breakerState{}
	m.mu.Lock()
	if existing, exists := m.breakers[key]; exists && existing != nil {
		br = existing
	} else {
		m.breakers[key] = br
	}
	m.mu.Unlock()
	return br
}

// ReadPool resolves the preferred READ *sql.DB for a company: the replica
// when enabled & healthy, otherwise the PRIMARY (fallback). Dipakai handler
// GET (pool-level selection); untuk fallback tingkat query pakai ReadRouter.
func (m *Manager) ReadPool(companyCode string) (*sql.DB, error) {
	key := strings.ToUpper(strings.TrimSpace(companyCode))
	m.mu.RLock()
	prim, hasPrim := m.pools[key]
	rep := m.replicas[key]
	m.mu.RUnlock()
	if !hasPrim || prim == nil {
		return nil, fmt.Errorf("%w: %s", ErrCompanyNotFound, companyCode)
	}

	route := routePrimary
	db := prim
	if rep != nil && m.cfg.ReplicaEnabled && m.breakerFor(key).allows(time.Now()) {
		db = rep
		route = routeReplica
	}
	if m.metrics != nil {
		m.metrics.incReadRoute(key, route)
	}
	return db, nil
}

// openPoolOnce opens a pool with a SINGLE reachability check (no retry storm)
// — dipakai untuk pool replica yang best-effort: kegagalan warm-up hanya
// menonaktifkan replika tenant tsb (fallback primary), bukan startup service.
func (m *Manager) openPoolOnce(dsn string) (*sql.DB, error) {
	db, err := sql.Open(m.cfg.DriverName(), dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(m.cfg.ReplicaPoolMax)
	db.SetMaxIdleConns(m.cfg.ReplicaPoolMin)
	db.SetConnMaxLifetime(m.cfg.ConnMaxLifetime)

	ctx, cancel := context.WithTimeout(context.Background(), m.cfg.ConnectTimeout)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// warmReplica opens + registers the read-replica pool for a tenant key
// (best-effort; dipanggil dari loadCompanies/ProvisionCompany).
func (m *Manager) warmReplica(key, dbName string) {
	if !m.cfg.ReplicaEnabled {
		return
	}
	rep, err := m.openPoolOnce(m.cfg.ReplicaDSN(dbName))
	if err != nil {
		slog.Warn("tenant: read REPLICA unavailable (reads fall back to primary)",
			"company", key, "db", dbName, "error", err)
		return
	}
	m.mu.Lock()
	m.replicas[key] = rep
	m.mu.Unlock()
	host, port := m.cfg.ReplicaEndpoint()
	slog.Info("tenant: company READ REPLICA pool warmed",
		"company", key, "db", dbName, "endpoint", host+":"+port)
}

// runReplicaProbes periodically pings every warmed replica pool and updates
// the per-tenant breaker plus the db_replica_up gauge. Dijalankan sebagai
// goroutine dari Run() ketika replica enabled.
func (m *Manager) runReplicaProbes(ctx context.Context) {
	interval := m.cfg.ProbeInterval
	if interval <= 0 {
		interval = 10 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.mu.RLock()
			snapshot := make(map[string]*sql.DB, len(m.replicas))
			for k, v := range m.replicas {
				snapshot[k] = v
			}
			m.mu.RUnlock()

			for key, rep := range snapshot {
				pctx, cancel := context.WithTimeout(ctx, interval)
				err := rep.PingContext(pctx)
				cancel()

				br := m.breakerFor(key)
				before := br.failuresCount()
				if err != nil {
					br.recordFailure()
					if m.metrics != nil {
						m.metrics.setReplicaUp(key, false)
					}
					slog.Warn("tenant: replica probe FAILED",
						"company", key,
						"consecutive_failures", br.failuresCount(), "error", err)
					continue
				}
				if before > 0 {
					br.recordSuccess()
					slog.Info("tenant: replica probe RECOVERED", "company", key)
				}
				if m.metrics != nil {
					m.metrics.setReplicaUp(key, true)
				}
			}
		}
	}
}