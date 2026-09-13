// Package dialect provides a minimal, shared abstraction over the two SQL
// dialects supported by this project (PRD §7: DATABASE_PROVIDER=mysql|postgres).
//
// It exists so that the same Go code path works against either MySQL 8.0 or
// PostgreSQL 15 without branching on stringly-typed driver behaviour in every
// controller. The abstraction is intentionally small — it covers only the
// constructs that actually differ between the two engines:
//
//   - the database/sql driver name ("mysql" vs "pgx");
//   - `INSERT IGNORE` / `INSERT ... ON CONFLICT DO NOTHING`;
//   - `ON DUPLICATE KEY UPDATE ... = VALUES(col)` / `ON CONFLICT DO UPDATE ... EXCLUDED.col`;
//   - `JSON_ARRAY()` / `JSON_OBJECT()` empty expressions;
//   - `LastInsertId()` → `INSERT ... RETURNING id`.
//
// Important: PostgreSQL is reached via pgx v5's stdlib driver
// (`sql.Open("pgx", dsn)`). NOTE (fixed B4 2026-08-25): that driver does NOT
// rewrite '?' placeholders — every postgres pool is therefore opened through
// the placeholder-transpiling wrapper "pgxadatrack" instead (see pgxdriver.go),
// so ordinary query placeholders need NO change; only the five constructs
// above differ.
//
// Every method on Dialect is pure (no I/O) and unit-tested. Controllers may also
// read a process-wide default via Current(), which each service sets once at
// startup from internal.Config.Dialect() (see main.go of every service).
package dialect

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
)

// Dialect identifies a supported SQL engine.
type Dialect string

const (
	MySQL    Dialect = "mysql"
	Postgres Dialect = "postgres"
)

// current is the process-wide dialect. Default mengikuti provider default
// proyek (POSTGRES, keputusan 2026-08-25); tiap service menimpanya sekali di
// startup via Set(cfg.Dialect()).
var current Dialect = Postgres

// Set sets the process-wide dialect. Called once from each service main.
func Set(d Dialect) { current = d }

// Current returns the process-wide dialect (defaults to MySQL).
func Current() Dialect { return current }

// FromProvider maps a DATABASE_PROVIDER value to a Dialect.
// Default proyek = POSTGRES (2026-08-25): string kosong dan nilai tak dikenal
// dipetakan ke Postgres; "mysql"/"mariadb" eksplisit ke MySQL. Validasi ketat
// tetap dilakukan di config.Validate()/tenant.Validate().
func FromProvider(provider string) Dialect {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "mysql", "mariadb":
		return MySQL
	default:
		return Postgres
	}
}

func (d Dialect) IsPostgres() bool { return d == Postgres }
func (d Dialect) IsMySQL() bool    { return d != Postgres }

// DriverName returns the database/sql driver name to pass to sql.Open.
// MySQL uses the go-sql-driver; PostgreSQL uses a placeholder-transpiling
// wrapper ("pgxadatrack") over pgx v5's stdlib driver, because the stdlib driver
// does NOT rewrite '?' to '$N' (verified live 2026-08-25 — SQLSTATE 42601).
func (d Dialect) DriverName() string {
	if d == Postgres {
		RegisterPgxPlaceholderDriver()
		return PgxadatrackDriverName
	}
	return "mysql"
}

// ---------------------------------------------------------------------------
// Identifier quoting
// ---------------------------------------------------------------------------

// QuoteIdent quotes a SQL identifier according to the dialect
// (MySQL back-tick, PostgreSQL double-quote).
func (d Dialect) QuoteIdent(ident string) string {
	ident = strings.TrimSpace(ident)
	if d == Postgres {
		return `"` + ident + `"`
	}
	return "`" + ident + "`"
}

// ---------------------------------------------------------------------------
// INSERT ... IGNORE
// ---------------------------------------------------------------------------

// InsertOrIgnoreLead returns the leading keywords of an insert that must NOT
// fail on duplicate keys:
//
//	mysql: "INSERT IGNORE"
//	pg:     "INSERT"
func (d Dialect) InsertOrIgnoreLead() string {
	if d == Postgres {
		return "INSERT"
	}
	return "INSERT IGNORE"
}

// ConflictDoNothing returns the trailing clause that makes an INSERT ignore
// duplicates on PostgreSQL, and the empty string on MySQL (IGNORE is already in
// the lead). Example build:
//
//	d.InsertOrIgnoreLead() + " INTO t (a,b) VALUES (?)" + d.ConflictDoNothing()
func (d Dialect) ConflictDoNothing() string {
	if d == Postgres {
		return " ON CONFLICT DO NOTHING"
	}
	return ""
}

