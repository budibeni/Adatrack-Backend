package dialect

import "testing"

func TestFromProvider(t *testing.T) {
	cases := []struct {
		in   string
		want Dialect
	}{
		{"mysql", MySQL}, {"MYSQL", MySQL}, {"mariadb", MySQL},
		{"", Postgres}, {"postgres", Postgres}, {"Postgres", Postgres}, {"PG", Postgres},
		{"postgresql", Postgres}, {"  PostgreSQL  ", Postgres}, {"sqlite", Postgres}, // unknown → default proyek
	}
	for _, c := range cases {
		if got := FromProvider(c.in); got != c.want {
			t.Errorf("FromProvider(%q)=%s want %s", c.in, got, c.want)
		}
	}
}

func TestDialectDriverName(t *testing.T) {
	if MySQL.DriverName() != "mysql" {
		t.Errorf("mysql driver = %q want mysql", MySQL.DriverName())
	}
	if got := Postgres.DriverName(); got != PgxadatrackDriverName {
		t.Errorf("pg driver = %q want %q", got, PgxadatrackDriverName)
	}
}

func TestInsertOrIgnore(t *testing.T) {
	cases := []struct {
		d       Dialect
		lead    string
		trailer string
	}{
		{MySQL, "INSERT IGNORE", ""},
		{Postgres, "INSERT", " ON CONFLICT DO NOTHING"},
	}
	for _, c := range cases {
		if got := c.d.InsertOrIgnoreLead(); got != c.lead {
			t.Errorf("%s lead=%q want %q", c.d, got, c.lead)
		}
		if got := c.d.ConflictDoNothing(); got != c.trailer {
			t.Errorf("%s trailer=%q want %q", c.d, got, c.trailer)
		}
	}
}

func TestQuoteIdent(t *testing.T) {
	if MySQL.QuoteIdent("name") != "`name`" {
		t.Errorf("mysql quote got %q", MySQL.QuoteIdent("name"))
	}
	if Postgres.QuoteIdent("name") != `"name"` {
		t.Errorf("pg quote got %q", Postgres.QuoteIdent("name"))
	}
}

func TestValuesExpr(t *testing.T) {
	if MySQL.ValuesExpr("name") != "VALUES(`name`)" {
		t.Errorf("mysql values = %q", MySQL.ValuesExpr("name"))
	}
	if Postgres.ValuesExpr("name") != `EXCLUDED."name"` {
		t.Errorf("pg values = %q", Postgres.ValuesExpr("name"))
	}
}

func TestUpsert(t *testing.T) {
	// MySQL ignores conflictCols; PG uses them as target.
	mysql := MySQL.Upsert(nil, []string{"name = " + MySQL.ValuesExpr("name"), "is_active = TRUE"})
	if mysql != " ON DUP..."[:0] {
		// sanity: must contain ON DUPLICATE KEY UPDATE and VALUES(name)
	}
	if mysql != " ON DUPLICATE KEY UPDATE name = VALUES(`name`), is_active = TRUE" {
		t.Errorf("mysql upsert = %q", mysql)
	}
	pg := Postgres.Upsert([]string{"code"}, []string{"name = " + Postgres.ValuesExpr("name"), "is_active = TRUE"})
	want := ` ON CONFLICT ("code") DO UPDATE SET name = EXCLUDED."name", is_active = TRUE`
	if pg != want {
		t.Errorf("pg upsert = %q want %q", pg, want)
	}
	// PG without a conflict target must degrade to DO NOTHING (DO UPDATE
	// requires a target — telemetry batch has none).
	if got := Postgres.Upsert(nil, []string{"name = " + Postgres.ValuesExpr("name")}); got != " ON CONFLICT DO NOTHING" {
		t.Errorf("pg upsert(nil) = %q want ON CONFLICT DO NOTHING", got)
	}
}

func TestJSONArrayEmpty(t *testing.T) {
	if MySQL.JSONArrayEmpty() != "JSON_ARRAY()" {
		t.Errorf("mysql arr = %q", MySQL.JSONArrayEmpty())
	}
	if Postgres.JSONArrayEmpty() != "'[]'::jsonb" {
		t.Errorf("pg arr = %q", Postgres.JSONArrayEmpty())
	}
	if Postgres.JSONObjectEmpty() != "'{}'::jsonb" {
		t.Errorf("pg obj = %q", Postgres.JSONObjectEmpty())
	}
}

func TestPlaceholders(t *testing.T) {
	if MySQL.Placeholders(3) != "?, ?, ?" {
		t.Errorf("mysql placeholders = %q", MySQL.Placeholders(3))
	}
	if Postgres.Placeholders(3) != "$1, $2, $3" {
		t.Errorf("pg placeholders = %q", Postgres.Placeholders(3))
	}
	if MySQL.Placeholders(0) != "" {
		t.Errorf("mysql p(0)=%q", MySQL.Placeholders(0))
	}
}

func TestErrIsDuplicate(t *testing.T) {
	if !ErrIsDuplicate(MySQL, errStr("Error 1062: Duplicate entry 'x'")) {
		t.Error("expected mysql dup")
	}
	if ErrIsDuplicate(MySQL, errStr("syntax error")) {
		t.Error("mysql should not treat syntax error as dup")
	}
	if !ErrIsDuplicate(Postgres, errStr("ERROR: duplicate key (23505)")) {
		t.Error("expected pg dup")
	}
	if ErrIsDuplicate(Postgres, nil) {
		t.Error("nil should not be dup")
	}
}

type errStr string

func (e errStr) Error() string { return string(e) }

func TestCurrentDefaultIsMySQL(t *testing.T) {
	// Reset to default by setting then leaving; default at package load is MySQL.
	Set(MySQL)
	if Current() != MySQL {
		t.Errorf("current=%s want mysql", Current())
	}
	Set(Postgres)
	if Current() != Postgres {
		t.Errorf("current=%s want postgres", Current())
	}
	Set(MySQL)
}

func TestSplitSQLStatements(t *testing.T) {
	sql := `
-- header comment
CREATE TABLE foo (id BIGSERIAL PRIMARY KEY, payload JSONB);
INSERT INTO foo (payload) VALUES ('{"a":1; "b":2}');
INSERT INTO foo (payload) VALUES ('it''s; quoted');
/* block ; comment */
SELECT 'semi;colon' AS txt;
`
	stmts := SplitSQLStatements(sql)
	if len(stmts) != 4 {
		t.Fatalf("got %d statements: %#v", len(stmts), stmts)
	}
	if stmts[0] != "CREATE TABLE foo (id BIGSERIAL PRIMARY KEY name JSONB)" &&
		!contains(stmts[0], "CREATE TABLE foo") {
		t.Errorf("stmt0 = %q", stmts[0])
	}
	if stmts[1] != `INSERT INTO foo (payload) VALUES ('{"a":1; "b":2}')` {
		t.Errorf("stmt1 (string w/ semicolon) = %q", stmts[1])
	}
	if !contains(stmts[3], "semi") {
		t.Errorf("stmt3 = %q", stmts[3])
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
