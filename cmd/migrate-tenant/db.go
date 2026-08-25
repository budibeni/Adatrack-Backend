package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"ajb_gps/internal/dialect"
)

// openDB opens a verified sql.DB for the given DSN.
func openDB(dsn string) (*sql.DB, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// applyMigrations executes every *.sql file in dir in lexical (versioned)
// order against the given connection. Each file is split into individual
// statements (see internal/dialect.SplitSQLStatements) so the same code works
// against MySQL (multiStatements) and PostgreSQL (pgx extended protocol
// forbids multiple statements per Exec).
func applyMigrations(ctx context.Context, db *sql.DB, dir string) (int, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil {
		return 0, err
	}
	sortStrings(files)

	applied := 0
	for _, f := range files {
		content, err := os.ReadFile(f)
		if err != nil {
			return applied, fmt.Errorf("read %s: %w", f, err)
		}
		for _, stmt := range dialect.SplitSQLStatements(string(content)) {
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				return applied, fmt.Errorf("exec %s (%.72s): %w", filepath.Base(f), stmt, err)
			}
		}
		applied++
		slog.Info("migration applied", "file", filepath.Base(f))
	}
	return applied, nil
}

// sortStrings is a tiny deterministic sort (avoids pulling sort import twice).
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
