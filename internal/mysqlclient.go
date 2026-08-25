package internal

import (
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/prometheus/client_golang/prometheus"

	"ajb_gps/internal/dialect"
)

// MySQLClient wraps a sql.DB connection pool with metrics.
// Provides connection pooling, retry logic, and Prometheus metrics.
type MySQLClient struct {
	db             *sql.DB
	config         *Config
	insertDuration *prometheus.HistogramVec
	insertErrors   *prometheus.CounterVec
}

// NewMySQLClient creates a new MySQL client with connection pooling.
// The DSN should include parseTime=True for proper timestamp handling.
func NewMySQLClient(config *Config, insertDuration *prometheus.HistogramVec, insertErrors *prometheus.CounterVec) (*MySQLClient, error) {
	db, err := sql.Open("mysql", config.MySQL.DSN)
	if err != nil {
		return nil, fmt.Errorf("failed to open MySQL connection: %w", err)
	}

	// Configure connection pool
	db.SetMaxOpenConns(config.MySQL.MaxOpenConns)
	db.SetMaxIdleConns(config.MySQL.MaxIdleConns)
	db.SetConnMaxLifetime(config.MySQL.ConnMaxLifetime)

	// Verify connection
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping MySQL: %w", err)
	}

	slog.Info("MySQL client initialized",
		"maxOpenConns", config.MySQL.MaxOpenConns,
		"maxIdleConns", config.MySQL.MaxIdleConns,
		"connMaxLifetime", config.MySQL.ConnMaxLifetime,
	)

	return &MySQLClient{
		db:             db,
		config:         config,
		insertDuration: insertDuration,
		insertErrors:   insertErrors,
	}, nil
}

// DB returns the underlying sql.DB for direct query access.
func (c *MySQLClient) DB() *sql.DB {
	return c.db
}

// Close closes the MySQL connection pool.
// Should be called during graceful shutdown.
func (c *MySQLClient) Close() error {
	if c.db != nil {
		return c.db.Close()
	}
	return nil
}

// Exec executes a query that doesn't return rows (INSERT, UPDATE, DELETE).
// Includes retry logic with exponential backoff for transient errors.
func (c *MySQLClient) Exec(query string, args ...interface{}) (sql.Result, error) {
	maxRetries := 3
	baseDelay := 1 * time.Second

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		result, err := c.db.Exec(query, args...)
		if err != nil {
			lastErr = err
			if isTransientError(err) && attempt < maxRetries {
				delay := baseDelay * time.Duration(1<<uint(attempt))
				slog.Warn("MySQL transient error, retrying",
					"attempt", attempt+1,
					"delay", delay,
					"error", err,
				)
				time.Sleep(delay)
				continue
			}
			return nil, err
		}
		return result, nil
	}
	return nil, lastErr
}

// BatchInsert performs a batch insert with the given table, columns, and rows.
// Updates MySQL insert duration metrics. Uses the client's own pool.
func (c *MySQLClient) BatchInsert(table string, columns []string, rows [][]interface{}) (int64, error) {
	return BatchInsertDB(c.db, dialect.Current(), table, columns, rows, c.insertDuration, c.insertErrors)
}

// BatchInsertDB performs a multi-row INSERT against an arbitrary *sql.DB
// (e.g. a per-company pool resolved via internal/tenant). Records duration
// and inserts errors on the provided vectors (ignored when nil).
// Only records rows>0; an empty batch is a no-op.
//
// Conflict semantics are dialect-aware and append-only-safe (verified against
// live PostgreSQL via database/init-pg smoke tests):
//   - MySQL: INSERT IGNORE
//   - PostgreSQL: INSERT ... ON CONFLICT DO NOTHING
//
// Plain upsert (ON CONFLICT DO UPDATE / ON DUPLICATE KEY UPDATE) is NOT used
// here because target tables carry GENERATED ALWAYS AS IDENTITY primary keys;
// updating "id = EXCLUDED.id" would raise SQLSTATE 428C9.
func BatchInsertDB(db *sql.DB, d dialect.Dialect, table string, columns []string, rows [][]interface{},
	duration *prometheus.HistogramVec, errors *prometheus.CounterVec) (int64, error) {
	if len(rows) == 0 {
		return 0, nil
	}

	start := time.Now()

	// Dialect-aware column list & table identifier quoting.
	query := fmt.Sprintf("%s INTO %s (%s) VALUES ",
		d.InsertOrIgnoreLead(), d.QuoteIdent(table), joinColumns(d, columns))
	args := make([]interface{}, 0, len(rows)*len(columns))
	valuePlaceholders := make([]string, 0, len(rows))

	// Per-row placeholder group uses the dialect's ordinal marker ("?"). The
	// postgres driver opened via Dialect.DriverName() is the transpiling
	// wrapper ("pgxadatrack") that converts these to $N at execution time.
	rowPH := make([]string, len(columns))
	for i := range columns {
		rowPH[i] = "?"
	}
	valueGroup := "(" + joinStrings(rowPH, ", ") + ")"

	for _, row := range rows {
		valuePlaceholders = append(valuePlaceholders, valueGroup)
		args = append(args, row...)
	}

	query += joinStrings(valuePlaceholders, ", ")
	query += d.ConflictDoNothing()

	result, err := db.Exec(query, args...)
	if err != nil {
		if errors != nil {
			errors.WithLabelValues(table).Inc()
		}
		return 0, fmt.Errorf("batch insert failed: %w", err)
	}

	if duration != nil {
		duration.WithLabelValues(table).Observe(time.Since(start).Seconds())
	}

	rowsAffected, _ := result.RowsAffected()
	return rowsAffected, nil
}

// joinColumns joins column names with commas, each quoted for the dialect.
func joinColumns(d dialect.Dialect, columns []string) string {
	result := ""
	for i, col := range columns {
		if i > 0 {
			result += ", "
		}
		result += d.QuoteIdent(col)
	}
	return result
}

// joinStrings joins a slice of strings with a separator.
func joinStrings(parts []string, sep string) string {
	result := ""
	for i, part := range parts {
		if i > 0 {
			result += sep
		}
		result += part
	}
	return result
}

// isTransientError checks if an error is transient and suitable for retry.
func isTransientError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	// Common transient MySQL errors
	transientPatterns := []string{
		"connection refused",
		"connection reset",
		"timeout",
		"too many connections",
		"lock wait timeout",
		"deadlock",
		"server has gone away",
	}
	for _, pattern := range transientPatterns {
		if containsString(errStr, pattern) {
			return true
		}
	}
	return false
}

// containsString checks if a string contains a substring.
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

// searchSubstring performs a case-insensitive substring search.
func searchSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		match := true
		for j := 0; j < len(substr); j++ {
			if toLowerByte(s[i+j]) != toLowerByte(substr[j]) {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func toLowerByte(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + 32
	}
	return b
}
