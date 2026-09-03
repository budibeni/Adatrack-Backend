package controllers

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"ajb_gps/api-vehicle/models"
	"ajb_gps/internal"
	"ajb_gps/internal/tenant"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// RBAC middleware (FR-5.1, PRD §3.1/§6) — pola identik service-websocket (B2):
// 1. AuthN : validate JWT (HS256, payload berisi company_code).
// 2. Tenant: route ke adatrack_gps_{company_code} via TenantManager; cross-tenant → 403.
// 3. AuthZ : user_company_access is_active + role_override + user_vehicles.
// ---------------------------------------------------------------------------

const (
	ctxUserKey        = "auth_user"
	ctxAllowedKey     = "auth_allowed_vehicle_ids" // map[uint64]struct{} (nil utk admin)
	ctxAdminKey       = "auth_is_admin"
	ctxCompanyDBKey   = "auth_company_db"
	ctxCompanyROKey   = "auth_company_read_db" // B4 HA: pool READ (replica-preferred)
	ctxCompanyCodeKey = "auth_company_code"
	ctxRoleKey        = "auth_role"
)

// isAdminRole reports whether an effective role is Admin (case-insensitive).
func isAdminRole(role string) bool {
	return strings.EqualFold(strings.TrimSpace(role), "Admin")
}

// isManagerRole reports whether an effective role is Manager/adatrack Manager.
func isManagerRole(role string) bool {
	r := strings.ToLower(strings.TrimSpace(role))
	return r == "manager" || r == "adatrack_manager"
}

// authorize resolves the tenant DB + role + vehicle scope from JWT claims.
// allowed berisi vehicle yang boleh diakses; kosong untuk non-admin tanpa
// assignment; Admin → set kosong + flag admin true.
func authorize(claims *tokenClaims) (allowed map[uint64]struct{}, db *sql.DB,
	companyUserID uint64, role string, admin bool, err error) {

	// Platform tier (PRD §6.1 governance): konteks 'default' berada DI LUAR
	// semua tenant. api-vehicle tidak mengekspos endpoint platform, sehingga
	// token platform ditolak di sini lebih awal (requireAuth mengembalikan
	// 403 PLATFORM_SCOPE) — admin tenant tidak pernah boleh berpindah konteks.
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

	allowed = make(map[uint64]struct{})
	if !admin {
		ids, err := userVehicleIDs(db, claims.UserID)
		if err != nil {
			return nil, nil, 0, "", false, err
		}
		for _, id := range ids {
			allowed[uint64(id)] = struct{}{}
		}
	}
	return allowed, db, ucaUserID, role, admin, nil
}

// installAuthContext stores the resolved RBAC state on the gin context.
func installAuthContext(c *gin.Context, claims *tokenClaims, allowed map[uint64]struct{},
	db *sql.DB, companyUserID uint64, role string, admin bool) {
	c.Set(ctxCompanyDBKey, db)

	// B4 HA read/write split: simpan pool READ terpisah (replica ketika
	// tersedia & sehat, fallback primary). Handler GET memakai companyRead();
	// endpoint tulis tetap memakai ctxCompanyDBKey (primary).
	if appTenant != nil {
		if ro, rerr := appTenant.ReadPool(claims.CompanyCode); rerr == nil {
			c.Set(ctxCompanyROKey, ro)
		} else {
			c.Set(ctxCompanyROKey, db)
		}
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

// requireAuth validates the bearer JWT and installs RBAC context on the request.
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

		// B4 hardening: revocation — jti di-denylist setelah logout/refresh paksa.
		if appCfg.JWT.RevocationEnabled && claims.RegisteredClaims.ID != "" {
			denied, derr := getTokenManager().IsJTIDenied(c.Request.Context(), claims.RegisteredClaims.ID)
			if derr != nil {
				slog.Error("auth: cek denylist gagal", "error", derr, "jti", claims.RegisteredClaims.ID)
				writeError(c, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "service temporarily unavailable")
				c.Abort()
				return
			}
			if denied {
				internal.RBACDenialsTotal.WithLabelValues("request", "token_revoked").Inc()
				writeError(c, http.StatusUnauthorized, "TOKEN_REVOKED", "token has been revoked")
				c.Abort()
				return
			}
		}

		start := time.Now()
		allowed, db, ucaID, role, admin, err := authorize(claims)
		internal.RBACCheckDuration.WithLabelValues("authorize").Observe(time.Since(start).Seconds())
		if err != nil {
			if errors.Is(err, tenant.ErrCompanyNotFound) {
				slog.Warn("cross-tenant access denied", "company", claims.CompanyCode, "user_id", claims.UserID)
				internal.RBACDenialsTotal.WithLabelValues("request", "cross_tenant").Inc()
				internal.LogAudit(auditDB(), internal.AuditEntry{
					UserID:      claims.UserID,
					CompanyCode: claims.CompanyCode,
					EventType:   "ACCESS_DENIED",
					Action:      "request",
					Entity:      c.FullPath(),
					IP:          c.ClientIP(),
					UserAgent:   c.Request.UserAgent(),
					Details:     map[string]interface{}{"reason": "cross_tenant"},
				})
				writeError(c, http.StatusForbidden, "FORBIDDEN", "cross-tenant access is not allowed")
			} else {
				slog.Error("rbac: authorize failed", "error", err, "user_id", claims.UserID, "company", claims.CompanyCode)
				writeError(c, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "service temporarily unavailable")
			}
			c.Abort()
			return
		}

		// Platform tier: api-vehicle hanya melayani konteks tenant; identitas
		// platform (SuperAdmin @ 'default') dikelola di service-websocket.
		if tenant.IsPlatformCompany(claims.CompanyCode) {
			internal.RBACDenialsTotal.WithLabelValues(c.FullPath(), "platform_scope").Inc()
			writeError(c, http.StatusForbidden, "PLATFORM_SCOPE", "platform tokens may not access tenant APIs")
			c.Abort()
			return
		}

		installAuthContext(c, claims, allowed, db, ucaID, role, admin)
		c.Next()
	}
}
