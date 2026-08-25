package controllers

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"ajb_gps/internal"
	"ajb_gps/internal/tenant"
	"ajb_gps/service-websocket/models"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// RBAC middleware & permission checks (FR-5.1, PRD §3.1/§6, GAP #12)
//
// 1. AuthN : validate JWT (HS256, payload berisi company_code).
// 2. Tenant: route ke adatrack_gps_{company_code} via TenantManager; cross-tenant
//            (company_code tidak dikenal / token milik company lain) → 403.
// 3. AuthZ : authoritative row-level security — user_company_access is_active +
//            role_override (PRD §6.1) + user_vehicles di company DB per request.
//            Role "Admin" melihat semua vehicle perusahaannya; lainnya hanya
//            user_vehicles. Denied → 403 + audit + metric rbac_denials_total.
// ---------------------------------------------------------------------------

const (
	ctxUserKey        = "auth_user"
	ctxAllowedKey     = "auth_allowed_vehicle_ids" // map[uint64]struct{}
	ctxAdminKey       = "auth_is_admin"
	ctxCompanyDBKey   = "auth_company_db"
	ctxCompanyROKey   = "auth_company_read_db" // B4 HA: pool READ (replica-preferred)
	ctxCompanyCodeKey = "auth_company_code"
)

// isAdminRole reports whether an effective role is Admin (case-insensitive).
func isAdminRole(role string) bool {
	return strings.EqualFold(strings.TrimSpace(role), "Admin")
}

// authorize resolves the tenant DB + role + vehicle scope from JWT claims.
// Returned allowed set is nil when the user is the company Admin (unrestricted
// within the company).
func authorize(claims *tokenClaims) (allowed map[uint64]struct{}, db *sql.DB,
	companyUserID uint64, role string, admin bool, err error) {

	// Platform tier (PRD §6.1 governance): konteks 'default' berada DI LUAR
	// semua tenant — tidak ada company DB dan tidak ada user_vehicles yang
	// perlu di-resolve. Hanya SuperAdmin yang boleh memakai konteks ini;
	// token platform dibatasi ke endpoint platform oleh requireAuth().
	if tenant.IsPlatformCompany(claims.CompanyCode) {
		if !tenant.IsPlatformRole(claims.Role) {
			return nil, nil, 0, "", false, errors.New("platform context requires the SuperAdmin role")
		}
		return make(map[uint64]struct{}), nil, 0, claims.Role, false, nil
	}

	db, err = companyDBByCode(claims.CompanyCode)
	if err != nil {
		return nil, nil, 0, "", false, err
	}
	ucaID, roleOverride, isActive, err := loadCompanyAccess(db, claims.UserID)
	if err != nil {
		return nil, nil, 0, "", false, err
	}
	if !isActive {
		return nil, nil, 0, "", false, errors.New("company access inactive")
	}
	role = effectiveRole(claims.Role, roleOverride)
	admin = isAdminRole(role)

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
	return allowed, db, ucaID, role, admin, nil
}

// installAuthContext stores the resolved RBAC state on the gin context.
func installAuthContext(c *gin.Context, claims *tokenClaims, allowed map[uint64]struct{},
	db *sql.DB, companyUserID uint64, role string, admin bool) {
	c.Set(ctxCompanyDBKey, db)

	// B4 HA read/write split: simpan pool READ terpisah (replica ketika
	// tersedia & sehat, fallback primary). Handler GET memakai companyRead();
	// handler tulis tetap memakai ctxCompanyDBKey (primary).
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
		claims, err := parseToken(appCfg, tokenStr)
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
		allowed, db, ucaID, role, isAdmin, err := authorize(claims)
		internal.RBACCheckDuration.WithLabelValues("authorize").Observe(time.Since(start).Seconds())
		if err != nil {
			if errors.Is(err, tenant.ErrCompanyNotFound) {
				slog.Warn("cross-tenant access denied", "company", claims.CompanyCode, "user_id", claims.UserID)
				recordRBACDenial(c, "request", "cross_tenant")
				writeError(c, http.StatusForbidden, "FORBIDDEN", "cross-tenant access is not allowed")
			} else {
				slog.Error("rbac: authorize failed", "error", err, "user_id", claims.UserID, "company", claims.CompanyCode)
				writeError(c, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "service temporarily unavailable")
			}
			c.Abort()
			return
		}

		// Platform token hanya boleh menyentuh endpoint platform — handler
		// tenant mengharapkan company DB non-nil, jadi blok di sini agar tidak
		// pernah terjadi panic / kebocoran data lintas-konteks.
		if tenant.IsPlatformCompany(claims.CompanyCode) && !isPlatformPath(c.FullPath()) {
			recordRBACDenial(c, c.FullPath(), "platform_scope")
			writeError(c, http.StatusForbidden, "PLATFORM_SCOPE", "platform tokens may only access platform endpoints")
			c.Abort()
			return
		}

		installAuthContext(c, claims, allowed, db, ucaID, role, isAdmin)
		c.Next()
	}
}

