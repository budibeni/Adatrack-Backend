package controllers

import (
	"strings"
	"testing"
	"time"
)

// settingsEnvKeys are every variable LoadSettings reads (PRD §7.2).
var settingsEnvKeys = []string{
	"API_VEHICLE_HTTP_ADDR", "JWT_SECRET", "JWT_ISSUER", "JWT_CLOCK_SKEW_SEC",
	"JWT_REVOCATION_ENABLED", "AUTH_DENYLIST_PREFIX", "API_RATE_LIMIT",
	"API_RATE_WINDOW_SEC", "API_DEFAULT_PAGE_SIZE", "API_MAX_PAGE_SIZE",
	"API_MAX_BODY_BYTES", "WS_ALLOWED_ORIGINS", "WS_ALLOW_EMPTY_ORIGIN",
}

// clearSettingsEnv blanks every variable so the dev host environment can never
// leak into the assertions (an empty value is treated as unset everywhere).
func clearSettingsEnv(t *testing.T) {
	t.Helper()
	for _, k := range settingsEnvKeys {
		t.Setenv(k, "")
	}
}

// TestLoadSettingsDefaults pins the dev-safe defaults of PRD §7.2.
func TestLoadSettingsDefaults(t *testing.T) {
	clearSettingsEnv(t)
	s := LoadSettings()

	if s.HTTPAddr != ":8081" {
		t.Errorf("HTTPAddr = %q, want :8081", s.HTTPAddr)
	}
	if s.JWTSecret != "" {
		t.Errorf("JWTSecret = %q, want empty (fail-closed)", s.JWTSecret)
	}
	if s.JWTIssuer != "adatrack" {
		t.Errorf("JWTIssuer = %q, want adatrack", s.JWTIssuer)
	}
	if s.JWTClockSkew != 30*time.Second {
		t.Errorf("JWTClockSkew = %v, want 30s", s.JWTClockSkew)
	}
	if !s.RevocationEnabled {
		t.Error("revocation must default to enabled (FR-5.7)")
	}
	if s.DenylistPrefix != "adatrack_gps:auth:denylist:" {
		t.Errorf("DenylistPrefix = %q", s.DenylistPrefix)
	}
	if s.APIRateLimit != 100 || s.APIRateWindow != time.Minute {
		t.Errorf("rate limit = %d/%v, want 100/1m (PRD §8.4)", s.APIRateLimit, s.APIRateWindow)
	}
	if s.DefaultPageSize != 100 || s.MaxPageSize != 1000 {
		t.Errorf("pagination = %d/%d, want 100/1000", s.DefaultPageSize, s.MaxPageSize)
	}
	if s.MaxBodyBytes != 1<<20 {
		t.Errorf("MaxBodyBytes = %d, want %d", s.MaxBodyBytes, 1<<20)
	}
	if !s.OriginAllowed("http://localhost:3000") || !s.OriginAllowed("http://127.0.0.1:3000") {
		t.Errorf("default CORS allowlist = %v, want the two dev dashboards", s.AllowedOrigins)
	}
	if !s.AllowEmptyOrigin {
		t.Error("AllowEmptyOrigin must default to true for non-browser clients")
	}
}

