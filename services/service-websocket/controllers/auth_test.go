package controllers

import (
	"testing"
	"time"

	"ajb_gps/internal"
	"ajb_gps/service-websocket/models"
)

func testConfig() *internal.Config {
	cfg := &internal.Config{}
	cfg.JWT.Secret = "unit-test-secret"
	cfg.JWT.Expiry = time.Hour
	cfg.RateLimit.LoginMaxAttempts = 5
	cfg.RateLimit.LoginWindow = 15 * time.Minute
	return cfg
}

func mu() models.MasterUser {
	return models.MasterUser{
		ID: 1, CompanyID: 7, CompanyCode: "DEV001",
		Email: "admin@dev001.io", Role: "Admin", Status: "active",
	}
}

func TestSignAndParseTokenRoundTrip(t *testing.T) {
	cfg := testConfig()
	m := mu()
	token, expSec, err := signToken(cfg, m, "Admin", []int64{1, 2, 3})
	if err != nil {
		t.Fatalf("signToken error: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty token")
	}
	if expSec <= 0 || expSec > int64(cfg.JWT.Expiry.Seconds()) {
		t.Fatalf("unexpected expires_in: %d", expSec)
	}

	claims, err := parseToken(cfg, token)
	if err != nil {
		t.Fatalf("parseToken error: %v", err)
	}
	if claims.UserID != 1 {
		t.Errorf("expected user_id 1, got %d", claims.UserID)
	}
	if claims.CompanyCode != "DEV001" {
		t.Errorf("expected company DEV001, got %s", claims.CompanyCode)
	}
	if claims.Role != "Admin" {
		t.Errorf("expected role Admin, got %s", claims.Role)
	}
	if len(claims.VehicleIDs) != 3 {
		t.Errorf("expected 3 vehicle_ids, got %d", len(claims.VehicleIDs))
	}
	if claims.ExpiresAt == nil || !claims.ExpiresAt.After(time.Now()) {
		t.Error("expected future expiry")
	}
}

// Tampered token harus ditolak.
func TestParseTokenRejectsTamperedToken(t *testing.T) {
	cfg := testConfig()
	token, _, err := signToken(cfg, mu(), "Admin", nil)
	if err != nil {
		t.Fatalf("signToken error: %v", err)
	}
	if _, err := parseToken(cfg, token+"x"); err == nil {
		t.Fatal("expected error for tampered token")
	}
}

// Token yang sudah kedaluwarsa harus ditolak.
func TestParseTokenRejectsExpired(t *testing.T) {
	cfg := testConfig()
	cfg2 := &internal.Config{}
	cfg2.JWT.Secret = cfg.JWT.Secret
	cfg2.JWT.Expiry = -1 * time.Hour
	token, _, err := signToken(cfg2, mu(), "Admin", nil)
	if err != nil {
		t.Fatalf("signToken error: %v", err)
	}
	if _, err := parseToken(cfg, token); err == nil {
		t.Fatal("expected error for expired token")
	}
}

// Token yang ditandatangani secret lain harus ditolak.
func TestParseTokenRejectsDifferentSecret(t *testing.T) {
	cfg := testConfig()
	other := testConfig()
	other.JWT.Secret = "different-secret"
	token, _, err := signToken(cfg, mu(), "Admin", nil)
	if err != nil {
		t.Fatalf("signToken error: %v", err)
	}
	if _, err := parseToken(other, token); err == nil {
		t.Fatal("expected error for token signed with a different secret")
	}
}

// Token tanpa company_code harus ditolak.
func TestParseTokenRejectsMissingCompany(t *testing.T) {
	cfg := testConfig()
	m := mu()
	m.CompanyCode = ""
	token, _, err := signToken(cfg, m, "Admin", nil)
	if err != nil {
		t.Fatalf("signToken error: %v", err)
	}
	if _, err := parseToken(cfg, token); err == nil {
		t.Fatal("expected error for token without company_code")
	}
}

// Login rate limiter: 5 kegagalan → 6th ditolak; reset memulihkan.
func TestFailureRateLimiter(t *testing.T) {
	l := newFailureRateLimiter(5, time.Minute)
	if !l.allow("1.2.3.4") {
		t.Fatal("expected allow before any failures")
	}
	for i := 0; i < 5; i++ {
		l.recordFailure("1.2.3.4")
	}
	if l.allow("1.2.3.4") {
		t.Fatal("expected limit reached")
	}
	if !l.allow("5.6.7.8") {
		t.Fatal("different IP should not be blocked")
	}
	l.reset("1.2.3.4")
	if !l.allow("1.2.3.4") {
		t.Fatal("expected allow after reset")
	}
}

// Role & override helper.
func TestEffectiveRole(t *testing.T) {
	if got := effectiveRole("Manager", ""); got != "Manager" {
		t.Errorf("expected Manager, got %s", got)
	}
	if got := effectiveRole("Manager", "Admin"); got != "Admin" {
		t.Errorf("expected Admin (override), got %s", got)
	}
	if !isAdminRole("Admin") || !isAdminRole("admin") || !isAdminRole("ADMIN") {
		t.Error("expected isAdminRole case-insensitive")
	}
	if isAdminRole("Manager") {
		t.Error("Manager should not be admin")
	}
}

// Redis live-state key harus sama persis dengan worker-live (PRD §7).
func TestRedisVehicleStateKey(t *testing.T) {
	t.Setenv("REDIS_KEY_PREFIX", "adatrack_gps:")
	if got := redisVehicleStateKey("DEV001", "864201040512345"); got != "adatrack_gps:dev001:vehicle:state:864201040512345" {
		t.Errorf("unexpected redis key: %s", got)
	}
}
