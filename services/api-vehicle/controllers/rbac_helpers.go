package controllers

import (
	"log/slog"
	"net/http"
	"strings"

	"ajb_gps/api-vehicle/models"
	"ajb_gps/internal"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// RBAC permission helpers (row-level security).
// ---------------------------------------------------------------------------

// loadAuthUser reads the authenticated user from context.
func loadAuthUser(c *gin.Context) (models.AuthUser, bool) {
	v, ok := c.Get(ctxUserKey)
	if !ok {
		return models.AuthUser{}, false
	}
	u, ok := v.(models.AuthUser)
	return u, ok
}

// isAdmin reports whether the caller is a company Admin.
func isAdmin(c *gin.Context) bool {
	v, ok := c.Get(ctxAdminKey)
	if !ok {
		return false
	}
	admin, _ := v.(bool)
	return admin
}

// accessibleVehicleIDs returns the caller's allowed vehicle set.
func accessibleVehicleIDs(c *gin.Context) map[uint64]struct{} {
	v, _ := c.Get(ctxAllowedKey)
	allowed, _ := v.(map[uint64]struct{})
	return allowed
}

// canAccessVehicle returns true when the caller may access the given vehicle.
func canAccessVehicle(c *gin.Context, vehicleID uint64) bool {
	if isAdmin(c) {
		return true
	}
	allowed := accessibleVehicleIDs(c)
	if len(allowed) == 0 {
		recordRBACDenial(c, "vehicle", "no_access")
		return false
	}
	_, has := allowed[vehicleID]
	if !has {
		recordRBACDenial(c, "vehicle", "no_access")
	}
	return has
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

// requireRole aborts with 403 unless the caller has one of the given roles.
func requireRole(c *gin.Context, roles ...string) bool {
	v, ok := c.Get(ctxRoleKey)
	if ok {
		role, _ := v.(string)
		for _, r := range roles {
			if strings.EqualFold(role, r) {
				return true
			}
		}
	}
	recordRBACDenial(c, "role", "insufficient_role")
	writeError(c, http.StatusForbidden, "FORBIDDEN", "insufficient role")
	c.Abort()
	return false
}

// requireAdminOrManager allows Admin & Manager (mis. route management).
func requireAdminOrManager(c *gin.Context) bool {
	if isAdmin(c) {
		return true
	}
	v, _ := c.Get(ctxRoleKey)
	role, _ := v.(string)
	if isManagerRole(role) {
		return true
	}
	recordRBACDenial(c, "role", "insufficient_role")
	writeError(c, http.StatusForbidden, "FORBIDDEN", "admin or manager role required")
	c.Abort()
	return false
}

// companyCodeOf reads the tenant code from the request context.
func companyCodeOf(c *gin.Context) string {
	v, ok := c.Get(ctxCompanyCodeKey)
	if !ok {
		return ""
	}
	code, _ := v.(string)
	return code
}

// recordRBACDenial writes the rbac metric + audit log (GAP #10).
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
}
