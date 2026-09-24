package validate

// validate_test.go — B10 input-validation + anti-attack rules (PRD §8.5/§9.6).
//
// The suite pins the exact boundary behaviour: every rule must reject the
// attack-shaped input (control characters, wildcards in subjects, oversized text,
// out-of-range numbers) and accept the values the platform legitimately carries.

import (
	"errors"
	"strings"
	"testing"
)

func TestIMEI(t *testing.T) {
	if err := IMEI("864201040512345"); err != nil {
		t.Fatalf("valid IMEI rejected: %v", err)
	}
	cases := []struct {
		name  string
		value string
		want  error
	}{
		{"empty", "", ErrRequired},
		{"too short", "86420104051234", ErrBadFormat},
		{"too long", "8642010405123456", ErrBadFormat},
		{"non numeric", "86420104051234x", ErrBadFormat},
		{"whitespace", "86420 040512345", ErrBadFormat},
	}
	for _, tc := range cases {
		err := IMEI(tc.value)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
	// Surrounding whitespace is normalised, not rejected.
	if err := IMEI("  864201040512345 "); err != nil {
		t.Errorf("trimmed IMEI rejected: %v", err)
	}
}

func TestText(t *testing.T) {
	if err := Text("reason", "Oil change overdue", 1, 255, true, false); err != nil {
		t.Fatalf("valid text rejected: %v", err)
	}
	if err := Text("reason", "", 1, 255, false, false); err != nil {
		t.Fatalf("optional empty text rejected: %v", err)
	}
	if err := Text("reason", "", 1, 255, true, false); !errors.Is(err, ErrRequired) {
		t.Errorf("required empty text: err = %v, want ErrRequired", err)
	}
	if err := Text("reason", strings.Repeat("x", 256), 1, 255, true, false); !errors.Is(err, ErrTooLong) {
		t.Errorf("oversized text: err = %v, want ErrTooLong", err)
	}
	// Control characters are the injection vector (log/terminal, CRLF splitting).
	if err := Text("reason", "line1\r\nInjected: yes", 1, 255, true, false); !errors.Is(err, ErrControlChars) {
		t.Errorf("CRLF text: err = %v, want ErrControlChars", err)
	}
	if err := Text("notes", "line1\nline2", 1, 255, true, true); err != nil {
		t.Errorf("multi-line notes rejected: %v", err)
	}
	if err := Text("reason", "bell\x07", 1, 255, true, true); !errors.Is(err, ErrControlChars) {
		t.Errorf("BEL text: err = %v, want ErrControlChars", err)
	}
}

func TestCoordinates(t *testing.T) {
	if err := Coordinates(-6.2, 106.8); err != nil {
		t.Fatalf("valid coordinates rejected: %v", err)
	}
	if err := Coordinates(0, 0); err != nil {
		t.Fatalf("no-fix coordinates rejected: %v", err)
	}
	if err := Coordinates(90.1, 0); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("latitude 90.1: err = %v, want ErrOutOfRange", err)
	}
	if err := Coordinates(0, -180.5); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("longitude -180.5: err = %v, want ErrOutOfRange", err)
	}
}

func TestOneOfAndIntRange(t *testing.T) {
	if err := OneOf("command", "engine_cut", "engine_cut", "reboot"); err != nil {
		t.Fatalf("whitelisted value rejected: %v", err)
	}
	if err := OneOf("command", "open_trunk", "engine_cut", "reboot"); !errors.Is(err, ErrNotAllowed) {
		t.Errorf("non-whitelisted value: err = %v, want ErrNotAllowed", err)
	}
	if err := IntRange("interval_seconds", 20, 5, 86400); err != nil {
		t.Fatalf("in-range value rejected: %v", err)
	}
	if err := IntRange("interval_seconds", 1, 5, 86400); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("below-range value: err = %v, want ErrOutOfRange", err)
	}
}

func TestPagination(t *testing.T) {
	if err := Pagination(1, 100, 1000); err != nil {
		t.Fatalf("valid pagination rejected: %v", err)
	}
	if err := Pagination(0, 100, 1000); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("page 0: err = %v, want ErrOutOfRange", err)
	}
	if err := Pagination(1, 1001, 1000); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("limit above max: err = %v, want ErrOutOfRange", err)
	}
}

func TestSubjectToken(t *testing.T) {
	if err := SubjectToken("company", "DEV001"); err != nil {
		t.Fatalf("valid tenant code rejected: %v", err)
	}
	// A wildcard or a dot would let a crafted tenant code fan out or hijack a
	// neighbouring subject — the whole point of this rule.
	for _, bad := range []string{"DEV001.>", "dev001", "DEV 001", "DEV-001", "", strings.Repeat("A", 21)} {
		if err := SubjectToken("company", bad); err == nil {
			t.Errorf("SubjectToken(%q) accepted an unsafe tenant code", bad)
		}
	}
}

func TestSearchTerm(t *testing.T) {
	if err := SearchTerm("", 100); err != nil {
		t.Fatalf("empty search rejected: %v", err)
	}
	if err := SearchTerm("B 1234 ABC", 100); err != nil {
		t.Fatalf("valid search rejected: %v", err)
	}
	if err := SearchTerm(strings.Repeat("x", 101), 100); !errors.Is(err, ErrTooLong) {
		t.Errorf("oversized search: err = %v, want ErrTooLong", err)
	}
	if err := SearchTerm("a\tb", 100); !errors.Is(err, ErrControlChars) {
		t.Errorf("tab in search: err = %v, want ErrControlChars", err)
	}
}
