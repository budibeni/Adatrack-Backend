package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// checkAuditRows asserts the PRD §9.4 requirement that security-relevant events
// land in the append-only master `tm_audit_logs`: the harness produced a login, an
// invalid-credential failure, an access denial, a logout and a token revocation.
func checkAuditRows(ctx context.Context, opt options) checkResult {
	const name = "audit.trail_rows"

	db, err := openPG(ctx, opt)
	if err != nil {
		return fail(name, "postgres unavailable", err)
	}
	defer func() { _ = db.Close() }()

	required := map[string]string{
		"LOGIN_SUCCESS":       "a successful login is audited",
		"LOGIN_FAILURE":       "a failed login is audited",
		"ACCESS_DENIED":       "401/403 responses are audited",
		"LOGOUT":              "a logout is audited",
		"TOKEN_REVOKED":       "token revocation/denylist use is audited",
		"SOFT_DELETED_VIEWED": "reading soft-deleted rows is audited (only when exercised)",
	}

	since := time.Now().UTC().Add(-30 * time.Minute)
	counts := map[string]int64{}
	rows, qerr := db.QueryContext(ctx, fmt.Sprintf(`
		SELECT action, count(*) FROM %s.tm_audit_logs
		WHERE created_at >= $1 GROUP BY action`, quoteIdent(opt.masterSchema)), since)
	if qerr != nil {
		return fail(name, "audit query failed", qerr)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var action string
		var n int64
		if serr := rows.Scan(&action, &n); serr != nil {
			return fail(name, "audit scan failed", serr)
		}
		counts[action] = n
	}
	if rerr := rows.Err(); rerr != nil {
		return fail(name, "audit rows failed", rerr)
	}

	var missing []string
	for action, reason := range required {
		// SOFT_DELETED_VIEWED is only produced when a client asks for deleted rows,
		// which this harness does not, so it is reported but not required.
		if action == "SOFT_DELETED_VIEWED" {
			continue
		}
		if counts[action] == 0 {
			missing = append(missing, action+" ("+reason+")")
		}
	}
	if len(missing) > 0 {
		return fail(name, "missing audit actions: "+strings.Join(missing, ", "), nil)
	}

	// The append-only guarantee (PRD §9.4) must be enforced by the database.
	if !auditIsAppendOnly(ctx, db, opt.masterSchema) {
		return fail(name, "tm_audit_logs accepted an UPDATE/DELETE (append-only violated)", nil)
	}

	return pass(name, fmt.Sprintf("actions=%v (append-only enforced)", summarizeCounts(counts)))
}

// auditIsAppendOnly verifies the immutability trigger rejects an UPDATE.
func auditIsAppendOnly(ctx context.Context, db *sql.DB, schema string) bool {
	var auditID int64
	err := db.QueryRowContext(ctx, fmt.Sprintf(
		`SELECT audit_id FROM %s.tm_audit_logs ORDER BY audit_id DESC LIMIT 1`, quoteIdent(schema))).Scan(&auditID)
	if err != nil {
		return false
	}
	_, uerr := db.ExecContext(ctx, fmt.Sprintf(
		`UPDATE %s.tm_audit_logs SET reason = 'tampered' WHERE audit_id = $1`, quoteIdent(schema)), auditID)
	return uerr != nil // an error means the trigger blocked the mutation
}

// openPG opens a pool with the master schema on the search_path.
func openPG(ctx context.Context, opt options) (*sql.DB, error) {
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		opt.pg.user, opt.pg.password, opt.pg.host, opt.pg.port, opt.pg.db)
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// quoteIdent double-quotes a PostgreSQL identifier.
func quoteIdent(ident string) string {
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}

// summarizeCounts renders the audit counters for the report.
func summarizeCounts(counts map[string]int64) string {
	names := make([]string, 0, len(counts))
	for name, n := range counts {
		names = append(names, fmt.Sprintf("%s=%d", name, n))
	}
	// Deterministic ordering keeps the evidence readable.
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
	return strings.Join(names, " ")
}

// errNotFound is used by helpers that must distinguish an absent row.
var errNotFound = errors.New("not found")
