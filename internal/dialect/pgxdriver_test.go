package dialect

import (
	"database/sql"
	"testing"
)

func TestRewritePlaceholders(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"tanpa placeholder", "SELECT 1", "SELECT 1"},
		{"sudah $n tidak disentuh", "SELECT * FROM t WHERE a=$1 AND b=$2", "SELECT * FROM t WHERE a=$1 AND b=$2"},
		{"ordinal tunggal", "SELECT company_code FROM vehicle_imei_map WHERE imei = ?",
			"SELECT company_code FROM vehicle_imei_map WHERE imei = $1"},
		{"beberapa ordinal", "INSERT INTO t (a,b,c) VALUES (?, ?, ?)",
			"INSERT INTO t (a,b,c) VALUES ($1, $2, $3)"},
		{"multi-row insert urut", "INSERT INTO t (a,b) VALUES (?, ?), (?, ?), (?, ?)",
			"INSERT INTO t (a,b) VALUES ($1, $2), ($3, $4), ($5, $6)"},
		{"string literal dipertahankan", "SELECT 'a?b' FROM t WHERE x = ?",
			"SELECT 'a?b' FROM t WHERE x = $1"},
		{"escape kutip dalam literal", "SELECT 'it''s ? fine' WHERE x = ?",
			"SELECT 'it''s ? fine' WHERE x = $1"},
		{"identifier berkutip ganda", `SELECT "col?name" FROM t WHERE x = ?`,
			`SELECT "col?name" FROM t WHERE x = $1`},
		{"komentar baris", "-- filter: imei?\nSELECT 1 WHERE x = ?",
			"-- filter: imei?\nSELECT 1 WHERE x = $1"},
		{"komentar blok", "/* alamat? */ SELECT 1 WHERE x = ?",
			"/* alamat? */ SELECT 1 WHERE x = $1"},
		{"cast setelah placeholder", "WHERE imei = ?::text AND n > ?",
			"WHERE imei = $1::text AND n > $2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RewritePlaceholders(tc.in)
			if got != tc.want {
				t.Fatalf("RewritePlaceholders(%q)\n got  %q\n want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestDriverNameUsesWrapperForPostgres(t *testing.T) {
	pg := FromProvider("postgres")
	if got := pg.DriverName(); got != PgxadatrackDriverName {
		t.Fatalf("postgres DriverName = %q, want %q", got, PgxadatrackDriverName)
	}
	found := false
	for _, name := range sql.Drivers() {
		if name == PgxadatrackDriverName {
			found = true
		}
	}
	if !found {
		t.Fatalf("driver %q tidak terdaftar di database/sql", PgxadatrackDriverName)
	}
	if got := FromProvider("mysql").DriverName(); got != "mysql" {
		t.Fatalf("mysql DriverName = %q, want \"mysql\"", got)
	}
}