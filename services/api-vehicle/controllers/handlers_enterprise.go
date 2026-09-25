package controllers

import (
	"github.com/gin-gonic/gin"

	"adatrack_gps/internal/validate"
)

// registerEnterpriseRoutes wires the table-driven CRUD of one enterprise resource
// (B12). Read is tenant-wide; write needs Admin/Manager; soft delete + restore are
// Admin-only (§6.0.1). An immutable resource (access log) gets no mutation routes
// at all — an append-only log is stronger than a soft delete.
func (s *Service) registerEnterpriseRoutes(group *gin.RouterGroup, spec resourceSpec) {
	r := group.Group("/" + spec.Name)
	r.GET("", s.handleListEnterprise(spec))
	r.POST("", s.requireWrite(), s.handleCreateEnterprise(spec))
	r.GET("/:id", s.handleGetEnterprise(spec))
	if spec.Immutable {
		return
	}
	r.PATCH("/:id", s.requireWrite(), s.handleUpdateEnterprise(spec))
	r.DELETE("/:id", s.requireAdmin(), s.handleDeleteEnterprise(spec))
	r.POST("/:id/restore", s.requireAdmin(), s.handleRestoreEnterprise(spec))
}

// enterpriseNotFoundCode derives the per-resource 404 error_code from the audit
// entity label (e.g. DRIVER_NOT_FOUND) so a client can distinguish resources.
func enterpriseNotFoundCode(spec resourceSpec) string {
	return spec.Entity + "_NOT_FOUND"
}

// enterpriseStoreOr503 guards a handler when the persistence layer is absent.
func (s *Service) enterpriseStoreOr503(c *gin.Context) bool {
	if s.enterprise == nil {
		respondError(c, errUnavailable("enterprise data source unavailable"))
		return false
	}
	return true
}

// handleListEnterprise implements GET /api/v1/<resource>.
func (s *Service) handleListEnterprise(spec resourceSpec) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !s.enterpriseStoreOr503(c) {
			return
		}
		identity, _ := currentIdentity(c)
		page, limit, perr := s.parsePagination(c)
		if perr != nil {
			respondError(c, perr)
			return
		}
		includeDel, ierr := parseIncludeDeleted(c)
		if ierr != nil {
			respondError(c, ierr)
			return
		}
		if aerr := deniedListGuard(identity, includeDel); aerr != nil {
			respondError(c, aerr)
			return
		}
		if err := validate.SearchTerm(c.Query("search"), 100); err != nil {
			respondError(c, errValidation("invalid search term",
				map[string]string{"search": "max 100 characters, no control characters"}))
			return
		}
		items, total, err := s.enterprise.ListEnterprise(c.Request.Context(), spec, EnterpriseQuery{
			CompanyCode: identity.companyCode,
			Search:      c.Query("search"),
			IncludeDel:  includeDel,
			Page:        page,
			Limit:       limit,
		})
		if err != nil {
			respondError(c, vehicleStoreErr(err))
			return
		}
		respondOK(c, items, pagination(page, limit, total))
	}
}

// handleGetEnterprise implements GET /api/v1/<resource>/:id.
func (s *Service) handleGetEnterprise(spec resourceSpec) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !s.enterpriseStoreOr503(c) {
			return
		}
		identity, _ := currentIdentity(c)
		id, perr := pathID(c)
		if perr != nil {
			respondError(c, perr)
			return
		}
		item, err := s.enterprise.GetEnterprise(c.Request.Context(), identity.companyCode, spec, id, false)
		if err != nil {
			respondError(c, vehicleStoreErr(err))
			return
		}
		if item == nil {
			respondError(c, errNotFound(enterpriseNotFoundCode(spec), spec.Name+" entry not found"))
			return
		}
		respondOK(c, item, nil)
	}
}

// handleCreateEnterprise implements POST /api/v1/<resource>.
func (s *Service) handleCreateEnterprise(spec resourceSpec) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !s.enterpriseStoreOr503(c) {
			return
		}
		identity, _ := currentIdentity(c)
		var payload map[string]any
		if verr := bindJSON(c, &payload); verr != nil {
			respondError(c, verr)
			return
		}
		data, nerr := spec.normalize(payload, false)
		if nerr != nil {
			respondError(c, nerr)
			return
		}
		ctx := c.Request.Context()
		id, err := s.enterprise.CreateEnterprise(ctx, identity.companyCode, spec, data, identity.userID)
		if err != nil {
			respondError(c, vehicleStoreErr(err))
			return
		}
		created, err := s.enterprise.GetEnterprise(ctx, identity.companyCode, spec, id, false)
		if err != nil || created == nil {
			respondError(c, vehicleStoreErr(err))
			return
		}
		respondCreated(c, created)
	}
}

