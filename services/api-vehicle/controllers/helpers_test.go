package controllers

import (
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func init() { gin.SetMode(gin.TestMode) }

func TestFmtParseUintExtra(t *testing.T) {
	if v, err := fmtParseUint("123"); err != nil || v != 123 {
		t.Fatalf("fmtParseUint(123) = %d, %v", v, err)
	}
	if _, err := fmtParseUint(""); err == nil {
		t.Fatal("expected error for empty")
	}
	if _, err := fmtParseUint("12a"); err == nil {
		t.Fatal("expected error for non-numeric")
	}
	if _, err := fmtParseUint("-1"); err == nil {
		t.Fatal("expected error for negative")
	}
}

func TestParseIDParam(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Params = gin.Params{{Key: "id", Value: "42"}}
	if id, ok := parseIDParam(c); !ok || id != 42 {
		t.Fatalf("parseIDParam valid = %d,%v", id, ok)
	}

	c2, _ := gin.CreateTestContext(httptest.NewRecorder())
	c2.Params = gin.Params{{Key: "id", Value: "abc"}}
	if _, ok := parseIDParam(c2); ok {
		t.Fatal("expected fail on non-numeric id")
	}
	if c2.Writer.Status() != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", c2.Writer.Status())
	}
}

func TestIsDuplicateErr(t *testing.T) {
	dup := errors.New("Error 1062: Duplicate entry '1' for key 'PRIMARY'")
	if !isDuplicateErr(dup) {
		t.Fatal("duplicate error not detected")
	}
	if isDuplicateErr(nil) {
		t.Fatal("nil considered duplicate")
	}
	if isDuplicateErr(sql.ErrNoRows) {
		t.Fatal("ErrNoRows considered duplicate")
	}
}

func TestTimeFmt(t *testing.T) {
	if timeFmt(sql.NullTime{}) != "" {
		t.Fatal("NULL time should be empty")
	}
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	if got := timeFmt(sql.NullTime{Time: now, Valid: true}); got != "2026-08-25T12:00:00Z" {
		t.Fatalf("timeFmt = %q", got)
	}
}

func nowForTest() time.Time { return time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC) }

var zeroU64 uint64