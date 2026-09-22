package internal

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // pgx database/sql driver (PostgreSQL-only, PRD §7.1)
)

// DBPool wraps a *sql.DB (pgx stdlib) with the pool name used for metrics.
type DBPool struct {
	DB   *sql.DB
	Name string
}

// OpenPostgresPool opens a PostgreSQL pool for one schema (search_path is baked
// into the DSN) using the PRD §7.1 pool sizing: Min 20 / Max 50 / conn lifetime
// 5 min / statement timeout 30 s.
func OpenPostgresPool(cfg *Config, schema, poolName string) (*DBPool, error) {
	return OpenPostgresPoolDSN(cfg, cfg.PostgresDSN(schema), poolName)
}

// OpenPostgresPoolDSN opens a pool from an explicit DSN with the same sizing as
// OpenPostgresPool. Used by the optional read replica (PRD §13), whose URL points
// at a different host than the primary and therefore cannot be derived from
// cfg.PostgresDSN.
func OpenPostgresPoolDSN(cfg *Config, dsn, poolName string) (*DBPool, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres pool %s: %w", poolName, err)
	}
	db.SetMaxOpenConns(cfg.Postgres.PoolMax)
	db.SetMaxIdleConns(cfg.Postgres.PoolMin)
	db.SetConnMaxLifetime(cfg.Postgres.ConnMaxLifetime)
	db.SetConnMaxIdleTime(cfg.Postgres.ConnMaxLifetime)

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Postgres.ConnectTimeout)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping postgres pool %s: %w", poolName, err)
	}
	return &DBPool{DB: db, Name: poolName}, nil
}

// Close closes the pool.
func (p *DBPool) Close() error {
	if p == nil || p.DB == nil {
		return nil
	}
	return p.DB.Close()
}

// PublishPoolMetrics refreshes the pool gauges (called periodically by services).
func (p *DBPool) PublishPoolMetrics() {
	if p == nil || p.DB == nil || PGPoolInUse == nil {
		return
	}
	st := p.DB.Stats()
	PGPoolInUse.WithLabelValues(p.Name).Set(float64(st.InUse))
	PGPoolOpen.WithLabelValues(p.Name).Set(float64(st.OpenConnections))
}

// Ping checks connectivity (readiness probe).
func (p *DBPool) Ping(ctx context.Context) error {
	if p == nil || p.DB == nil {
		return fmt.Errorf("pool %s not initialised", p.Name)
	}
	return p.DB.PingContext(ctx)
}

// BatchInsert executes a multi-row INSERT built from `columns` and `rows`
// (every value is a bound parameter — PRD §9.6 "100% parameterized"). It returns
// the number of rows written. `UNNEST`-free plain VALUES keeps the statement
// predictable for pgx's prepared-statement cache.
//
// Example:
//
//	BatchInsert(ctx, pool, "th_telemetry_logs",
//	    []string{"imei", "latitude"}, [][]any{{"86001", -6.2}})
func BatchInsert(ctx context.Context, pool *DBPool, table string, columns []string, rows [][]any) (int64, error) {
	// An empty batch is a no-op regardless of pool state.
	if len(rows) == 0 {
		return 0, nil
	}
	if pool == nil || pool.DB == nil {
		return 0, fmt.Errorf("pool not initialised")
	}
	if len(columns) == 0 {
		return 0, fmt.Errorf("batch insert %s: no columns", table)
	}

	if PGInsertDuration != nil {
		start := time.Now()
		defer func() {
			PGInsertDuration.WithLabelValues(table).Observe(time.Since(start).Seconds())
		}()
	}

	quoted := make([]string, len(columns))
	for i, c := range columns {
		quoted[i] = QuoteIdent(c)
	}

	args := make([]any, 0, len(rows)*len(columns))
	var sb strings.Builder
	sb.WriteString("INSERT INTO ")
	sb.WriteString(QuoteIdent(table))
	sb.WriteString(" (")
	sb.WriteString(strings.Join(quoted, ", "))
	sb.WriteString(") VALUES ")

	n := 1
	for r, row := range rows {
		if len(row) != len(columns) {
			return 0, fmt.Errorf("batch insert %s: row %d has %d values, want %d",
				table, r, len(row), len(columns))
		}
		if r > 0 {
			sb.WriteString(", ")
		}
		sb.WriteByte('(')
		for i := range row {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString("$")
			sb.WriteString(strconv.Itoa(n))
			n++
			args = append(args, row[i])
		}
		sb.WriteByte(')')
	}

	res, err := pool.DB.ExecContext(ctx, sb.String(), args...)
	if err != nil {
		if PGInsertErrors != nil {
			PGInsertErrors.WithLabelValues(table).Inc()
		}
		return 0, fmt.Errorf("batch insert %s (%d rows): %w", table, len(rows), err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, nil // driver without row counts: statement succeeded
	}
	return affected, nil
}

// QuoteIdent double-quotes a PostgreSQL identifier (schema/table/column names),
// escaping embedded quotes. Identifiers are never sourced from user input.
func QuoteIdent(ident string) string {
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}

// IsTransientError classifies database errors that are worth retrying
// (FR-3.4 step 5: NACK + backoff on transient failures only).
func IsTransientError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, needle := range []string{
		"connection refused", "connection reset", "broken pipe",
		"timeout", "timed out", "too many connections", "deadlock",
		"server closed the connection", "cannot allocate memory",
		"the database system is starting up", "the database system is in recovery mode",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

// RetryWithBackoff runs fn with the configured exponential backoff schedule
// (RETRY_BACKOFF_MS, default 1s/5s/10s, FR-4.3). It retries only transient
// errors and returns the last error when the schedule is exhausted.
func RetryWithBackoff(ctx context.Context, backoff []time.Duration, maxRetries int, fn func(ctx context.Context) error) error {
	if len(backoff) == 0 {
		backoff = []time.Duration{time.Second, 5 * time.Second, 10 * time.Second}
	}
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		lastErr = fn(ctx)
		if lastErr == nil {
			return nil
		}
		if !IsTransientError(lastErr) || attempt == maxRetries {
			return lastErr
		}
		delay := backoff[min(attempt, len(backoff)-1)]
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w (context: %v)", lastErr, ctx.Err())
		case <-time.After(delay):
		}
	}
	return lastErr
}
