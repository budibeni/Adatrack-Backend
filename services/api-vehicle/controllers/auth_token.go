package controllers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"ajb_gps/api-vehicle/models"
	"ajb_gps/internal"
	"ajb_gps/internal/tokenauth"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// Phase B4 hardening (GAP #12): refresh token + rotasi + revocation JWT.
//
//   POST /api/v1/auth/refresh  {refresh_token} → access+refresh BARU (rotasi)
//   POST /api/v1/auth/logout   bearer/refresh  → jti di-denylist + refresh dihapus
//
// Access tetap HS256 dengan klaim identik B2/B3 + jti agar bisa direvoke.
// ---------------------------------------------------------------------------

var tokenMgr *tokenauth.Manager

// getTokenManager lazily builds the shared token store on top of Redis.
func getTokenManager() *tokenauth.Manager {
	if tokenMgr == nil {
		tokenMgr = tokenauth.New(appRedis.Client(), appCfg.Redis.KeyPrefix)
	}
	return tokenMgr
}

// newJTI returns a 16-byte random hex identifier for a JWT.
func newJTI() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return hex.EncodeToString([]byte(time.Now().String()))[:32]
	}
	return hex.EncodeToString(b)
}

// issueTokenPair signs a fresh access JWT (dengan jti) dan membuat refresh
// token baru untuk identitas yang diberikan.
func issueTokenPair(ctx context.Context, mu models.MasterUser, role string, vehicleIDs []int64) (access string, expSec int64, refresh string, err error) {
	access, expSec, err = signTokenClaims(mu, role, vehicleIDs)
	if err != nil {
		return "", 0, "", err
	}
	refresh, rerr := getTokenManager().IssueRefresh(ctx, tokenauth.Payload{
		UserID:      mu.ID,
		CompanyCode: mu.CompanyCode,
		Email:       mu.Email,
		Role:        role,
		VehicleIDs:  vehicleIDs,
	}, appCfg.JWT.RefreshExpiry)
	if rerr != nil {
		slog.Error("issue refresh token failed", "error", rerr, "user_id", mu.ID)
		// Access tetap diterbitkan; refresh kosong berarti fitur degraded —
		// kegagalan TIDAK silent (log error di atas).
		return access, expSec, "", nil
	}
	return access, expSec, refresh, nil
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// authRefreshHandler rotates a refresh token into a brand-new token pair.
func authRefreshHandler(c *gin.Context) {
	var req refreshRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.RefreshToken) == "" {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "refresh_token is required")
		return
	}

	tm := getTokenManager()
	ctx := c.Request.Context()
	payload, err := tm.ResolveRefresh(ctx, req.RefreshToken)
	if err != nil {
		if errors.Is(err, tokenauth.ErrInvalidToken) {
			writeError(c, http.StatusUnauthorized, "UNAUTHORIZED", "invalid or expired refresh token")
			return
		}
		slog.Error("refresh resolve failed", "error", err)
		writeError(c, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "service temporarily unavailable")
		return
	}

	newRefresh, err := tm.RotateRefresh(ctx, req.RefreshToken, appCfg.JWT.RefreshExpiry)
	if err != nil {
		if errors.Is(err, tokenauth.ErrInvalidToken) {
			writeError(c, http.StatusUnauthorized, "UNAUTHORIZED", "invalid or expired refresh token")
			return
		}
		slog.Error("refresh rotation failed", "error", err, "user_id", payload.UserID)
		writeError(c, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "service temporarily unavailable")
		return
	}

	// Bangun ulang MasterUser minimal untuk menandatangani access token baru.
	mu := models.MasterUser{
		ID:          payload.UserID,
		CompanyCode: payload.CompanyCode,
		Email:       payload.Email,
		Status:      "active",
	}
	token, expSec, err := signTokenClaims(mu, payload.Role, payload.VehicleIDs)
	if err != nil {
		slog.Error("refresh: sign token failed", "error", err, "user_id", payload.UserID)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	internal.LogAudit(auditDB(), internal.AuditEntry{
		UserID:      payload.UserID,
		CompanyCode: payload.CompanyCode,
		EventType:   "TOKEN_REVOKED",
		Action:      "auth.refresh",
		Entity:      "refresh_token",
		IP:          c.ClientIP(),
		UserAgent:   c.Request.UserAgent(),
	})

	writeSuccess(c, http.StatusOK, models.LoginResponse{
		Token:            token,
		TokenType:        "Bearer",
		ExpiresIn:        expSec,
		RefreshToken:     newRefresh,
		RefreshExpiresIn: int64(appCfg.JWT.RefreshExpiry.Seconds()),
		User: models.AuthUserPayload{
			ID:          payload.UserID,
			CompanyCode: payload.CompanyCode,
			Email:       payload.Email,
			Role:        payload.Role,
		},
	})
}

// authLogoutHandler revokes the presented access token (jti denylist) and/or
// refresh token. Sengaja TIDAK memakai requireAuth agar klien tetap bisa
// logout walau access token sudah kedaluwarsa.
func authLogoutHandler(c *gin.Context) {
	ctx := c.Request.Context()

	var body refreshRequest
	hasEffect := false
	if err := c.ShouldBindJSON(&body); err == nil && strings.TrimSpace(body.RefreshToken) != "" {
		if err := getTokenManager().RevokeRefresh(ctx, body.RefreshToken); err != nil {
			slog.Warn("logout: revoke refresh gagal", "error", err)
		}
		hasEffect = true
	}

	if tok := extractToken(c); tok != "" {
		if claims, err := parseToken(tok); err == nil && claims.RegisteredClaims.ID != "" {
			ttl := time.Until(claims.RegisteredClaims.ExpiresAt.Time)
			if err := getTokenManager().DenyJTI(ctx, claims.RegisteredClaims.ID, ttl); err != nil {
				slog.Error("logout: deny jti gagal", "error", err, "jti", claims.RegisteredClaims.ID)
				writeError(c, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "service temporarily unavailable")
				return
			}
			internal.LogAudit(auditDB(), internal.AuditEntry{
				UserID:      claims.UserID,
				CompanyCode: claims.CompanyCode,
				EventType:   "TOKEN_REVOKED",
				Action:      "auth.logout",
				Entity:      "access_token",
				EntityID:    claims.RegisteredClaims.ID,
				IP:          c.ClientIP(),
				UserAgent:   c.Request.UserAgent(),
			})
			hasEffect = true
		}
	}

	if !hasEffect {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "provide bearer token and/or refresh_token")
		return
	}
	writeSuccess(c, http.StatusOK, gin.H{"message": "logged out"})
}
