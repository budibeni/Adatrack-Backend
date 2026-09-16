package controllers

import (
	"github.com/gin-gonic/gin"

	"ajb_gps/api-vehicle/models"
)

// handleUpdateGeofence implements PATCH /api/v1/geofences/:id.
func (s *Service) handleUpdateGeofence(c *gin.Context) {
	identity, _ := currentIdentity(c)
	id, perr := pathID(c)
	if perr != nil {
		respondError(c, perr)
		return
	}
	var req models.UpsertGeofenceRequest
	if verr := bindJSON(c, &req); verr != nil {
		respondError(c, verr)
		return
	}
	ctx := c.Request.Context()
	existing, err := s.store.GeofenceByID(ctx, identity.companyCode, id, false)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	if existing == nil {
		respondError(c, errNotFound(CodeGeofenceNotFound, "geofence not found"))
		return
	}

	// Merge the patch onto the stored row, then validate the merged geometry —
	// a partial update must never produce an invalid zone.
	merged := mergeGeofence(existing, &req)
	if gerr := validateGeofenceGeometry(mergedUpsert(merged)); gerr != nil {
		respondError(c, gerr)
		return
	}
	if merged.Name != existing.Name {
		exists, err := s.store.GeofenceNameExists(ctx, identity.companyCode, merged.Name, id)
		if err != nil {
			respondError(c, vehicleStoreErr(err))
			return
		}
		if exists {
			respondError(c, errConflict("a geofence with this name already exists"))
			return
		}
	}
	merged.ID = id
	if err := s.store.UpdateGeofence(ctx, identity.companyCode, merged, identity.userID); err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	updated, err := s.store.GeofenceByID(ctx, identity.companyCode, id, false)
	if err != nil || updated == nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondOK(c, updated, nil)
}

// handleDeleteGeofence implements DELETE /api/v1/geofences/:id (soft delete).
func (s *Service) handleDeleteGeofence(c *gin.Context) {
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
	if err := s.store.SoftDeleteGeofence(c.Request.Context(), identity.companyCode, id, identity.userID, reason); err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondOK(c, gin.H{"id": id, "deleted": true}, nil)
}

// handleRestoreGeofence implements POST /api/v1/geofences/:id/restore.
func (s *Service) handleRestoreGeofence(c *gin.Context) {
	identity, _ := currentIdentity(c)
	id, perr := pathID(c)
	if perr != nil {
		respondError(c, perr)
		return
	}
	ctx := c.Request.Context()
	existing, err := s.store.GeofenceByID(ctx, identity.companyCode, id, true)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	if existing == nil {
		respondError(c, errNotFound(CodeGeofenceNotFound, "geofence not found"))
		return
	}
	if existing.DeletedAt == nil {
		respondError(c, errBadRequest(CodeConflict, "geofence is not deleted"))
		return
	}
	if err := s.store.RestoreGeofence(ctx, identity.companyCode, id); err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	restored, err := s.store.GeofenceByID(ctx, identity.companyCode, id, false)
	if err != nil || restored == nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondOK(c, restored, nil)
}

// buildGeofence maps an upsert request onto a Geofence with PRD §5.9.1
// defaults (severity medium, entry+exit events on, zone active).
func buildGeofence(req *models.UpsertGeofenceRequest, base *models.Geofence) *models.Geofence {
	g := &models.Geofence{
		Name:        req.Name,
		Description: req.Description,
		AreaType:    req.AreaType,
		CenterLat:   req.CenterLat,
		CenterLon:   req.CenterLon,
		RadiusM:     req.RadiusM,
		Boundary:    req.Boundary,
		Severity:    req.Severity,
	}
	if g.Severity == "" {
		g.Severity = "medium"
	}
	if req.OnEntry != nil {
		g.OnEntry = *req.OnEntry
	} else {
		g.OnEntry = true
	}
	if req.OnExit != nil {
		g.OnExit = *req.OnExit
	} else {
		g.OnExit = true
	}
	if req.Active != nil {
		g.Active = *req.Active
	} else {
		g.Active = true
	}
	g.VehicleIDs = []int64{}
	return g
}

// mergeGeofence overlays a PATCH onto the stored row (nil fields keep values).
func mergeGeofence(existing *models.Geofence, req *models.UpsertGeofenceRequest) *models.Geofence {
	merged := *existing
	if req.Name != "" {
		merged.Name = req.Name
	}
	merged.Description = overlay(merged.Description, req.Description)
	if req.AreaType != "" {
		merged.AreaType = req.AreaType
	}
	merged.CenterLat = overlay(merged.CenterLat, req.CenterLat)
	merged.CenterLon = overlay(merged.CenterLon, req.CenterLon)
	merged.RadiusM = overlay(merged.RadiusM, req.RadiusM)
	if len(req.Boundary) > 0 {
		merged.Boundary = req.Boundary
	}
	if req.Severity != "" {
		merged.Severity = req.Severity
	}
	if req.OnEntry != nil {
		merged.OnEntry = *req.OnEntry
	}
	if req.OnExit != nil {
		merged.OnExit = *req.OnExit
	}
	if req.Active != nil {
		merged.Active = *req.Active
	}
	if merged.VehicleIDs == nil {
		merged.VehicleIDs = []int64{}
	}
	return &merged
}

// mergedUpsert adapts a merged Geofence to the geometry validator input.
func mergedUpsert(g *models.Geofence) *models.UpsertGeofenceRequest {
	return &models.UpsertGeofenceRequest{
		AreaType:  g.AreaType,
		CenterLat: g.CenterLat,
		CenterLon: g.CenterLon,
		RadiusM:   g.RadiusM,
		Boundary:  g.Boundary,
	}
}