// handleUpdateEnterprise implements PATCH /api/v1/<resource>/:id.
func (s *Service) handleUpdateEnterprise(spec resourceSpec) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !s.enterpriseStoreOr503(c) {
			return
		}
		identity, _ := currentIdentity(c)
		id, perr := pathID(c)
		if perr != nil {
			respondError(c, perr)
			return
		}
		var payload map[string]any
		if verr := bindJSON(c, &payload); verr != nil {
			respondError(c, verr)
			return
		}
		data, nerr := spec.normalize(payload, true)
		if nerr != nil {
			respondError(c, nerr)
			return
		}
		ctx := c.Request.Context()
		affected, err := s.enterprise.UpdateEnterprise(ctx, identity.companyCode, spec, id, data, identity.userID)
		if err != nil {
			respondError(c, vehicleStoreErr(err))
			return
		}
		if affected == 0 {
			respondError(c, errNotFound(enterpriseNotFoundCode(spec), spec.Name+" entry not found"))
			return
		}
		updated, err := s.enterprise.GetEnterprise(ctx, identity.companyCode, spec, id, false)
		if err != nil || updated == nil {
			respondError(c, vehicleStoreErr(err))
			return
		}
		respondOK(c, updated, nil)
	}
}

// handleDeleteEnterprise implements DELETE /api/v1/<resource>/:id (soft delete).
func (s *Service) handleDeleteEnterprise(spec resourceSpec) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !s.enterpriseStoreOr503(c) {
			return
		}
		identity, _ := currentIdentity(c)
		id, perr := pathID(c)
		if perr != nil {
			respondError(c, perr)
			return
		}
		var body deleteReasonRequest
		_ = c.ShouldBindJSON(&body)
		reason := body.Reason
		if reason == "" {
			reason = "deleted via api"
		}
		affected, err := s.enterprise.SoftDeleteEnterprise(c.Request.Context(), identity.companyCode,
			spec, id, identity.userID, reason)
		if err != nil {
			respondError(c, vehicleStoreErr(err))
			return
		}
		if affected == 0 {
			respondError(c, errNotFound(enterpriseNotFoundCode(spec), spec.Name+" entry not found"))
			return
		}
		respondOK(c, gin.H{"id": id, "deleted": true}, nil)
	}
}

// handleRestoreEnterprise implements POST /api/v1/<resource>/:id/restore.
func (s *Service) handleRestoreEnterprise(spec resourceSpec) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !s.enterpriseStoreOr503(c) {
			return
		}
		identity, _ := currentIdentity(c)
		id, perr := pathID(c)
		if perr != nil {
			respondError(c, perr)
			return
		}
		ctx := c.Request.Context()
		existing, err := s.enterprise.GetEnterprise(ctx, identity.companyCode, spec, id, true)
		if err != nil {
			respondError(c, vehicleStoreErr(err))
			return
		}
		if existing == nil {
			respondError(c, errNotFound(enterpriseNotFoundCode(spec), spec.Name+" entry not found"))
			return
		}
		if deletedAt, _ := existing["deleted_at"].(string); deletedAt == "" {
			respondError(c, errBadRequest(CodeConflict, spec.Name+" entry is not deleted"))
			return
		}
		affected, err := s.enterprise.RestoreEnterprise(ctx, identity.companyCode, spec, id)
		if err != nil {
			respondError(c, vehicleStoreErr(err))
			return
		}
		if affected == 0 {
			respondError(c, errNotFound(enterpriseNotFoundCode(spec), spec.Name+" entry not found"))
			return
		}
		restored, err := s.enterprise.GetEnterprise(ctx, identity.companyCode, spec, id, false)
		if err != nil || restored == nil {
			respondError(c, vehicleStoreErr(err))
			return
		}
		respondOK(c, restored, nil)
	}
}
