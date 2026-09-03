package dialect

import "testing"

// TestExistingColRef guards the Postgres DO UPDATE SET ambiguity fix
// (SQLSTATE 42702): bare RHS columns inside ON CONFLICT DO UPDATE must be
// qualified with the target table; MySQL keeps the bare column form.
func TestExistingColRef(t *testing.T) {
	if got := MySQL.ExistingColRef("companies", "legal_name"); got != "`legal_name`" && got != "legal_name" {
		t.Fatalf("mysql ExistingColRef = %q", got)
	}
	want := `"companies"."legal_name"`
	if got := Postgres.ExistingColRef("companies", "legal_name"); got != want {
		t.Fatalf("postgres ExistingColRef = %q, want %q", got, want)
	}
	// Sanity: ValuesExpr stays EXCLUDED.col on PG (inserted-value ref).
	if got := Postgres.ValuesExpr("name"); got != `EXCLUDED."name"` {
		t.Fatalf("postgres ValuesExpr = %q", got)
	}
}
