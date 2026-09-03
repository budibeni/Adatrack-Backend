package controllers

import (
	"testing"

	"ajb_gps/internal/dialect"
)

// PG-parity regression guards untuk jalur notifikasi (sesi verifikasi live
// 2026-08-26 menemukan dua bug: LastInsertId di InsertAlert & JSON_OBJECT()
// di EnabledPreferences — keduanya hanya meledak di provider postgres).
func TestDeliveryDefaultExpr(t *testing.T) {
	if got := deliveryDefaultExpr(dialect.Postgres); got != `'{}'::jsonb` {
		t.Fatalf("postgres expr = %q, want '{}':jsonb literal", got)
	}
	if got := deliveryDefaultExpr(dialect.MySQL); got != "JSON_OBJECT()" {
		t.Fatalf("mysql expr = %q, want JSON_OBJECT()", got)
	}
}