// TestLoadSettingsEnvOverrides asserts every knob is actually read from the
// environment (single source of configuration, PRD §7.1).
func TestLoadSettingsEnvOverrides(t *testing.T) {
	clearSettingsEnv(t)
	t.Setenv("API_VEHICLE_HTTP_ADDR", ":9999")
	t.Setenv("JWT_SECRET", strings.Repeat("k", 48))
	t.Setenv("JWT_ISSUER", "adatrack-test")
	t.Setenv("JWT_CLOCK_SKEW_SEC", "5")
	t.Setenv("JWT_REVOCATION_ENABLED", "false")
	t.Setenv("AUTH_DENYLIST_PREFIX", "custom:denylist:")
	t.Setenv("API_RATE_LIMIT", "7")
	t.Setenv("API_RATE_WINDOW_SEC", "30")
	t.Setenv("API_DEFAULT_PAGE_SIZE", "25")
	t.Setenv("API_MAX_PAGE_SIZE", "50")
	t.Setenv("API_MAX_BODY_BYTES", "2048")
	t.Setenv("WS_ALLOWED_ORIGINS", "https://fleet.example.com, https://ops.example.com,")
	t.Setenv("WS_ALLOW_EMPTY_ORIGIN", "0")

	s := LoadSettings()
	if s.HTTPAddr != ":9999" || s.JWTIssuer != "adatrack-test" || s.JWTClockSkew != 5*time.Second {
		t.Errorf("address/issuer/skew not honoured: %+v", s)
	}
	if s.RevocationEnabled || s.DenylistPrefix != "custom:denylist:" {
		t.Errorf("revocation knobs not honoured: %+v", s)
	}
	if s.APIRateLimit != 7 || s.APIRateWindow != 30*time.Second {
		t.Errorf("rate limit not honoured: %d/%v", s.APIRateLimit, s.APIRateWindow)
	}
	if s.DefaultPageSize != 25 || s.MaxPageSize != 50 || s.MaxBodyBytes != 2048 {
		t.Errorf("pagination/body limits not honoured: %+v", s)
	}
	if len(s.AllowedOrigins) != 2 || s.AllowEmptyOrigin {
		t.Errorf("CORS list = %v (allowEmpty=%v), want 2 origins + false", s.AllowedOrigins, s.AllowEmptyOrigin)
	}
	if err := s.Validate(); err != nil {
		t.Errorf("a fully valid configuration must pass Validate: %v", err)
	}
}

// TestSettingsValidate covers the fail-closed rules of PRD §9.1/§10.2: no
// implicit signing key, no empty issuer, no negative bounds.
func TestSettingsValidate(t *testing.T) {
	base := func() Settings {
		return Settings{
			HTTPAddr:        ":8081",
			JWTSecret:       strings.Repeat("s", MinJWTSecretLen),
			JWTIssuer:       "adatrack",
			APIRateLimit:    100,
			MaxPageSize:     1000,
			DefaultPageSize: 100,
		}
	}

	if err := base().Validate(); err != nil {
		t.Fatalf("baseline configuration must be valid: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*Settings)
		want   string
	}{
		{"empty address", func(s *Settings) { s.HTTPAddr = " " }, "API_VEHICLE_HTTP_ADDR"},
		{"missing secret", func(s *Settings) { s.JWTSecret = "" }, "JWT_SECRET"},
		{"short secret", func(s *Settings) { s.JWTSecret = strings.Repeat("s", MinJWTSecretLen-1) }, "JWT_SECRET"},
		{"empty issuer", func(s *Settings) { s.JWTIssuer = "" }, "JWT_ISSUER"},
		{"negative rate limit", func(s *Settings) { s.APIRateLimit = -1 }, "negative"},
		{"negative page size", func(s *Settings) { s.MaxPageSize = -5 }, "negative"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := base()
			tc.mutate(&s)
			err := s.Validate()
			if err == nil {
				t.Fatalf("%s must be rejected", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// TestSettingsOriginAllowed asserts the CORS allowlist is compare-normalised but
// never widened (no wildcard, PRD §9.3).
func TestSettingsOriginAllowed(t *testing.T) {
	s := Settings{AllowedOrigins: []string{"https://fleet.example.com", " http://localhost:3000 "}}

	cases := []struct {
		origin string
		want   bool
	}{
		{"https://fleet.example.com", true},
		{"HTTPS://FLEET.EXAMPLE.COM", true},
		{"http://localhost:3000", true},
		{"https://evil.example.com", false},
		{"", false},
		{"*", false},
	}
	for _, tc := range cases {
		if got := s.OriginAllowed(tc.origin); got != tc.want {
			t.Errorf("OriginAllowed(%q) = %v, want %v", tc.origin, got, tc.want)
		}
	}
	if got := (Settings{}).OriginAllowed("https://fleet.example.com"); got {
		t.Error("an empty allowlist must deny every origin")
	}
}

// TestSplitList covers the comma-list parsing (blank entries dropped).
func TestSplitList(t *testing.T) {
	if got := splitList("   "); got != nil {
		t.Errorf("blank list = %v, want nil", got)
	}
	got := splitList("a, b ,,  c,")
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("splitList = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("splitList = %v, want %v", got, want)
		}
	}
}
