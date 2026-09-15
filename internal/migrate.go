package internal

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// MigrationResult summarises one migrate run (used by /healthz + audit logging).
type MigrationResult struct {
	Schema   string
	Applied  int
	Skipped  int
	Duration time.Duration
}

// ApplyMigrations applies every *.sql file in dir to `schema` (PRD §14.5):
//
//   - PostgreSQL ADVISORY LOCK → concurrent deploys are safe: one migrator runs,
//     the others wait (MIGRATE_LOCK_TIMEOUT_SEC), so nothing is applied twice.
//   - LEDGER + CHECKSUM (tm_schema_migrations) → an already-applied file is
//     skipped; a modified already-applied file aborts (migrations are immutable).
//   - FORWARD-ONLY: no down migrations; rollback = previous image + backup (§12).
//   - IDEMPOTENT files are still expected (IF NOT EXISTS / ON CONFLICT).
func ApplyMigrations(ctx context.Context, db *sql.DB, scope, schema, dir, ledgerTable string, lockTimeout time.Duration) (*MigrationResult, error) {
	if db == nil {
		return nil, errors.New("migrate: nil database handle")
	}
	if ledgerTable == "" {
		ledgerTable = "tm_schema_migrations"
	}
	start := time.Now()
	res := &MigrationResult{Schema: schema}

	files, err := listMigrationFiles(dir)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return res, fmt.Errorf("migrate: no *.sql files in %s", dir)
	}

	lockKey := advisoryLockKey("adatrack:migrate:" + schema)
	lockCtx := ctx
	if lockTimeout > 0 {
		var cancel context.CancelFunc
		lockCtx, cancel = context.WithTimeout(ctx, lockTimeout)
		defer cancel()
	}

	conn, err := db.Conn(lockCtx)
	if err != nil {
		return nil, fmt.Errorf("migrate: acquire connection: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(lockCtx, "SELECT pg_advisory_lock($1)", lockKey); err != nil {
		return nil, fmt.Errorf("migrate: advisory lock (%s): %w", schema, err)
	}
	defer func() {
		if _, uerr := conn.ExecContext(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", lockKey); uerr != nil {
			slog.Warn("migrate: advisory unlock failed", "schema", schema, "error", uerr)
		}
	}()

	if err := ensureLedger(ctx, conn, schema, ledgerTable); err != nil {
		return nil, err
	}

	for _, file := range files {
		version := filepath.Base(file)
		checksum, err := fileChecksum(file)
		if err != nil {
			return nil, err
		}

		existing, err := ledgerChecksum(ctx, conn, schema, ledgerTable, scope, version)
		if err != nil {
			return nil, err
		}
		if existing != "" {
			if existing != checksum {
				return nil, fmt.Errorf(
					"migrate: checksum drift for %s/%s (ledger=%s file=%s) — migrations are immutable, add a new versioned file",
					scope, version, existing, checksum)
			}
			res.Skipped++
			continue
		}

		body, err := os.ReadFile(file) //nolint:gosec // path comes from our own migrations dir
		if err != nil {
			return nil, fmt.Errorf("migrate: read %s: %w", file, err)
		}

		fileStart := time.Now()
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return nil, fmt.Errorf("migrate: begin tx: %w", err)
		}
		// search_path is scoped to the transaction, so migration files never need
		// schema-qualified object names.
		if _, err := tx.ExecContext(ctx, "SELECT set_config('search_path', $1, true)", schema); err != nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("migrate: set search_path=%s: %w", schema, err)
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("migrate: apply %s/%s: %w", scope, version, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(
			`INSERT INTO %s.%s (scope, version, checksum, duration_ms, success, applied_by)
			 VALUES ($1,$2,$3,$4,TRUE,$5)
			 ON CONFLICT (scope, version) DO UPDATE SET checksum = EXCLUDED.checksum,
			   applied_at = CURRENT_TIMESTAMP, duration_ms = EXCLUDED.duration_ms, success = TRUE`,
			QuoteIdent(schema), QuoteIdent(ledgerTable)),
			scope, version, checksum, time.Since(fileStart).Milliseconds(), serviceName()); err != nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("migrate: record ledger %s/%s: %w", scope, version, err)
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("migrate: commit %s/%s: %w", scope, version, err)
		}

		slog.Info("migration applied", "scope", scope, "schema", schema, "version", version,
			"duration_ms", time.Since(fileStart).Milliseconds())
		res.Applied++
	}

	res.Duration = time.Since(start)
	slog.Info("migrations settled", "scope", scope, "schema", schema,
		"applied", res.Applied, "skipped", res.Skipped, "duration_ms", res.Duration.Milliseconds())
	return res, nil
}
