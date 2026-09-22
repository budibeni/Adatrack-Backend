package controllers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims is the JWT payload — the SAME claim set service-websocket issues
// (PRD §9.1: every REST service validates the identical token).
type Claims struct {
	UserID      int64   `json:"user_id"`
	Email       string  `json:"email"`
	Role        string  `json:"role"`
	CompanyCode string  `json:"company_code"`
	GlobalRole  string  `json:"global_role"`
	VehicleIDs  []int64 `json:"vehicle_ids,omitempty"`
	jwt.RegisteredClaims
}

// AuthService validates access tokens (HS256) and answers revocation lookups.
type AuthService struct {
	settings Settings
	kv       KVStore
}

// NewAuthService wires the verifier.
func NewAuthService(settings Settings, kv KVStore) *AuthService {
	return &AuthService{settings: settings, kv: kv}
}

// ParseAccessToken verifies signature, issuer and expiry with the clock skew.
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

// Revoked reports whether the token jti is denylisted (FR-5.7). Disabled
// revocation short-circuits (dev only).
func (a *AuthService) Revoked(ctx context.Context, claims *Claims) (bool, error) {
	if !a.settings.RevocationEnabled || claims.ID == "" || a.kv == nil {
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

// denylistTTL returns the remaining validity window (diagnostics).
func (a *AuthService) denylistTTL(claims *Claims) time.Duration {
	if claims.ExpiresAt == nil {
		return time.Hour
	}
	return time.Until(claims.ExpiresAt.Time)
}
