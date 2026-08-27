package controllers

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"ajb_gps/internal"
	"ajb_gps/internal/tenant"
	"ajb_gps/service-media/models"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// ---------------------------------------------------------------------------
// RBAC middleware (FR-8.4, PRD §6) — pola identik api-vehicle/service-websocket:
//  1. AuthN : validate shared HS256 JWT (JWT_SECRET sama → token interop B2/B3).
//  2. Tenant: route ke adatrack_gps_{company_code}; cross-tenant → 403.
//  3. AuthZ : user_company_access is_active + role_override + user_vehicles
//     row-level. Admin company → akses semua media dalam company.
// ---------------------------------------------------------------------------

const (
	ctxUserKey        = "auth_user"
	ctxAllowedKey     = "auth_allowed_vehicle_ids"
	ctxAdminKey       = "auth_is_admin"
	ctxCompanyDBKey   = "auth_company_db"
	ctxCompanyROKey   = "auth_company_read_db"
	ctxCompanyCodeKey = "auth_company_code"
	ctxRoleKey        = "auth_role"
)

// tokenClaims is the JWT payload — IDENTICAL ke service-websocket/api-vehicle.
type tokenClaims struct {
	UserID      uint64  `json:"user_id"`
	CompanyCode string  `json:"company_code"`
	CompanyID   int64   `json:"company_id,omitempty"`
	Email       string  `json:"email"`
	Role        string  `json:"role"`
	VehicleIDs  []int64 `json:"vehicle_ids,omitempty"`
	jwt.RegisteredClaims
}

// isAdminRole reports whether an effective role is Admin (case-insensitive).
func isAdminRole(role string) bool {
	return strings.EqualFold(strings.TrimSpace(role), "Admin")
}

// parseToken validates a JWT and returns its claims (no login endpoint here —
// tokens are issued by service-websocket / api-vehicle and validated as-is).
func parseToken(tokenStr string) (*tokenClaims, error) {
	claims := &tokenClaims{}
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(appCfg.JWT.Secret), nil
	})
	if err != nil {
		return nil, err
	}
	if !token.Valid {
		return nil, errors.New("invalid token")
	}
	if claims.CompanyCode == "" {
		return nil, errors.New("token missing company_code")
	}
	return claims, nil
}