// ---------------------------------------------------------------------------
// UPSERT (ON DUPLICATE KEY UPDATE / ON CONFLICT DO UPDATE)
// ---------------------------------------------------------------------------

// ValuesExpr returns the expression that references an inserted value inside an
// upsert body.
//
//	mysql: VALUES(col)   (e.g. name = VALUES(name))
//	pg:     EXCLUDED.col (e.g. name = EXCLUDED.name)
func (d Dialect) ValuesExpr(col string) string {
	if d == Postgres {
		return "EXCLUDED." + d.QuoteIdent(col)
	}
	return "VALUES(" + d.QuoteIdent(col) + ")"
}

// ExistingColRef returns an expression referencing the EXISTING (pre-conflict)
// row's column inside an upsert body.
//
//	mysql: bare col            (ON DUPLICATE KEY UPDATE scope resolves it)
//	pg:    <table>.<col>       REQUIRED: within ON CONFLICT DO UPDATE SET an
//	        unqualified column reference is ambiguous between the target row
//	        and the EXCLUDED pseudo-relation (SQLSTATE 42702).
func (d Dialect) ExistingColRef(table, col string) string {
	if d == Postgres {
		return d.QuoteIdent(table) + "." + d.QuoteIdent(col)
	}
	return d.QuoteIdent(col)
}

// Upsert returns the trailing upsert clause appended to an INSERT statement.
//
//	mysql: "ON DUPLICATE KEY UPDATE <setExprs>"            (conflictCols ignored)
//	pg:     "ON CONFLICT (<conflictCols>) DO UPDATE SET <setExprs>"
//
// Callers build setExprs with ValuesExpr(col) for "use inserted value" refs and
// literal SQL (e.g. "is_active = TRUE") for constants. Example:
//
//	d.Upsert([]string{"code"},
//	    []string{"name = " + d.ValuesExpr("name"), "is_active = TRUE"})
func (d Dialect) Upsert(conflictCols []string, setExprs []string) string {
	if d == Postgres {
		if len(conflictCols) == 0 {
			// PostgreSQL DO UPDATE requires a conflict target; without one the
			// only legal form is DO NOTHING. (MySQL ON DUPLICATE KEY UPDATE
			// fires on ANY unique key — for tables with no conflicting rows
			// this is effectively a no-op, which DO NOTHING mirrors safely.)
			return " ON CONFLICT DO NOTHING"
		}
		cols := make([]string, len(conflictCols))
		for i, c := range conflictCols {
			cols[i] = d.QuoteIdent(c)
		}
		target := "(" + strings.Join(cols, ", ") + ")"
		return " ON CONFLICT " + target + " DO UPDATE SET " + strings.Join(setExprs, ", ")
	}
	return " ON DUPLICATE KEY UPDATE " + strings.Join(setExprs, ", ")
}

// JSONArrayEmpty returns an empty JSON array literal for the dialect, suitable
// for use inside COALESCE(...) in a SELECT:
//
//	mysql: JSON_ARRAY()
//	pg:     '[]'::jsonb
func (d Dialect) JSONArrayEmpty() string {
	if d == Postgres {
		return "'[]'::jsonb"
	}
	return "JSON_ARRAY()"
}

// JSONObjectEmpty returns an empty JSON object literal for the dialect, for use
// inside COALESCE(...) in a SELECT:
//
//	mysql: JSON_OBJECT()
//	pg:     '{}'::jsonb
func (d Dialect) JSONObjectEmpty() string {
	if d == Postgres {
		return "'{}'::jsonb"
	}
	return "JSON_OBJECT()"
}

// ---------------------------------------------------------------------------
// INSERT ... RETURNING id  (dialect-aware replacement for LastInsertId)
// ---------------------------------------------------------------------------

// Queryer is satisfied by *sql.DB and *sql.Tx, letting the helpers below work
// for both standalone connections and transactions.
type Queryer interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

// InsertReturningID executes an INSERT and returns the new primary key.
//
//	mysql: db.Exec(insertSQL, args...) -> res.LastInsertId()
//	pg:     db.QueryRow(insertSQL+" RETURNING id", args...).Scan(&id)
//
// insertSQL must NOT include a trailing semicolon.
func InsertReturningID(d Dialect, ctx context.Context, db Queryer, insertSQL string, args ...interface{}) (int64, error) {
	if d == Postgres {
		var id int64
		err := db.QueryRowContext(ctx, insertSQL+" RETURNING id", args...).Scan(&id)
		return id, err
	}
	res, err := db.ExecContext(ctx, insertSQL, args...)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return id, err
}

