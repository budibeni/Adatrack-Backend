package controllers

import (
	"database/sql"
	"testing"
)

func TestContains(t *testing.T) {
	cases := []struct {
		s, sub string
		want   bool
	}{
		{"hello world", "world", true},
		{"hello world", "xyz", false},
		{"", "", true},
		{"abc", "", true},
		{"", "a", false},
		{"aaa", "aa", true},
		{"abc", "abcd", false},
	}
	for _, tc := range cases {
		if got := contains(tc.s, tc.sub); got != tc.want {
			t.Errorf("contains(%q,%q) = %v, want %v", tc.s, tc.sub, got, tc.want)
		}
	}
}

func TestRefCacheKey(t *testing.T) {
	got := refCacheKey("countries", "page1")
	if got != "adatrack_gps:ref:countries:page1" {
		t.Fatalf("refCacheKey = %q", got)
	}
}

func TestNullableStrP2EdgeCases(t *testing.T) {
	if got := nullableStrP2(sql.NullString{Valid: false}); got != nil {
		t.Fatalf("invalid should return nil, got %v", got)
	}
	got := nullableStrP2(sql.NullString{String: "", Valid: true})
	if got == nil || *got != "" {
		t.Fatalf("valid empty string should return empty ptr, got %v", got)
	}
}

func TestNullableIntPtrEdgeCases(t *testing.T) {
	if got := nullableIntPtr(sql.NullInt64{Valid: false}); got != nil {
		t.Fatalf("invalid should return nil, got %v", got)
	}
	got := nullableIntPtr(sql.NullInt64{Int64: 0, Valid: true})
	if got == nil || *got != 0 {
		t.Fatalf("zero should return ptr to 0, got %v", got)
	}
}
