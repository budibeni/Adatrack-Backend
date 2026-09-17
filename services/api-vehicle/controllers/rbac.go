package controllers

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"adatrack_gps/api-vehicle/models"
)

// tenantIdentity is the resolved (role + row-level grants) identity of one user
// inside their tenant (PRD §3.1/§9.2) — resolved fresh on EVERY request so a
// role/assignment change takes effect without waiting for token expiry.
type tenantIdentity struct {
	userID      int64
	email       string
	role        string // effective role (tenant override wins)
	companyCode string
	assigned    []int64
	// allVehicles is true for Admin/Manager (tenant-wide) and platform admins.
	allVehicles        bool
	platform           bool
	mustChangePassword bool
}

// extractToken reads the bearer token from the Authorization header.
func extractToken(c *gin.Context) string {
	header := strings.TrimSpace(c.GetHeader("Authorization"))
	if header == "" {
		return ""
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) == 2 && strings.EqualFold(parts[0], "bearer") {
		return strings.TrimSpace(parts[1])
	}
	return ""
}

// authenticate validates the JWT (signature/issuer/expiry/revocation), loads
// the auth authority row and resolves the tenant identity (PRD §8.6 flow).
func (s *Service) authenticate() gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := extractToken(c)
		if raw == "" {
			s.denyRequest(c, nil, errUnauthorized("missing bearer token"), "missing_token")
			return
		}
		claims, err := s.auth.ParseAccessToken(raw)
		if err != nil {
			s.denyRequest(c, nil, err, "invalid_token")
			return
		}
		revoked, rerr := s.auth.Revoked(c.Request.Context(), claims)
		if rerr != nil {
			respondError(c, errUnavailable("token revocation state unavailable"))
			c.Abort()
			return
		}
		if revoked {
			respondError(c, s.auth.RevokedTokenError())
			c.Abort()
			return
		}
		user, uerr := s.store.UserByID(c.Request.Context(), claims.UserID)
		if uerr != nil {
			respondError(c, errUnavailable("authentication backend unavailable"))
			c.Abort()
			return
		}
		if user == nil || !user.IsActive {
			s.denyRequest(c, claims, NewAPIError(http.StatusUnauthorized, CodeAccountInactive, "account is inactive"), "inactive_account")
			return
		}

		identity, ierr := s.resolveIdentity(c.Request.Context(), user)
		if ierr != nil {
			respondError(c, ierr)
			c.Abort()
			return
		}
		c.Set(ctxClaims, claims)
		c.Set(ctxIdentity, identity)
		c.Next()
	}
}

// resolveIdentity resolves the effective role from `tm_user_company_access`
// (role_override wins) and the row-level vehicle grants from `tm_user_vehicles`.
func (s *Service) resolveIdentity(ctx context.Context, u *UserRecord) (*tenantIdentity, error) {
	identity := &tenantIdentity{
		userID:             u.ID,
		email:              u.Email,
		role:               u.GlobalRole,
		companyCode:        strings.ToUpper(strings.TrimSpace(u.CompanyCode)),
		mustChangePassword: u.MustChangePassword,
	}

	// Platform tier (PRD §3.1): context `default` + SuperAdmin.
	if identity.role == models.RoleSuperAdmin && identity.companyCode == models.PlatformCompanyCode {
		identity.allVehicles = true
		identity.platform = true
		return identity, nil
	}

	roleOverride, active, found, err := s.store.TenantAccess(ctx, identity.companyCode, u.ID)
	if err != nil {
		return nil, errUnavailable("authorization backend unavailable")
	}
	if found {
		if !active {
			return nil, NewAPIError(http.StatusUnauthorized, CodeAccountInactive, "tenant access revoked")
		}
		if roleOverride != "" {
			identity.role = roleOverride
		}
	} else if identity.role != models.RoleSuperAdmin {
		// No membership row and not a platform admin → no tenant access at all.
		rbacDenied.WithLabelValues("not_a_member").Inc()
		return nil, errForbidden(CodeForbidden, "user has no access to this tenant")
	}

	switch identity.role {
	case models.RoleAdmin, models.RoleManager:
		identity.allVehicles = true
	default:
		ids, ierr := s.store.AssignedVehicleIDs(ctx, identity.companyCode, u.ID)
		if ierr != nil {
			return nil, errUnavailable("authorization backend unavailable")
		}
		identity.assigned = ids
	}
	return identity, nil
}

// requireTenantScope rejects platform tokens on tenant fleet routes (PRD §3.1:
// platform scope is governance-only; fleet data access needs tenant membership).
func (s *Service) requireTenantScope() gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := currentIdentity(c)
		if ok && identity.platform {
			s.denyRequest(c, nil, errForbidden(CodePlatformScope,
				"platform identity cannot access tenant fleet resources"), "platform_scope")
			return
		}
		c.Next()
	}
}

// requireWrite allows Admin and Manager to mutate fleet resources (PRD §8.2:
// write: Admin; Manager is the operational deputy role, PRD §3.1).
func (s *Service) requireWrite() gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := currentIdentity(c)
		if !ok {
			s.denyRequest(c, nil, errUnauthorized("missing identity"), "missing_identity")
			return
		}
		switch identity.role {
		case models.RoleAdmin, models.RoleManager:
			c.Next()
		default:
			s.denyRequest(c, nil, errForbidden(CodeForbidden,
				"only Admin or Manager may modify fleet resources"), "write_role")
		}
	}
}

// requireAdmin restricts destructive operations (soft delete / restore) to the
// tenant Admin role.
func (s *Service) requireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := currentIdentity(c)
		if !ok {
			s.denyRequest(c, nil, errUnauthorized("missing identity"), "missing_identity")
			return
		}
		if identity.role != models.RoleAdmin {
			s.denyRequest(c, nil, errForbidden(CodeForbidden,
				"only Admin may delete or restore fleet resources"), "admin_role")
			return
		}
		c.Next()
	}
}

// vehicleAllowed implements the row-level check (PRD §3.1): Admin/Manager see
// the whole tenant; everyone else only their tm_user_vehicles grants. An empty
// grant list means ZERO vehicles, never "all".
func vehicleAllowed(identity *tenantIdentity, vehicleID int64) bool {
	if identity.allVehicles {
		return true
	}
	for _, id := range identity.assigned {
		if id == vehicleID {
			return true
		}
	}
	return false
}

// requireVehicleAccess enforces the row-level check on /:id routes.
func (s *Service) requireVehicleAccess() gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := currentIdentity(c)
		if !ok {
			s.denyRequest(c, nil, errUnauthorized("missing identity"), "missing_identity")
			return
		}
		id, perr := pathID(c)
		if perr != nil {
			respondError(c, perr)
			c.Abort()
			return
		}
		if !vehicleAllowed(identity, id) {
			s.denyRequest(c, nil, errForbidden(CodeUnauthorizedVehicle,
				"vehicle exists but is not assigned to you"), "vehicle_row_level")
			return
		}
		c.Next()
	}
}

// requirePasswordRotated blocks every business endpoint while the account still
// carries the FR-5.5 default password (`403 PASSWORD_CHANGE_REQUIRED`) — the
// same contract as service-websocket.
func (s *Service) requirePasswordRotated() gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := currentIdentity(c)
		if !ok {
			respondError(c, errUnauthorized("unauthenticated"))
			c.Abort()
			return
		}
		if identity.mustChangePassword {
			respondError(c, errForbidden(CodePasswordChangeNeeded,
				"password change required before using this endpoint"))
			c.Abort()
			return
		}
		c.Next()
	}
}
