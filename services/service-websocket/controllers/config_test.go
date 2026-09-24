package controllers

import (
	"strings"
	"testing"
	"time"
)

// TestSettingsValidateRequiresJWTSecret asserts the fail-fast rule: the service
// never boots with an implicit signing key.
func TestSettingsValidateRequiresJWTSecret(t *testing.T) {
	base := Settings{
		HTTPAddr:         ":8082",
		JWTSecret:        strings.Repeat("a", MinJWTSecretLen),
		JWTIssuer:        "adatrack",
		AccessExpiry:     time.Hour,
		RefreshExpiry:    time.Hour,
		BcryptCost:       12,
		DefaultPageSize:  100,
		MaxPageSize:      1000,
		WSMaxQueueSize:   1000,
		WSMaxConnections: 5000,

		// B7.3/B7.4 playback + geocoding (the loader always fills these).
		PlaybackMaxPoints:     20000,
		PlaybackToleranceM:    10,
		PlaybackMaxToleranceM: 1000,
		GeocodeIndexRefresh:   6 * time.Hour,
		GeocodeCacheTTL:       6 * time.Hour,
		GeocodeMaxDistanceKM:  75,
		GeocodeSpecificMaxKM:  30,
		GeocodeMaxPoints:      500,
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid settings rejected: %v", err)
	}

	t.Run("empty secret", func(t *testing.T) {
		cfg := base
		cfg.JWTSecret = ""
		if err := cfg.Validate(); err == nil {
			t.Fatalf("empty JWT_SECRET accepted")
		}
	})

	t.Run("short secret", func(t *testing.T) {
		cfg := base
		cfg.JWTSecret = "too-short"
		if err := cfg.Validate(); err == nil {
			t.Fatalf("short JWT_SECRET accepted")
		}
	})

	t.Run("weak bcrypt cost", func(t *testing.T) {
		cfg := base
		cfg.BcryptCost = 4
		if err := cfg.Validate(); err == nil {
			t.Fatalf("BCRYPT_COST below the PRD §9.1 minimum accepted")
		}
	})

	t.Run("page size inversion", func(t *testing.T) {
		cfg := base
		cfg.DefaultPageSize = 5000
		cfg.MaxPageSize = 100
		if err := cfg.Validate(); err == nil {
			t.Fatalf("default page size > max accepted")
		}
	})

	t.Run("zero ws queue", func(t *testing.T) {
		cfg := base
		cfg.WSMaxQueueSize = 0
		if err := cfg.Validate(); err == nil {
			t.Fatalf("WS_MAX_QUEUE=0 accepted")
		}
	})

	t.Run("expired token lifetime", func(t *testing.T) {
		cfg := base
		cfg.AccessExpiry = 0
		if err := cfg.Validate(); err == nil {
			t.Fatalf("JWT_EXPIRY_HOURS=0 accepted")
		}
	})

	// B7.3/B7.4: impossible playback/geocoding values are rejected, zero keeps
	// the documented defaults (see the *_FALLBACK constants in the geocoder).
	t.Run("negative playback tolerance", func(t *testing.T) {
		cfg := base
		cfg.PlaybackToleranceM = -1
		if err := cfg.Validate(); err == nil {
			t.Fatalf("negative PLAYBACK_TOLERANCE_M accepted")
		}
	})

	t.Run("negative geocode caps", func(t *testing.T) {
		cfg := base
		cfg.GeocodeMaxPoints = -5
		cfg.GeocodeMaxDistanceKM = -1
		if err := cfg.Validate(); err == nil {
			t.Fatalf("negative GEOCODE_* values accepted")
		}
	})
}

