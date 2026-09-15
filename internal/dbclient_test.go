package internal

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestQuoteIdentEscapes verifies identifier quoting (never sourced from user
// input, but it must still produce valid SQL for unusual schema names).
func TestQuoteIdentEscapes(t *testing.T) {
	if got := QuoteIdent("tm_vehicles"); got != `"tm_vehicles"` {
		t.Errorf("QuoteIdent = %s", got)
	}
	if got := QuoteIdent(`we"ird`); got != `"we""ird"` {
		t.Errorf("QuoteIdent must escape embedded quotes, got %s", got)
	}
}

// TestBatchInsertArgumentValidation covers the guard rails (nil pool, no columns,
// no rows, ragged rows) — the generated SQL itself is exercised by the E2E run.
func TestBatchInsertArgumentValidation(t *testing.T) {
	ctx := context.Background()

	if _, err := BatchInsert(ctx, nil, "t", []string{"a"}, [][]any{{1}}); err == nil {
		t.Error("expected an error for a nil pool")
	}

	pool := &DBPool{Name: "test"}
	if _, err := BatchInsert(ctx, pool, "t", nil, [][]any{{1}}); err == nil {
		t.Error("expected an error when no columns are given")
	}
	if _, err := BatchInsert(ctx, pool, "t", []string{"a"}, nil); err != nil {
		t.Errorf("an empty batch must be a no-op, got %v", err)
	}

	// Ragged row: fewer values than columns.
	_, err := BatchInsert(ctx, &DBPool{Name: "test", DB: newUnconnectedDB(t)}, "t",
		[]string{"a", "b"}, [][]any{{1}})
	if err == nil || !strings.Contains(err.Error(), "want 2") {
		t.Errorf("expected a column-count error, got %v", err)
	}
}

// TestIsTransientErrorClassification documents which failures are retried
// (FR-3.4 step 5) and which are terminal.
func TestIsTransientErrorClassification(t *testing.T) {
	transient := []string{
		"dial tcp: connection refused",
		"connection reset by peer",
		"i/o timeout",
		"deadlock detected",
		"the database system is starting up",
	}
	for _, msg := range transient {
		if !IsTransientError(errors.New(msg)) {
			t.Errorf("%q must be classified transient", msg)
		}
	}

	terminal := []string{
		"numeric field overflow",
		"duplicate key value violates unique constraint",
		"permission denied for schema adatrack_gps_dev001",
	}
	for _, msg := range terminal {
		if IsTransientError(errors.New(msg)) {
			t.Errorf("%q must NOT be retried", msg)
		}
	}
	if IsTransientError(nil) {
		t.Error("nil must not be transient")
	}
}

// TestRetryWithBackoffRetriesTransientOnly verifies the retry schedule and that
// terminal errors stop immediately.
func TestRetryWithBackoffRetriesTransientOnly(t *testing.T) {
	backoff := []time.Duration{time.Millisecond, time.Millisecond}

	attempts := 0
	err := RetryWithBackoff(context.Background(), backoff, 3, func(context.Context) error {
		attempts++
		if attempts < 3 {
			return errors.New("connection refused")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected success after retries, got %v", err)
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}

	attempts = 0
	err = RetryWithBackoff(context.Background(), backoff, 3, func(context.Context) error {
		attempts++
		return errors.New("numeric field overflow")
	})
	if err == nil {
		t.Fatal("expected the terminal error to be returned")
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1 (terminal errors are not retried)", attempts)
	}

	// A cancelled context stops the retry loop instead of sleeping.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	attempts = 0
	_ = RetryWithBackoff(ctx, []time.Duration{time.Hour}, 3, func(context.Context) error {
		attempts++
		return errors.New("connection refused")
	})
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1 (cancelled context must not retry)", attempts)
	}
}

// newUnconnectedDB returns a *sql.DB that is never dialled: every statement
// fails, which is what the guard tests need.
func newUnconnectedDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", "postgres://user:pw@127.0.0.1:1/none?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
