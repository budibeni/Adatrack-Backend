package controllers

import (
	"database/sql"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestWSPlaceholders(t *testing.T) {
	if got := placeholders(3); got != "?,?,?" {
		t.Fatalf("placeholders(3) = %q", got)
	}
	if got := placeholders(0); got != "" {
		t.Fatalf("placeholders(0) = %q", got)
	}
}

func TestWSMapKeys(t *testing.T) {
	k := mapKeys(map[uint64]struct{}{1: {}, 9: {}, 4: {}})
	if len(k) != 3 {
		t.Fatalf("len = %d", len(k))
	}
	seen := map[uint64]bool{}
	for _, v := range k {
		seen[v.(uint64)] = true
	}
	for _, id := range []uint64{1, 4, 9} {
		if !seen[id] {
			t.Fatalf("missing %d", id)
		}
	}
}

func TestWSNullableHelpers(t *testing.T) {
	if nullableStr("") != nil || nullableStr(" x ") != " x " {
		t.Fatal("nullableStr wrong")
	}
	if nullableStrP(sql.NullString{}) != nil {
		t.Fatal("nullableStrP null wrong")
	}
	if nullableStrP(sql.NullString{String: "a", Valid: true}) == nil {
		t.Fatal("nullableStrP valid wrong")
	}
	if nullableFloat(sql.NullFloat64{}) != nil {
		t.Fatal("nullableFloat null wrong")
	}
	if nullableFloat(sql.NullFloat64{Float64: 2.5, Valid: true}) == nil {
		t.Fatal("nullableFloat valid wrong")
	}
	now := time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)
	if nullableTimeP(sql.NullTime{}) != nil {
		t.Fatal("nullableTimeP null wrong")
	}
	if got := nullableTimeP(sql.NullTime{Time: now, Valid: true}); got == nil || !got.Equal(now) {
		t.Fatal("nullableTimeP valid wrong")
	}
	if nullableUint(sql.NullInt64{}) != nil {
		t.Fatal("nullableUint null wrong")
	}
	if nullableUint(sql.NullInt64{Int64: 7, Valid: true}) == nil {
		t.Fatal("nullableUint valid wrong")
	}
}

func TestWSAtoiAndPagination(t *testing.T) {
	if atoiDefault("", 5) != 5 || atoiDefault("7", 5) != 7 || atoiDefault("zz", 5) != 5 {
		t.Fatal("atoiDefault wrong")
	}
	run := func(q string) (int, int) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("GET", "/?"+q, nil)
		return paginationParams(c)
	}
	if p, l := run(""); p != 1 || l != 100 {
		t.Fatalf("empty => %d,%d", p, l)
	}
	if p, l := run("page=2&limit=300"); p != 2 || l != 300 {
		t.Fatalf("valid => %d,%d", p, l)
	}
	if p, l := run("page=0&limit=-2"); p != 1 || l != 100 {
		t.Fatalf("invalid => %d,%d", p, l)
	}
	if p, l := run("limit=9999"); p != 1 || l != 500 {
		t.Fatalf("over-max => %d,%d", p, l)
	}
}

func TestWSJoinStrings(t *testing.T) {
	if joinStrings(nil) != "" {
		t.Fatal("empty wrong")
	}
	if got := joinStrings([]string{"mysql:ok", "nats:ok"}); got != "mysql:ok,nats:ok" {
		t.Fatalf("join = %q", got)
	}
}

func TestWSCompanyCodeFromCtx(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(ctxCompanyCodeKey, "DEV001")
	if companyCodeFromCtx(c) != "DEV001" {
		t.Fatal("companyCodeFromCtx wrong")
	}
	c2, _ := gin.CreateTestContext(httptest.NewRecorder())
	if companyCodeFromCtx(c2) != "" {
		t.Fatal("empty ctx should give empty code")
	}
}