// extractToken reads a Bearer token from the Authorization header (ws ?token=).
func extractToken(c *gin.Context) string {
	h := c.GetHeader("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	return c.Query("token")
}

// effectiveRole derives the per-company role: role_override bila ada.
func effectiveRole(globalRole, roleOverride string) string {
	if strings.TrimSpace(roleOverride) != "" {
		return roleOverride
	}
	return globalRole
}

// loadCompanyAccess loads the per-company registry row (user_company_access).
func loadCompanyAccess(db *sql.DB, masterUserID uint64) (id, companyUserID uint64, roleOverride string, isActive bool, err error) {
	var role sql.NullString
	err = db.QueryRow(`SELECT id, user_id, role_override, is_active
FROM user_company_access WHERE user_id = ? LIMIT 1`, masterUserID).
		Scan(&id, &companyUserID, &role, &isActive)
	if err != nil {
		return 0, 0, "", false, err
	}
	if role.Valid {
		roleOverride = role.String
	}
	return id, companyUserID, roleOverride, isActive, nil
}

// userVehicleIDs returns vehicle IDs assigned to the user (user_vehicles).
func userVehicleIDs(db *sql.DB, userID uint64) (map[uint64]struct{}, error) {
	set := map[uint64]struct{}{}
	rows, err := db.Query(`SELECT vehicle_id FROM user_vehicles WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uint64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		set[id] = struct{}{}
	}
	return set, rows.Err()
}

// authorize resolves the tenant DB + role + vehicle scope from JWT claims.
func authorize(claims *tokenClaims) (allowed map[uint64]struct{}, db *sql.DB,
	companyUserID uint64, role string, admin bool, err error) {

	// Platform tier (PRD §6.1 governance): konteks 'default' berada DI LUAR
	// semua tenant. service-media tidak mengekspos endpoint platform.
	if tenant.IsPlatformCompany(claims.CompanyCode) {
		return nil, nil, 0, "", false, errors.New("platform tokens have no tenant scope")
	}

	db, err = appTenant.DB(claims.CompanyCode)
	if err != nil {
		return nil, nil, 0, "", false, err
	}
	_, ucaUserID, roleOverride, isActive, err := loadCompanyAccess(db, claims.UserID)
	if err != nil {
		return nil, nil, 0, "", false, err
	}
	if !isActive {
		return nil, nil, 0, "", false, errors.New("company access inactive")
	}
	role = effectiveRole(claims.Role, roleOverride)
	admin = isAdminRole(role)
	companyUserID = ucaUserID

	allowed = map[uint64]struct{}{}
	if !admin {
		set, err := userVehicleIDs(db, claims.UserID)
		if err != nil {
			return nil, nil, 0, "", false, err
		}
		allowed = set
	}
	return allowed, db, ucaUserID, role, admin, nil
}

// installAuthContext stores the resolved RBAC state on the gin context.
func installAuthContext(c *gin.Context, claims *tokenClaims, allowed map[uint64]struct{},
	db *sql.DB, companyUserID uint64, role string, admin bool) {
	c.Set(ctxCompanyDBKey, db)
	if ro, rerr := appTenant.ReadPool(claims.CompanyCode); rerr == nil && ro != nil {
		c.Set(ctxCompanyROKey, ro)
	} else {
		c.Set(ctxCompanyROKey, db)
	}
	c.Set(ctxCompanyCodeKey, claims.CompanyCode)
	c.Set(ctxAllowedKey, allowed)
	c.Set(ctxAdminKey, admin)
	c.Set(ctxRoleKey, role)
	c.Set(ctxUserKey, models.AuthUser{
		ID:            claims.UserID,
		CompanyCode:   claims.CompanyCode,
		CompanyID:     claims.CompanyID,
		Email:         claims.Email,
		Role:          role,
		CompanyUserID: companyUserID,
	})
}

// requireAuth validates the bearer JWT and installs RBAC context (FR-8.4).
func requireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenStr := extractToken(c)
		if tokenStr == "" {
			writeError(c, http.StatusUnauthorized, "UNAUTHORIZED", "missing bearer token")
			c.Abort()
			return
		}
		claims, err := parseToken(tokenStr)
		if err != nil {
			slog.Warn("auth failed", "error", err.Error())
			writeError(c, http.StatusUnauthorized, "UNAUTHORIZED", "invalid or expired token")
			c.Abort()
			return
		}
		start := time.Now()
		allowed, db, ucaID, role, admin, err := authorize(claims)
		if err != nil {
			if errors.Is(err, tenant.ErrCompanyNotFound) {
				slog.Warn("cross-tenant access denied", "company", claims.CompanyCode, "user_id", claims.UserID)
				internal.RBACDenialsTotal.WithLabelValues("request", "cross_tenant").Inc()
				writeError(c, http.StatusForbidden, "FORBIDDEN", "cross-tenant access is not allowed")
			} else {
				slog.Error("rbac: authorize failed", "error", err, "user_id", claims.UserID, "company", claims.CompanyCode)
				writeError(c, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "service temporarily unavailable")
			}
			c.Abort()
			return
		}
		if tenant.IsPlatformCompany(claims.CompanyCode) {
			internal.RBACDenialsTotal.WithLabelValues(c.FullPath(), "platform_scope").Inc()
			writeError(c, http.StatusForbidden, "PLATFORM_SCOPE", "platform tokens may not access tenant APIs")
			c.Abort()
			return
		}
		installAuthContext(c, claims, allowed, db, ucaID, role, admin)
		_ = start
		c.Next()
	}
}