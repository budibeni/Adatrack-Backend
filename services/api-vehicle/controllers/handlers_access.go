package controllers

import (
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"adatrack_gps/api-vehicle/models"
)

// registerAccessGovernanceRoutes wires the B12 access/menu registry + module
// licence endpoints:
//
//	GET  /api/v1/access/menu                  navigation of the CALLER's role
//	GET  /api/v1/access/menu/role/{role}      full role→menu matrix (Admin)
//	PUT  /api/v1/access/menu/role/{role}      replace the matrix     (Admin)
//	GET  /api/v1/modules                      tenant module licences
//	PUT  /api/v1/modules/{code}               set a licence         (Admin)
func (s *Service) registerAccessGovernanceRoutes(group *gin.RouterGroup) {
	access := group.Group("/access")
	access.GET("/menu", s.handleAccessMenu)
	access.GET("/menu/role/:role", s.requireAdmin(), s.handleRoleMenuMatrix)
	access.PUT("/menu/role/:role", s.requireAdmin(), s.handleReplaceRoleMenu)

	modules := group.Group("/modules")
	modules.GET("", s.handleListModules)
	modules.PUT("/:code", s.requireAdmin(), s.handleSetModuleLicense)
}

// handleAccessMenu implements GET /api/v1/access/menu: the navigation of the
// caller's role, filtered by the tenant licence registry (B12 acceptance:
// "Navigasi frontend dimuat dinamis dari GET /api/v1/access/menu sesuai role").
func (s *Service) handleAccessMenu(c *gin.Context) {
	if !s.enterpriseStoreOr503(c) {
		return
	}
	identity, _ := currentIdentity(c)
	items, err := s.enterprise.MenuItemsForRole(c.Request.Context(), identity.companyCode, identity.role)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondOK(c, items, nil)
}

// handleRoleMenuMatrix implements GET /api/v1/access/menu/role/{role} (Admin).
func (s *Service) handleRoleMenuMatrix(c *gin.Context) {
	if !s.enterpriseStoreOr503(c) {
		return
	}
	identity, _ := currentIdentity(c)
	role := strings.ToLower(strings.TrimSpace(c.Param("role")))
	if !validRole(role) {
		respondError(c, errValidation("invalid role",
			map[string]string{"role": "must be one of: admin manager operator driver"}))
		return
	}
	matrix, err := s.enterprise.RoleMenuMatrix(c.Request.Context(), identity.companyCode, role)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondOK(c, matrix, nil)
}

// handleReplaceRoleMenu implements PUT /api/v1/access/menu/role/{role} (Admin):
// the matrix is replaced atomically and the change is audited (MENU_ACCESS_UPDATED
// via the audit middleware action mapping).
func (s *Service) handleReplaceRoleMenu(c *gin.Context) {
	if !s.enterpriseStoreOr503(c) {
		return
	}
	identity, _ := currentIdentity(c)
	role := strings.ToLower(strings.TrimSpace(c.Param("role")))
	if !validRole(role) {
		respondError(c, errValidation("invalid role",
			map[string]string{"role": "must be one of: admin manager operator driver"}))
		return
	}
	var req models.UpsertRoleMenuRequest
	if verr := bindJSON(c, &req); verr != nil {
		respondError(c, verr)
		return
	}
	written, err := s.enterprise.ReplaceRoleMenuAccess(c.Request.Context(), identity.companyCode,
		role, req.Menus, identity.userID)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondOK(c, gin.H{"role": role, "menus": written}, nil)
}

// validRole guards the role segment against an arbitrary value (the matrix is
// keyed by role name, so an unchecked value would create dead rows).
func validRole(role string) bool {
	switch role {
	case "admin", "manager", "operator", "driver":
		return true
	default:
		return false
	}
}

// handleListModules implements GET /api/v1/modules (tenant module licences).
func (s *Service) handleListModules(c *gin.Context) {
	if !s.enterpriseStoreOr503(c) {
		return
	}
	identity, _ := currentIdentity(c)
	modules, err := s.enterprise.ModuleLicenses(c.Request.Context(), identity.companyCode)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondOK(c, modules, nil)
}

// handleSetModuleLicense implements PUT /api/v1/modules/{code} (Admin): enables/
// disables one module licence of the tenant (industry modules are opt-in).
func (s *Service) handleSetModuleLicense(c *gin.Context) {
	if !s.enterpriseStoreOr503(c) {
		return
	}
	identity, _ := currentIdentity(c)
	code := strings.ToLower(strings.TrimSpace(c.Param("code")))
	if code == "" || len(code) > 64 {
		respondError(c, errValidation("invalid module code", map[string]string{"code": "invalid"}))
		return
	}
	var req models.UpsertModuleLicenseRequest
	if verr := bindJSON(c, &req); verr != nil {
		respondError(c, verr)
		return
	}
	var expiresAt *time.Time
	if req.ExpiresAt != nil && strings.TrimSpace(*req.ExpiresAt) != "" {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(*req.ExpiresAt))
		if err != nil {
			respondError(c, errValidation("invalid expires_at",
				map[string]string{"expires_at": "must be RFC3339"}))
			return
		}
		expiresAt = &parsed
	}
	if err := s.enterprise.SetModuleLicense(c.Request.Context(), identity.companyCode, code,
		req.Enabled, expiresAt, identity.userID); err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	modules, err := s.enterprise.ModuleLicenses(c.Request.Context(), identity.companyCode)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondOK(c, modules, nil)
}