// TestLoadSettingsDefaults documents the PRD §7.2/§8.4 defaults.
func TestLoadSettingsDefaults(t *testing.T) {
	t.Setenv("HTTP_ADDR", "")
	t.Setenv("JWT_SECRET", "unit-test-secret-at-least-32-characters")
	t.Setenv("JWT_EXPIRY_HOURS", "")
	t.Setenv("JWT_REFRESH_EXPIRY_HOURS", "")
	t.Setenv("BCRYPT_COST", "")
	t.Setenv("WS_MAX_QUEUE", "")
	t.Setenv("API_MAX_PAGE_SIZE", "")

	cfg := LoadSettings()
	if cfg.HTTPAddr != ":8082" {
		t.Fatalf("HTTP_ADDR default = %q, want :8082 (PRD §14.2)", cfg.HTTPAddr)
	}
	if cfg.AccessExpiry != 24*time.Hour {
		t.Fatalf("access expiry = %v, want 24h (PRD §9.1)", cfg.AccessExpiry)
	}
	if cfg.RefreshExpiry != 168*time.Hour {
		t.Fatalf("refresh expiry = %v, want 168h (FR-5.7)", cfg.RefreshExpiry)
	}
	if cfg.BcryptCost != 12 {
		t.Fatalf("bcrypt cost = %d, want 12 (PRD §9.1)", cfg.BcryptCost)
	}
	if cfg.LoginRateLimit != 5 || cfg.LoginRateWindow != 15*time.Minute {
		t.Fatalf("login rate limit = %d/%v, want 5/15m (PRD §8.4)", cfg.LoginRateLimit, cfg.LoginRateWindow)
	}
	if cfg.APIRateLimit != 100 || cfg.APIRateWindow != time.Minute {
		t.Fatalf("api rate limit = %d/%v, want 100/1m (PRD §8.4)", cfg.APIRateLimit, cfg.APIRateWindow)
	}
	if cfg.WSMaxConnections != 5000 || cfg.WSMaxQueueSize != 1000 {
		t.Fatalf("ws limits = %d/%d, want 5000/1000 (FR-5.4)", cfg.WSMaxConnections, cfg.WSMaxQueueSize)
	}
	if cfg.WSPingInterval != 30*time.Second {
		t.Fatalf("ws ping interval = %v, want 30s (FR-5.3)", cfg.WSPingInterval)
	}
	if cfg.WSSendBufferSize != 256*1024 {
		t.Fatalf("ws send buffer = %d, want 256 KiB (FR-5.4)", cfg.WSSendBufferSize)
	}
	if !cfg.RevocationEnabled {
		t.Fatalf("JWT revocation default = false, want true (FR-5.7)")
	}
	if !cfg.AllowEmptyOrigin {
		t.Fatalf("empty Origin default = false, want true (FR-5.4 non-browser clients)")
	}
	if cfg.DefaultAdminPassword != "Admin@123" {
		t.Fatalf("default tenant admin password = %q, want Admin@123 (FR-5.5)", cfg.DefaultAdminPassword)
	}
}

// TestOriginAllowed asserts the FR-5.4 origin rules.
func TestOriginAllowed(t *testing.T) {
	cfg := Settings{
		AllowedOrigins:   []string{"https://app.adatrackgps.com"},
		AllowEmptyOrigin: true,
	}
	if !cfg.OriginAllowed("") {
		t.Fatalf("empty origin rejected although non-browser clients are allowed")
	}
	if !cfg.OriginAllowed("https://app.adatrackgps.com") {
		t.Fatalf("allowlisted origin rejected")
	}
	if cfg.OriginAllowed("https://evil.example.com") {
		t.Fatalf("non-allowlisted origin accepted")
	}

	strict := Settings{AllowedOrigins: nil, AllowEmptyOrigin: false}
	if strict.OriginAllowed("") {
		t.Fatalf("empty origin accepted while WS_ALLOW_EMPTY_ORIGIN=false")
	}
}

// TestDefaultAdminEmail asserts the FR-5.5 e-mail convention.
func TestDefaultAdminEmail(t *testing.T) {
	if got := defaultAdminEmail("acme", "local"); got != "admin@acme.local" {
		t.Fatalf("defaultAdminEmail = %q, want admin@acme.local", got)
	}
	if got := defaultAdminEmail("ACME", ""); got != "admin@acme.local" {
		t.Fatalf("defaultAdminEmail with empty domain = %q, want admin@acme.local", got)
	}
}
