package internal

// normalization_test.go — B10 "Normalisasi & Konfigurasi" guard (PRD §6.0, §14).
//
// The B10 requirement is that every table follows the `tm_` (master/reference),
// `th_` (transaction header) or `td_` (transaction detail) naming convention and
// that the identity/business split (`tm_users` B2B vs `tm_users_b2c` B2C,
// `business_type` on `tm_companies`) exists. Because this repository was built
// "clean slate" those rules were applied from migration 001 — this test is the
// idempotent, machine-checked verification that they still hold, so a future
// migration cannot silently introduce an unprefixed table.
//
// It is a filesystem scan (no database needed), which is exactly what makes it
// runnable in the default `make test` gate.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

// SQL scan patterns. String literals are stripped first (the partition helpers
// build `CREATE TABLE ... PARTITION OF` DDL inside a DO block), then the optional
// `IF NOT EXISTS` is removed, and only then the table name is captured — a single
// regexp would backtrack and report the keyword `IF` as the table name.
var (
	sqlLiteralRE  = regexp.MustCompile(`'(?:[^']|'')*'`)
	ifNotExistsRE = regexp.MustCompile(`(?i)\bIF\s+NOT\s+EXISTS\b`)
	createTableRE = regexp.MustCompile(`(?i)\bCREATE\s+TABLE\s+("[^"]+"|[A-Za-z_][A-Za-z0-9_]*)`)
)

// backendRoot walks up from the test working directory until it finds the
// migrations directory (the test may run from internal/ or the module root).
func backendRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 6; i++ {
		if st, serr := os.Stat(filepath.Join(dir, "database", "migrations")); serr == nil && st.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("database/migrations not found above %s", dir)
	return ""
}

// migrationFiles lists every SQL migration (master + company).
func migrationFiles(t *testing.T) []string {
	t.Helper()
	root := backendRoot(t)
	var out []string
	for _, sub := range []string{"master_pg", "company_pg"} {
		entries, err := os.ReadDir(filepath.Join(root, "database", "migrations", sub))
		if err != nil {
			t.Fatalf("read migrations %s: %v", sub, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
				continue
			}
			out = append(out, filepath.Join(root, "database", "migrations", sub, e.Name()))
		}
	}
	if len(out) == 0 {
		t.Fatal("no migration files found")
	}
	return out
}

// TestMigrationTablePrefixesMatchConvention asserts every CREATE TABLE uses the
// tm_/th_/td_ prefix and an unqualified name (the schema is injected through the
// per-tenant search_path — PRD §6.2).
// TestObserveTelemetryIntervalSurvivesLateRegistration guards the B10 gauge: the
// config is loaded before a service registers its collectors, so the observed value
// must be replayed at registration — otherwise /metrics reported
// `telemetry_interval_seconds 0` instead of the configured interval (found while
// auditing B10 against a live `curl :8090/metrics`).
func TestObserveTelemetryIntervalSurvivesLateRegistration(t *testing.T) {
	ObserveTelemetryInterval(20)

	reg := prometheus.NewRegistry()
	RegisterSharedMetrics(reg)

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, mf := range families {
		if mf.GetName() != "telemetry_interval_seconds" {
			continue
		}
		if got := mf.GetMetric()[0].GetGauge().GetValue(); got != 20 {
			t.Fatalf("telemetry_interval_seconds = %v, want 20", got)
		}
		return
	}
	t.Fatal("telemetry_interval_seconds was not registered")
}

func TestMigrationTablePrefixesMatchConvention(t *testing.T) {
	allowed := []string{"tm_", "th_", "td_"}
	seen := map[string]string{}

	for _, file := range migrationFiles(t) {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		scan := ifNotExistsRE.ReplaceAllString(sqlLiteralRE.ReplaceAllString(string(raw), "''"), " ")
		for _, m := range createTableRE.FindAllStringSubmatch(scan, -1) {
			name := strings.Trim(m[1], `"`)
			if strings.Contains(name, ".") {
				t.Errorf("%s: table %q is schema-qualified; migrations rely on search_path",
					filepath.Base(file), name)
			}
			if !hasAnyPrefix(name, allowed) {
				t.Errorf("%s: table %q violates the tm_/th_/td_ convention",
					filepath.Base(file), name)
			}
			if prev, dup := seen[name]; dup && prev != file {
				// The same table may be declared once per schema family (e.g. the ledger
				// in master AND company) or re-declared idempotently — informational.
				t.Logf("table %q also declared in %s", name, filepath.Base(prev))
			}
			seen[name] = file
		}
	}

	// The B10 identity/business normalisation must be present and explicit.
	for _, required := range []string{"tm_users", "tm_users_b2c", "tm_companies"} {
		if _, ok := seen[required]; !ok {
			t.Errorf("required table %q not found in any migration", required)
		}
	}
}

// TestBusinessTypeSplitIsDeclared asserts the B2B/B2C split of B10: the master
// user tables are separate and `tm_companies` carries `business_type`.
func TestBusinessTypeSplitIsDeclared(t *testing.T) {
	root := backendRoot(t)
	users, err := os.ReadFile(filepath.Join(root, "database", "migrations", "master_pg",
		"008_create_users.sql"))
	if err != nil {
		t.Fatalf("read tm_users migration: %v", err)
	}
	b2c, err := os.ReadFile(filepath.Join(root, "database", "migrations", "master_pg",
		"009_create_users_b2c.sql"))
	if err != nil {
		t.Fatalf("read tm_users_b2c migration: %v", err)
	}
	companies, err := os.ReadFile(filepath.Join(root, "database", "migrations", "master_pg",
		"003_create_companies.sql"))
	if err != nil {
		t.Fatalf("read tm_companies migration: %v", err)
	}

	if !strings.Contains(string(users), "CREATE TABLE IF NOT EXISTS tm_users") {
		t.Error("tm_users (B2B master) is not declared in 008_create_users.sql")
	}
	if !strings.Contains(string(b2c), "CREATE TABLE IF NOT EXISTS tm_users_b2c") {
		t.Error("tm_users_b2c (B2C master) is not declared in 009_create_users_b2c.sql")
	}
	if !strings.Contains(string(companies), "business_type") {
		t.Error("tm_companies does not declare business_type (B2B/B2C, PRD §6.0)")
	}
}

// hasAnyPrefix reports whether name starts with one of the allowed prefixes.
func hasAnyPrefix(name string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}
