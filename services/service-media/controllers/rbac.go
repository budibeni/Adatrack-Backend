package controllers

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// Role names (master `tm_users.global_role`, PRD §3.1).
const (
	RoleSuperAdmin = "SuperAdmin"
	RoleAdmin      = "Admin"
	RoleManager    = "Manager"
	RoleOperator   = "Operator"
	RoleDriver     = "Driver"
)

// PlatformCompanyCode is the reserved platform tenant (PRD §3.1).
const PlatformCompanyCode = "DEFAULT"

// tenantIdentity is the resolved (role + row-level grants) identity of one user
// inside their tenant (PRD §3.1/§9.2), resolved fresh on EVERY request.
type tenantIdentity struct {
	userID             int64
	email              string
	role               string
	companyCode        string
	assigned           []int64
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

// authenticate validates the JWT (signature/issuer/expiry/revocation), loads the
// auth authority row and resolves the tenant identity (PRD §8.6 flow).
func (s *Service) authenticate() gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := extractToken(c)
		if raw == "" {
			s.denyRequest(c, errUnauthorized("missing bearer token"), "missing_token")
			return
		}
		claims, err := s.auth.ParseAccessToken(raw)
		if err != nil {
			s.denyRequest(c, err, "invalid_token")
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
			s.denyRequest(c, NewAPIError(http.StatusUnauthorized, CodeAccountInactive,
				"account is inactive"), "inactive_account")
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

// resolveIdentity resolves the effective role (tenant override wins) and the
// row-level vehicle grants (PRD §3.1).
func (s *Service) resolveIdentity(ctx context.Context, u *UserRecord) (*tenantIdentity, error) {
	identity := &tenantIdentity{
		userID:             u.ID,
		email:              u.Email,
		role:               u.GlobalRole,
		companyCode:        strings.ToUpper(strings.TrimSpace(u.CompanyCode)),
		mustChangePassword: u.MustChangePassword,
	}

	if identity.role == RoleSuperAdmin && identity.companyCode == PlatformCompanyCode {
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
			return nil, NewAPIError(http.StatusUnauthorized, CodeAccountInactive,
				"tenant membership is disabled")
		}
		if roleOverride != "" {
			identity.role = roleOverride
		}
	}
	switch identity.role {
	case RoleAdmin, RoleManager:
		identity.allVehicles = true
	default:
		ids, aerr := s.store.AssignedVehicleIDs(ctx, identity.companyCode, u.ID)
		if aerr != nil {
			return nil, errUnavailable("authorization backend unavailable")
		}
		identity.assigned = ids
	}
	return identity, nil
}

// vehicleAllowed implements the row-level check (PRD §3.1): Admin/Manager see the
// whole tenant; everyone else only their tm_user_vehicles grants. An empty grant
// list means ZERO vehicles, never "all".
func vehicleAllowed(identity *tenantIdentity, vehicleID int64) bool {
	if identity == nil {
		return false
	}
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
