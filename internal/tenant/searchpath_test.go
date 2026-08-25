package tenant

import "testing"

func TestEnsureSearchPath(t *testing.T) {
	cases := []struct {
		name   string
		raw    string
		schema string
		sslm   string
		want   string
	}{
		{
			"url_valid_tambah_search_path",
			"postgres://u:p%40ss@127.0.0.1:5532/adatrack_gps_db?sslmode=disable",
			"adatrack_gps_dev001",
			"disable",
			"postgres://u:p%40ss@127.0.0.1:5532/adatrack_gps_db?search_path=adatrack_gps_dev001&sslmode=disable",
		},
		{
			"search_path_eksisting_dioverride",
			"postgres://u:p@h/adatrack_gps_db?search_path=adatrack_gps_default&sslmode=disable",
			"adatrack_gps_master",
			"",
			"postgres://u:p@h/adatrack_gps_db?search_path=adatrack_gps_master&sslmode=disable",
		},
		{
			"url_tanpa_query",
			"postgres://u:p@h/adatrack_gps_db",
			"adatrack_gps_dev001",
			"disable",
			"postgres://u:p@h/adatrack_gps_db?search_path=adatrack_gps_dev001&sslmode=disable",
		},
		{
			"url_invalid_ditangani_aman",
			"postgres://u :broken",
			"adatrack_gps_dev001",
			"disable",
			"postgres://u :broken?search_path=adatrack_gps_dev001&sslmode=disable",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ensureSearchPath(tc.raw, tc.schema, tc.sslm)
			if got != tc.want {
				t.Fatalf("ensureSearchPath(%q, %q, %q)\n got  %q\n want %q", tc.raw, tc.schema, tc.sslm, got, tc.want)
			}
		})
	}
}

// TestPostgresDSNForcesSearchPath memastikan bahwa meski DATABASE_URL diset,
// per-tenant search_path tetap disuntikkan (regresi B4 2026-08-25:
// "relation ... does not exist" karena schema diabaikan).
func TestPostgresDSNForcesSearchPath(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://adatrack_gps_user:secret@127.0.0.1:5532/adatrack_gps_db?sslmode=disable")
	t.Setenv("POSTGRES_DB", "adatrack_gps_db")
	c := Config{Provider: "postgres", PostgresSSLMode: "disable"}
	got := c.postgresDSN("adatrack_gps_dev001")
	want := "postgres://adatrack_gps_user:secret@127.0.0.1:5532/adatrack_gps_db?search_path=adatrack_gps_dev001&sslmode=disable"
	if got != want {
		t.Fatalf("postgresDSN(schema) dgn DATABASE_URL\n got  %q\n want %q", got, want)
	}
}