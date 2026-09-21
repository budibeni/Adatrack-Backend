package controllers

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"adatrack_gps/api-vehicle/models"
)

// authSettings is the verifier configuration shared by the auth/RBAC tests: the
// same HS256 secret + issuer service-websocket issues with (PRD §9.1).
func authSettings() Settings {
	return Settings{
		JWTSecret:         strings.Repeat("s", 40),
		JWTIssuer:         "adatrack",
		JWTClockSkew:      30 * time.Second,
		RevocationEnabled: true,
		DenylistPrefix:    "adatrack_gps:auth:denylist:",
	}
}

// validClaims builds an access-token claim set with the SAME shape
// service-websocket issues (PRD §9.1).
func validClaims(jti string) Claims {
	return Claims{
		UserID:      7,
		Email:       "op@test",
		Role:        models.RoleOperator,
		CompanyCode: "DEV001",
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        jti,
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-time.Minute)),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
}

// signToken signs a claim set with an explicit method/secret so the verifier
// rejection branches can be reproduced (wrong key, wrong alg, wrong issuer).
func signToken(t *testing.T, issuer, secret string, claims Claims, method jwt.SigningMethod) string {
	t.Helper()
	claims.Issuer = issuer
	raw, err := jwt.NewWithClaims(method, claims).SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return raw
}

// TestClaimsIsPlatform asserts the governance-tier identity rule (PRD §3.1):
// SuperAdmin WITHOUT a tenant context.
func TestClaimsIsPlatform(t *testing.T) {
	if !(&Claims{GlobalRole: models.RoleSuperAdmin, CompanyCode: models.PlatformCompanyCode}).IsPlatform() {
		t.Error("SuperAdmin + DEFAULT must be the platform identity")
	}
	if (&Claims{GlobalRole: models.RoleSuperAdmin, CompanyCode: "DEV001"}).IsPlatform() {
		t.Error("SuperAdmin INSIDE a tenant must not be the platform identity")
	}
	if (&Claims{GlobalRole: models.RoleAdmin, CompanyCode: models.PlatformCompanyCode}).IsPlatform() {
		t.Error("a tenant Admin must never be the platform identity")
	}
	if (&Claims{}).IsPlatform() {
		t.Error("an empty claim set must not be the platform identity")
	}
}

// TestParseAccessToken covers the happy path plus every rejection: expiry,
// foreign signature, foreign issuer, a non-HS256 algorithm and a missing `exp`.
func TestParseAccessToken(t *testing.T) {
	settings := authSettings()
	auth := NewAuthService(settings, nil)

	expired := validClaims("jti-exp")
	expired.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Hour))

	noExpiry := validClaims("jti-noexp")
	noExpiry.ExpiresAt = nil

	cases := []struct {
		name     string
		token    string
		wantCode string // "" = accepted
	}{
		{"valid", signToken(t, settings.JWTIssuer, settings.JWTSecret, validClaims("jti-1"), jwt.SigningMethodHS256), ""},
		{"expired", signToken(t, settings.JWTIssuer, settings.JWTSecret, expired, jwt.SigningMethodHS256), CodeTokenExpired},
		{"foreign signature", signToken(t, settings.JWTIssuer, strings.Repeat("x", 40), validClaims("jti-2"), jwt.SigningMethodHS256), CodeTokenInvalid},
		{"foreign issuer", signToken(t, "somebody-else", settings.JWTSecret, validClaims("jti-3"), jwt.SigningMethodHS256), CodeTokenInvalid},
		{"wrong algorithm", signToken(t, settings.JWTIssuer, settings.JWTSecret, validClaims("jti-4"), jwt.SigningMethodHS512), CodeTokenInvalid},
		{"no expiry", signToken(t, settings.JWTIssuer, settings.JWTSecret, noExpiry, jwt.SigningMethodHS256), CodeTokenInvalid},
		{"garbage", "not.a.jwt", CodeTokenInvalid},
		{"empty", "", CodeTokenInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			claims, err := auth.ParseAccessToken(tc.token)
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("valid token rejected: %v", err)
				}
				if claims.UserID != 7 || claims.CompanyCode != "DEV001" {
					t.Errorf("claims = %+v, want the signed identity", claims)
				}
				return
			}
			if err == nil {
				t.Fatal("token must be rejected")
			}
			apiErr, ok := err.(*APIError)
			if !ok {
				t.Fatalf("error = %T (%v), want *APIError", err, err)
			}
			if apiErr.Status != 401 || apiErr.Code != tc.wantCode {
				t.Errorf("got %d %s, want 401 %s", apiErr.Status, apiErr.Code, tc.wantCode)
			}
		})
	}
}

// TestAuthRevokedDenylist verifies the logout revocation lookup (FR-5.7): the
// token jti is looked up in the shared Redis denylist.
func TestAuthRevokedDenylist(t *testing.T) {
	settings := authSettings()
	kv, srv := newMiniredisKV(t)
	auth := NewAuthService(settings, kv)
	ctx := context.Background()

	claims := validClaims("jti-live")
	revoked, err := auth.Revoked(ctx, &claims)
	if err != nil {
		t.Fatalf("revoked lookup: %v", err)
	}
	if revoked {
		t.Error("a jti absent from the denylist must not be revoked")
	}

	srv.Set(settings.DenylistPrefix+"jti-live", "1")
	revoked, err = auth.Revoked(ctx, &claims)
	if err != nil {
		t.Fatalf("revoked lookup after logout: %v", err)
	}
	if !revoked {
		t.Error("a denylisted jti must be reported as revoked")
	}
}

// TestAuthRevokedShortCircuits documents the two cases that must NOT touch
// Redis: revocation disabled (dev) and a token without jti.
func TestAuthRevokedShortCircuits(t *testing.T) {
	ctx := context.Background()

	disabled := authSettings()
	disabled.RevocationEnabled = false
	// A nil KV would panic if it were consulted — that is the assertion.
	live := validClaims("jti")
	if revoked, err := NewAuthService(disabled, nil).Revoked(ctx, &live); err != nil || revoked {
		t.Fatalf("disabled revocation = (%v,%v), want (false,nil)", revoked, err)
	}

	kv, _ := newMiniredisKV(t)
	noJTI := validClaims("") // jti never issued → nothing to denylist
	if revoked, err := NewAuthService(authSettings(), kv).Revoked(ctx, &noJTI); err != nil || revoked {
		t.Fatalf("token without jti = (%v,%v), want (false,nil)", revoked, err)
	}
}

// TestAuthRevokedTokenError pins the 401 TOKEN_REVOKED contract.
func TestAuthRevokedTokenError(t *testing.T) {
	err := NewAuthService(authSettings(), nil).RevokedTokenError()
	if err.Status != 401 || err.Code != CodeTokenRevoked {
		t.Fatalf("got %d %s, want 401 %s", err.Status, err.Code, CodeTokenRevoked)
	}
}

// TestAuthDenylistTTL documents the informational jti window: one hour for a
// token without `exp`, the remaining lifetime otherwise.
func TestAuthDenylistTTL(t *testing.T) {
	auth := NewAuthService(authSettings(), nil)

	got := auth.denylistTTL(&Claims{})
	if got != time.Hour {
		t.Errorf("ttl without exp = %v, want 1h", got)
	}
	claims := validClaims("jti")
	got = auth.denylistTTL(&claims)
	if got <= 0 || got > time.Hour {
		t.Errorf("ttl = %v, want (0,1h]", got)
	}
}
