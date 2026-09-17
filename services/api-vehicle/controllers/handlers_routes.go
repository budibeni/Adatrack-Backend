package controllers

import (
	"github.com/gin-gonic/gin"

	"adatrack_gps/api-vehicle/models"
)

// handleListRoutes implements GET /api/v1/routes.
func (s *Service) handleListRoutes(c *gin.Context) {
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
	items, total, err := s.store.ListRoutes(c.Request.Context(), RouteQuery{
		CompanyCode: identity.companyCode,
		Page:        page,
		Limit:       limit,
		IncludeDel:  includeDel,
	})
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondOK(c, items, pagination(page, limit, total))
}

// handleRouteDetail implements GET /api/v1/routes/:id.
func (s *Service) handleRouteDetail(c *gin.Context) {
	identity, _ := currentIdentity(c)
	id, perr := pathID(c)
	if perr != nil {
		respondError(c, perr)
		return
	}
	r, err := s.store.RouteByID(c.Request.Context(), identity.companyCode, id, false)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	if r == nil {
		respondError(c, errNotFound(CodeRouteNotFound, "route not found"))
		return
	}
	respondOK(c, r, nil)
}

// handleCreateRoute implements POST /api/v1/routes (PRD §5.9.2: waypoints are
// ordered [lat, lon] stops; sequences are normalised server-side).
func (s *Service) handleCreateRoute(c *gin.Context) {
	identity, _ := currentIdentity(c)
	var req models.UpsertRouteRequest
	if verr := bindJSON(c, &req); verr != nil {
		respondError(c, verr)
		return
	}
	ctx := c.Request.Context()
	exists, err := s.store.RouteNameExists(ctx, identity.companyCode, req.Name, 0)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	if exists {
		respondError(c, errConflict("a route with this name already exists"))
		return
	}
	r := &models.Route{
		Name:        req.Name,
		Description: req.Description,
		Waypoints:   normalizeWaypoints(req.Waypoints),
		EstMinutes:  req.EstMinutes,
	}
	if _, err := s.store.CreateRoute(ctx, identity.companyCode, r, identity.userID); err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	created, err := s.store.RouteByID(ctx, identity.companyCode, r.ID, false)
	if err != nil || created == nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondCreated(c, created)
}

// handleUpdateRoute implements PATCH /api/v1/routes/:id.
func (s *Service) handleUpdateRoute(c *gin.Context) {
	identity, _ := currentIdentity(c)
	id, perr := pathID(c)
	if perr != nil {
		respondError(c, perr)
		return
	}
	var req models.UpsertRouteRequest
	if verr := bindJSON(c, &req); verr != nil {
		respondError(c, verr)
		return
	}
	ctx := c.Request.Context()
	existing, err := s.store.RouteByID(ctx, identity.companyCode, id, false)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	if existing == nil {
		respondError(c, errNotFound(CodeRouteNotFound, "route not found"))
		return
	}
	if req.Name != existing.Name {
		exists, err := s.store.RouteNameExists(ctx, identity.companyCode, req.Name, id)
		if err != nil {
			respondError(c, vehicleStoreErr(err))
			return
		}
		if exists {
			respondError(c, errConflict("a route with this name already exists"))
			return
		}
	}
	existing.Name = req.Name
	existing.Description = overlay(existing.Description, req.Description)
	existing.Waypoints = normalizeWaypoints(req.Waypoints)
	existing.EstMinutes = overlay(existing.EstMinutes, req.EstMinutes)
	if err := s.store.UpdateRoute(ctx, identity.companyCode, existing, identity.userID); err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	updated, err := s.store.RouteByID(ctx, identity.companyCode, id, false)
	if err != nil || updated == nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondOK(c, updated, nil)
}

// handleDeleteRoute implements DELETE /api/v1/routes/:id (soft delete).
func (s *Service) handleDeleteRoute(c *gin.Context) {
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
	if err := s.store.SoftDeleteRoute(c.Request.Context(), identity.companyCode, id, identity.userID, reason); err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondOK(c, gin.H{"id": id, "deleted": true}, nil)
}

// handleRestoreRoute implements POST /api/v1/routes/:id/restore.
func (s *Service) handleRestoreRoute(c *gin.Context) {
	identity, _ := currentIdentity(c)
	id, perr := pathID(c)
	if perr != nil {
		respondError(c, perr)
		return
	}
	ctx := c.Request.Context()
	existing, err := s.store.RouteByID(ctx, identity.companyCode, id, true)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	if existing == nil {
		respondError(c, errNotFound(CodeRouteNotFound, "route not found"))
		return
	}
	if existing.DeletedAt == nil {
		respondError(c, errBadRequest(CodeConflict, "route is not deleted"))
		return
	}
	if err := s.store.RestoreRoute(ctx, identity.companyCode, id); err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	restored, err := s.store.RouteByID(ctx, identity.companyCode, id, false)
	if err != nil || restored == nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondOK(c, restored, nil)
}

// normalizeWaypoints assigns 1-based sequences when the client omitted them.
func normalizeWaypoints(in []models.Waypoint) []models.Waypoint {
	out := make([]models.Waypoint, 0, len(in))
	for i, w := range in {
		if w.Seq == 0 {
			w.Seq = i + 1
		}
		out = append(out, w)
	}
	return out
}
