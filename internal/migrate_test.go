package internal

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestListMigrationFiles verifies ordering and filtering (forward-only apply).
func TestListMigrationFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"002_b.sql", "001_a.sql", "notes.txt", "003_c.sql"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("SELECT 1;"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	files, err := listMigrationFiles(dir)
	if err != nil {
		t.Fatalf("listMigrationFiles: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("found %d files, want 3 (*.sql only)", len(files))
	}
	want := []string{"001_a.sql", "002_b.sql", "003_c.sql"}
	for i, w := range want {
		if filepath.Base(files[i]) != w {
			t.Errorf("file[%d] = %s, want %s (sorted order)", i, filepath.Base(files[i]), w)
		}
	}

	if _, err := listMigrationFiles(filepath.Join(dir, "missing")); err == nil {
		t.Error("expected an error for a missing directory")
	}
}

// TestFileChecksumStability verifies the drift-guard input: identical contents
// hash identically and a modification is detected.
func TestFileChecksumStability(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "001_a.sql")
	if err := os.WriteFile(path, []byte("SELECT 1;"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	first, err := fileChecksum(path)
	if err != nil {
		t.Fatalf("fileChecksum: %v", err)
	}
	second, _ := fileChecksum(path)
	if first != second {
		t.Errorf("checksum is not stable: %s vs %s", first, second)
	}
	if len(first) != 64 {
		t.Errorf("sha256 hex length = %d, want 64", len(first))
	}

	if err := os.WriteFile(path, []byte("SELECT 2;"), 0o600); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	changed, _ := fileChecksum(path)
	if changed == first {
		t.Error("a modified migration file must produce a different checksum (drift detection)")
	}
}

// TestAdvisoryLockKeyIsStable documents that one schema always maps to the same
// advisory lock (parallel deploys serialise instead of racing).
func TestAdvisoryLockKeyIsStable(t *testing.T) {
	a := advisoryLockKey("adatrack:migrate:adatrack_gps_dev001")
	b := advisoryLockKey("adatrack:migrate:adatrack_gps_dev001")
	c := advisoryLockKey("adatrack:migrate:adatrack_gps_acme")
	if a != b {
		t.Error("the advisory lock key must be deterministic for the same schema")
	}
	if a == c {
		t.Error("different schemas should not share a lock key")
	}
}

// TestApplyMigrationsGuards checks the validation paths that need no live server.
func TestApplyMigrationsGuards(t *testing.T) {
	if _, err := ApplyMigrations(context.Background(), nil, "master", "s", t.TempDir(), "", time.Second); err == nil {
		t.Error("expected an error for a nil database handle")
	}

	db := newUnconnectedDB(t)
	if _, err := ApplyMigrations(context.Background(), db, "master", "s", t.TempDir(), "", time.Second); err == nil {
		t.Error("expected an error when the migrations directory holds no *.sql files")
	}
}

// TestLedgerStatusReportsFailure verifies the readiness gate errors out (never a
// false "ready") when the ledger cannot be read.
func TestLedgerStatusReportsFailure(t *testing.T) {
	db := newUnconnectedDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if _, _, err := LedgerStatus(ctx, db, "adatrack_gps_master", "tm_schema_migrations"); err == nil {
		t.Error("expected a ledger read error with an unreachable database")
	}
}
