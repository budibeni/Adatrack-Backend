package internal

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// listMigrationFiles returns the sorted *.sql files of a migrations directory.
func listMigrationFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("migrate: read dir %s: %w", dir, err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		files = append(files, filepath.Join(dir, e.Name()))
	}
	sort.Strings(files)
	return files, nil
}

// fileChecksum returns the sha256 of a migration file.
func fileChecksum(path string) (string, error) {
	body, err := os.ReadFile(path) //nolint:gosec // our own migrations dir
	if err != nil {
		return "", fmt.Errorf("migrate: read %s: %w", path, err)
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

// ensureLedger creates the ledger table (used before the first migration runs).
func ensureLedger(ctx context.Context, conn *sql.Conn, schema, ledgerTable string) error {
	if _, err := conn.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS "+QuoteIdent(schema)); err != nil {
		return fmt.Errorf("migrate: create schema %s: %w", schema, err)
	}
	_, err := conn.ExecContext(ctx, fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s.%s (
			id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
			scope VARCHAR(64) NOT NULL,
			version VARCHAR(255) NOT NULL,
			checksum VARCHAR(64) NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
			duration_ms BIGINT NOT NULL DEFAULT 0,
			success BOOLEAN NOT NULL DEFAULT TRUE,
			applied_by VARCHAR(64) NOT NULL DEFAULT CURRENT_USER,
			CONSTRAINT uq_schema_migrations UNIQUE (scope, version))`,
		QuoteIdent(schema), QuoteIdent(ledgerTable)))
	if err != nil {
		return fmt.Errorf("migrate: ensure ledger %s.%s: %w", schema, ledgerTable, err)
	}
	return nil
}

// ledgerChecksum returns the recorded checksum of a version ("" when absent).
func ledgerChecksum(ctx context.Context, conn *sql.Conn, schema, ledgerTable, scope, version string) (string, error) {
	var sum string
	err := conn.QueryRowContext(ctx, fmt.Sprintf(
		"SELECT checksum FROM %s.%s WHERE scope = $1 AND version = $2 AND success",
		QuoteIdent(schema), QuoteIdent(ledgerTable)), scope, version).Scan(&sum)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("migrate: read ledger %s/%s: %w", scope, version, err)
	}
	return sum, nil
}

// LedgerStatus reports (applied, failures) for a schema — a service can refuse
// readiness while migrations are incomplete (PRD §14.5 step 5).
func LedgerStatus(ctx context.Context, db *sql.DB, schema, ledgerTable string) (applied, failures int, err error) {
	query := fmt.Sprintf(
		`SELECT count(*) FILTER (WHERE success), count(*) FILTER (WHERE NOT success)
		 FROM %s.%s`, QuoteIdent(schema), QuoteIdent(ledgerTable))
	err = db.QueryRowContext(ctx, query).Scan(&applied, &failures)
	if err != nil {
		return 0, 0, err
	}
	return applied, failures, nil
}

// advisoryLockKey derives a stable int64 key from a name (FNV-1a), used for
// pg_advisory_lock so parallel deploys serialise instead of racing.
func advisoryLockKey(name string) int64 {
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)
	var h uint64 = offset64
	for i := 0; i < len(name); i++ {
		h ^= uint64(name[i])
		h *= prime64
	}
	return int64(h)
}
