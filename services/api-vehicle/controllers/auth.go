package controllers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"adatrack_gps/api-vehicle/models"
)

// Claims is the JWT payload — the SAME claim set service-websocket issues
// (PRD §9.1: "the SAME claims must work for service-websocket and api-vehicle").
type Claims struct {
	UserID      int64   `json:"user_id"`
	Email       string  `json:"email"`
	Role        string  `json:"role"`
	CompanyCode string  `json:"company_code"`
	GlobalRole  string  `json:"global_role"`
	VehicleIDs  []int64 `json:"vehicle_ids,omitempty"`
	jwt.RegisteredClaims
}

// IsPlatform reports the platform (governance) identity (PRD §3.1).
func (c *Claims) IsPlatform() bool {
	return c.GlobalRole == models.RoleSuperAdmin && c.CompanyCode == models.PlatformCompanyCode
}

// AuthService validates access tokens issued by service-websocket (HS256) and
// answers revocation lookups against the shared Redis denylist (PRD §9.1/FR-5.7).
type AuthService struct {
	settings Settings
	kv       *RedisKV
}

// NewAuthService wires the verifier.
func NewAuthService(settings Settings, kv *RedisKV) *AuthService {
	return &AuthService{settings: settings, kv: kv}
}

// ParseAccessToken verifies signature, issuer, expiry and (optionally) the
// not-before window with the configured clock skew.
func (a *AuthService) ParseAccessToken(raw string) (*Claims, error) {
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{"HS256"}),
		jwt.WithIssuer(a.settings.JWTIssuer),
		jwt.WithLeeway(a.settings.JWTClockSkew),
		jwt.WithExpirationRequired(),
	)
	var claims Claims
	token, err := parser.ParseWithClaims(raw, &claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		return []byte(a.settings.JWTSecret), nil
	})
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, NewAPIError(401, CodeTokenExpired, "access token expired")
		}
		return nil, NewAPIError(401, CodeTokenInvalid, "invalid access token")
	}
	if !token.Valid {
		return nil, NewAPIError(401, CodeTokenInvalid, "invalid access token")
	}
	return &claims, nil
}

// Revoked reports whether the token jti is denylisted (logout revocation,
// FR-5.7). Disabled revocation short-circuits (dev only).
func (a *AuthService) Revoked(ctx context.Context, claims *Claims) (bool, error) {
	if !a.settings.RevocationEnabled || claims.ID == "" {
		return false, nil
	}
	val, err := a.kv.Get(ctx, a.settings.DenylistPrefix+claims.ID)
	if err != nil {
		return false, err
	}
	return val != "", nil
}

// RevokedTokenError is the 401 answer for a revoked token.
func (a *AuthService) RevokedTokenError() *APIError {
	return NewAPIError(401, CodeTokenRevoked, "access token has been revoked")
}

// denylistTTL returns how long a jti check window remains (informational).
func (a *AuthService) denylistTTL(claims *Claims) time.Duration {
	if claims.ExpiresAt == nil {
		return time.Hour
	}
	return time.Until(claims.ExpiresAt.Time)
}