// loadAuthUser reads the authenticated user from context.
func loadAuthUser(c *gin.Context) (models.AuthUser, bool) {
	v, ok := c.Get(ctxUserKey)
	if !ok {
		return models.AuthUser{}, false
	}
	u, ok := v.(models.AuthUser)
	return u, ok
}

// canAccessVehicle returns true when the caller may access the given vehicle.
// Admin sees everything in their company; others must be in user_vehicles
// (row-level security).
func canAccessVehicle(c *gin.Context, vehicleID uint64) bool {
	if isAdmin(c) {
		return true
	}
	allowed, ok := c.Get(ctxAllowedKey)
	if !ok {
		return false
	}
	_, has := allowed.(map[uint64]struct{})[vehicleID]
	if !has {
		recordRBACDenial(c, "vehicle", "no_access")
	}
	return has
}

// isAdmin reports whether the caller has the Admin role (within its company).
func isAdmin(c *gin.Context) bool {
	v, ok := c.Get(ctxAdminKey)
	if !ok {
		return false
	}
	admin, _ := v.(bool)
	return admin
}

// isPlatformAdmin reports whether the caller is a platform super admin
// (konteks 'default' + role SuperAdmin) — satu-satunya identitas yang boleh
// mendaftarkan company baru (governance multi-tenant PRD §6.1).
func isPlatformAdmin(c *gin.Context) bool {
	u, ok := loadAuthUser(c)
	return ok && tenant.IsPlatformIdentity(u.CompanyCode, u.Role)
}

// isPlatformPath reports whether the route is a PLATFORM endpoint that
// SuperAdmin tokens may access (allowlist — semuanya lainnya ditolak).
func isPlatformPath(path string) bool {
	switch path {
	case "/api/v1/companies", "/api/v1/users":
		return true
	}
	return false
}

// requireVehicleAccess aborts with 403 when the caller lacks permission.
func requireVehicleAccess(c *gin.Context, vehicleID uint64) bool {
	if canAccessVehicle(c, vehicleID) {
		return true
	}
	writeError(c, http.StatusForbidden, "FORBIDDEN", "you do not have access to this vehicle")
	c.Abort()
	return false
}

// recordRBACDenial writes the rbac metric + audit log (GAP #10 + B4 DB audit).
func recordRBACDenial(c *gin.Context, action, reason string) {
	internal.RBACDenialsTotal.WithLabelValues(action, reason).Inc()
	u, _ := loadAuthUser(c)
	slog.Warn("rbac denial",
		"action", action,
		"reason", reason,
		"user_id", u.ID,
		"company", u.CompanyCode,
		"role", u.Role,
		"path", c.FullPath(),
		"ip", c.ClientIP(),
	)
	internal.LogAudit(auditDB(), internal.AuditEntry{
		UserID:      u.ID,
		CompanyCode: u.CompanyCode,
		EventType:   "ACCESS_DENIED",
		Action:      action,
		Entity:      c.FullPath(),
		IP:          c.ClientIP(),
		UserAgent:   c.Request.UserAgent(),
		Details:     map[string]interface{}{"reason": reason, "role": u.Role},
	})
}

// auditLogin records login events (sukses/gagal) — GAP #10/#12 audit:
// structured log SELALU + baris master.audit_logs (async, best-effort).
func auditLogin(userID uint64, email, ip, userAgent string, success bool) {
	if success {
		slog.Info("login success", "user_id", userID, "email", email, "ip", ip)
	} else {
		slog.Warn("login failure", "email", email, "ip", ip)
	}
	eventType := "LOGIN_FAILURE"
	if success {
		eventType = "LOGIN_SUCCESS"
	}
	internal.LogAudit(auditDB(), internal.AuditEntry{
		UserID:      userID,
		EventType:   eventType,
		Action:      "login",
		Entity:      "user",
		EntityID:    email,
		IP:          ip,
		UserAgent:   userAgent,
	})
}