// Placeholders returns n positional placeholders joined by the dialect's
// placeholder separator. MySQL uses "?" ; PostgreSQL uses "$1", "$2", ...
// (Most call sites can keep "?" — pgx rewrites them — but this is available for
// hand-built IN-lists or RETURNING helpers.)
func (d Dialect) Placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	if d == Postgres {
		parts := make([]string, n)
		for i := 0; i < n; i++ {
			parts[i] = "$" + strconv.Itoa(i+1)
		}
		return strings.Join(parts, ", ")
	}
	parts := make([]string, n)
	for i := range parts {
		parts[i] = "?"
	}
	return strings.Join(parts, ", ")
}

// ErrIsDuplicate reports whether err is a unique/duplicate-key violation for the
// given dialect (mysql 1062 / ER_DUP_ENTRY vs postgres SQLSTATE 23505).
func ErrIsDuplicate(d Dialect, err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	if d == Postgres {
		return strings.Contains(msg, "23505")
	}
	return strings.Contains(msg, "Error 1062") || strings.Contains(msg, "Duplicate entry")
}

// ---------------------------------------------------------------------------
// SQL statement splitting for migration files
// ---------------------------------------------------------------------------
// The migration runners (internal/tenant applyCompanyMigrations,
// cmd/migrate-tenant) execute *.sql files verbatim. MySQL is opened with
// multiStatements=true so one Exec can run a whole file; the pgx v5 driver
// uses the extended protocol (multi-statement not allowed), so the runner must
// split the file into individual statements first. Presentation here as a
// shared, dialect-independent helper keeps both runners identical.

// SplitSQLStatements splits a migration SQL file into executable statements at
// top-level semicolons, correctly skipping:
//
//   - single-quoted string literals (with ” and backslash escapes)
//   - double-quoted identifiers
//   - backtick-quoted identifiers (MySQL)
//   - line comments (--) and block comments (/* ... */)
//
// Resulting statements are trimmed; blank/comment-only chunks are dropped.
func SplitSQLStatements(sql string) []string {
	var out []string
	var cur strings.Builder

	inSingle := false
	inDouble := false
	inBacktick := false
	inLineComment := false
	inBlockComment := false

	n := len(sql)
	for i := 0; i < n; i++ {
		ch := sql[i]

		// Comment handling has the highest precedence.
		if inLineComment {
			if ch == '\n' {
				inLineComment = false
				cur.WriteByte(ch)
			} else {
				cur.WriteByte(ch)
			}
			continue
		}
		if inBlockComment {
			if ch == '*' && i+1 < n && sql[i+1] == '/' {
				inBlockComment = false
				cur.WriteString("*/")
				i++
			} else {
				cur.WriteByte(ch)
			}
			continue
		}

		// Detect comment starts outside strings.
		if !inSingle && !inDouble && !inBacktick {
			if ch == '-' && i+1 < n && sql[i+1] == '-' {
				inLineComment = true
				cur.WriteString("--")
				i++
				continue
			}
			if ch == '/' && i+1 < n && sql[i+1] == '*' {
				inBlockComment = true
				cur.WriteString("/*")
				i++
				continue
			}
		}

		switch {
		case inSingle:
			cur.WriteByte(ch)
			if ch == '\'' {
				if i+1 < n && sql[i+1] == '\'' { // escaped quote ''
					cur.WriteByte('\'')
					i++
				} else if i > 0 && sql[i-1] == '\\' {
					// escaped by backslash
				} else {
					inSingle = false
				}
			}
		case inDouble:
			cur.WriteByte(ch)
			if ch == '"' {
				if i+1 < n && sql[i+1] == '"' {
					cur.WriteByte('"')
					i++
				} else {
					inDouble = false
				}
			}
		case inBacktick:
			cur.WriteByte(ch)
			if ch == '`' {
				if i+1 < n && sql[i+1] == '`' {
					cur.WriteByte('`')
					i++
				} else {
					inBacktick = false
				}
			}
		default:
			switch ch {
			case '\'':
				inSingle = true
				cur.WriteByte(ch)
			case '"':
				inDouble = true
				cur.WriteByte(ch)
			case '`':
				inBacktick = true
				cur.WriteByte(ch)
			case ';':
				stmt := strings.TrimSpace(cur.String())
				if stmt != "" {
					out = append(out, stmt)
				}
				cur.Reset()
			default:
				cur.WriteByte(ch)
			}
		}
	}

	if last := strings.TrimSpace(cur.String()); last != "" {
		out = append(out, last)
	}
	return out
}
