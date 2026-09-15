package controllers

import (
	"net/http"
	"testing"
	"time"
)

// TestItoaHelper asserts the small integer formatter used in SQL placeholders.
func TestItoaHelper(t *testing.T) {
	cases := map[int64]string{0: "0", 7: "7", 42: "42", 1000: "1000", 987654321: "987654321"}
	for in, want := range cases {
		if got := itoa(in); got != want {
			t.Fatalf("itoa(%d) = %q, want %q", in, got, want)
		}
	}
}

// TestPlaceholders asserts the parameterised IN-list builder.
func TestPlaceholders(t *testing.T) {
	if got := placeholders(3); got != "$1, $2, $3" {
		t.Fatalf("placeholders(3) = %q, want \"$1, $2, $3\"", got)
	}
	if got := placeholders(0); got != "NULL" {
		t.Fatalf("placeholders(0) = %q, want NULL", got)
	}
}

// TestEscapeLikeNeutralisesWildcards asserts the search-term hardening (§8.5).
func TestEscapeLikeNeutralisesWildcards(t *testing.T) {
	if got := escapeLike("100%"); got != `100\%` {
		t.Fatalf("escapeLike = %q, want 100\\%%", got)
	}
	if got := escapeLike("a_b"); got != `a\_b` {
		t.Fatalf("escapeLike = %q, want a\\_b", got)
	}
	if got := escapeLike(`a\b`); got != `a\\b` {
		t.Fatalf("escapeLike = %q, want a\\\\b", got)
	}
	if got := escapeLike("  spaced  "); got != "spaced" {
		t.Fatalf("escapeLike trim = %q, want spaced", got)
	}
}

// TestParseTimestampFormats asserts the accepted history window formats (§8.5).
func TestParseTimestampFormats(t *testing.T) {
	rfc, err := parseTimestamp("2026-09-15T10:30:00Z")
	if err != nil {
		t.Fatalf("RFC3339 rejected: %v", err)
	}
	if rfc.UTC().Format(time.RFC3339) != "2026-09-15T10:30:00Z" {
		t.Fatalf("parsed RFC3339 = %v", rfc)
	}
	date, err := parseTimestamp("2026-09-15")
	if err != nil {
		t.Fatalf("date rejected: %v", err)
	}
	if date.UTC().Format("2006-01-02") != "2026-09-15" {
		t.Fatalf("parsed date = %v", date)
	}
	if _, err := parseTimestamp("15/09/2026"); err == nil {
		t.Fatalf("unsupported format accepted")
	}
	if _, err := parseTimestamp(""); err == nil {
		t.Fatalf("empty timestamp accepted")
	}
}

// TestPaginationBlock asserts the PRD §8.1 pagination shape.
func TestPaginationBlock(t *testing.T) {
	block := pagination(2, 50, 1234)
	if block.Page != 2 || block.Limit != 50 || block.Total != 1234 {
		t.Fatalf("pagination = %+v, want page=2 limit=50 total=1234", block)
	}
}

// TestParseIncludeDeleted asserts the §6.0.1 read flag parsing.
func TestParseIncludeDeleted(t *testing.T) {
	h := newHarness(t)
	access, _ := h.login(t, "admin@dev001.io", "Admin@123")

	resp, body := h.do(t, http.MethodGet, "/api/v1/vehicles?include_deleted=maybe", access, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %v)", resp.StatusCode, body)
	}
	if got := errorCode(t, body); got != CodeValidationError {
		t.Fatalf("error_code = %q, want %s", got, CodeValidationError)
	}
}

// TestValidVehicleStatus asserts the status whitelist helper.
func TestValidVehicleStatus(t *testing.T) {
	for _, status := range []string{"active", "inactive", "maintenance"} {
		if !validVehicleStatus(status) {
			t.Fatalf("validVehicleStatus(%q) = false", status)
		}
	}
	for _, status := range []string{"", "deleted", "ACTIVE"} {
		if validVehicleStatus(status) {
			t.Fatalf("validVehicleStatus(%q) = true", status)
		}
	}
}

// TestSlugHelpers asserts the config list parser.
func TestSlugHelpers(t *testing.T) {
	got := splitList("http://a, http://b ,, ")
	if len(got) != 2 || got[0] != "http://a" || got[1] != "http://b" {
		t.Fatalf("splitList = %v, want [http://a http://b]", got)
	}
	if len(splitList("")) != 0 {
		t.Fatalf("splitList(\"\") should be empty")
	}
}
