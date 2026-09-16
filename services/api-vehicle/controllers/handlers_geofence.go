package controllers

import (
	"github.com/gin-gonic/gin"

	"ajb_gps/api-vehicle/models"
)

// validateGeofenceGeometry enforces the PRD §5.9.1 geometry rules: circle needs
// center + radius, polygon needs a >= 3 point ring (the DB CHECKs re-assert it).
func validateGeofenceGeometry(g *models.UpsertGeofenceRequest) *APIError {
	switch g.AreaType {
	case "circle":
		if g.CenterLat == nil || g.CenterLon == nil || g.RadiusM == nil {
			return errValidation("circle geofence requires center_lat, center_lon and radius_meters",
				map[string]string{"area_type": "incomplete circle geometry"})
		}
	case "polygon":
		if len(g.Boundary) < 3 {
			return errValidation("polygon geofence requires at least 3 boundary_points",
				map[string]string{"boundary_points": "at least 3 points required"})
		}
	}
	return nil
}

// handleListGeofences implements GET /api/v1/geofences.
func (s *Service) handleListGeofences(c *gin.Context) {
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
	items, total, err := s.store.ListGeofences(c.Request.Context(), GeofenceQuery{
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

// handleGeofenceDetail implements GET /api/v1/geofences/:id.
func (s *Service) handleGeofenceDetail(c *gin.Context) {
	identity, _ := currentIdentity(c)
	id, perr := pathID(c)
	if perr != nil {
		respondError(c, perr)
		return
	}
	g, err := s.store.GeofenceByID(c.Request.Context(), identity.companyCode, id, false)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	if g == nil {
		respondError(c, errNotFound(CodeGeofenceNotFound, "geofence not found"))
		return
	}
	respondOK(c, g, nil)
}

// handleCreateGeofence implements POST /api/v1/geofences (PRD §6.2: Admin).
func (s *Service) handleCreateGeofence(c *gin.Context) {
	identity, _ := currentIdentity(c)
	var req models.UpsertGeofenceRequest
	if verr := bindJSON(c, &req); verr != nil {
		respondError(c, verr)
		return
	}
	if gerr := validateGeofenceGeometry(&req); gerr != nil {
		respondError(c, gerr)
		return
	}
	ctx := c.Request.Context()
	exists, err := s.store.GeofenceNameExists(ctx, identity.companyCode, req.Name, 0)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	if exists {
		respondError(c, errConflict("a geofence with this name already exists"))
		return
	}
	g := buildGeofence(&req, &models.Geofence{})
	if _, err := s.store.CreateGeofence(ctx, identity.companyCode, g, identity.userID); err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	created, err := s.store.GeofenceByID(ctx, identity.companyCode, g.ID, false)
	if err != nil || created == nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondCreated(c, created)
}
