package controllers

import (
	"github.com/gin-gonic/gin"

	"ajb_gps/api-vehicle/models"
)

// handleListSpeedConfigs implements GET /api/v1/speed-configs.
func (s *Service) handleListSpeedConfigs(c *gin.Context) {
	identity, _ := currentIdentity(c)
	includeDel, ierr := parseIncludeDeleted(c)
	if ierr != nil {
		respondError(c, ierr)
		return
	}
	if aerr := deniedListGuard(identity, includeDel); aerr != nil {
		respondError(c, aerr)
		return
	}
	items, err := s.store.ListSpeedConfigs(c.Request.Context(), identity.companyCode, includeDel)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondOK(c, items, nil)
}

// handleSpeedConfigDetail implements GET /api/v1/speed-configs/:id.
func (s *Service) handleSpeedConfigDetail(c *gin.Context) {
	identity, _ := currentIdentity(c)
	id, perr := pathID(c)
	if perr != nil {
		respondError(c, perr)
		return
	}
	sc, err := s.store.SpeedConfigByID(c.Request.Context(), identity.companyCode, id, false)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	if sc == nil {
		respondError(c, errNotFound(CodeSpeedConfigNotFound, "speed config not found"))
		return
	}
	respondOK(c, sc, nil)
}

// handleCreateSpeedConfig implements POST /api/v1/speed-configs (PRD §5.9.4:
// vehicle_id null = tenant-wide default; the vehicle row must exist otherwise).
func (s *Service) handleCreateSpeedConfig(c *gin.Context) {
	identity, _ := currentIdentity(c)
	var req models.UpsertSpeedConfigRequest
	if verr := bindJSON(c, &req); verr != nil {
		respondError(c, verr)
		return
	}
	ctx := c.Request.Context()
	if req.VehicleID != nil {
		v, err := s.store.VehicleByID(ctx, identity.companyCode, *req.VehicleID, false)
		if err != nil {
			respondError(c, vehicleStoreErr(err))
			return
		}
		if v == nil {
			respondError(c, errNotFound(CodeVehicleNotFound, "vehicle not found"))
			return
		}
	}
	sc := &models.SpeedConfig{
		VehicleID:   req.VehicleID,
		MaxSpeedKMH: req.MaxSpeedKMH,
		GracePct:    req.GracePct,
		Severity:    req.Severity,
		Enabled:     true,
	}
	if req.Enabled != nil {
		sc.Enabled = *req.Enabled
	}
	if sc.Severity == "" {
		sc.Severity = "medium"
	}
	if _, err := s.store.CreateSpeedConfig(ctx, identity.companyCode, sc, identity.userID); err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	created, err := s.store.SpeedConfigByID(ctx, identity.companyCode, sc.ID, false)
	if err != nil || created == nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondCreated(c, created)
}

// handleUpdateSpeedConfig implements PATCH /api/v1/speed-configs/:id.
func (s *Service) handleUpdateSpeedConfig(c *gin.Context) {
	identity, _ := currentIdentity(c)
	id, perr := pathID(c)
	if perr != nil {
		respondError(c, perr)
		return
	}
	var req models.UpsertSpeedConfigRequest
	if verr := bindJSON(c, &req); verr != nil {
		respondError(c, verr)
		return
	}
	ctx := c.Request.Context()
	existing, err := s.store.SpeedConfigByID(ctx, identity.companyCode, id, false)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	if existing == nil {
		respondError(c, errNotFound(CodeSpeedConfigNotFound, "speed config not found"))
		return
	}
	if req.VehicleID != nil && *req.VehicleID != derefInt64(existing.VehicleID) {
		v, err := s.store.VehicleByID(ctx, identity.companyCode, *req.VehicleID, false)
		if err != nil {
			respondError(c, vehicleStoreErr(err))
			return
		}
		if v == nil {
			respondError(c, errNotFound(CodeVehicleNotFound, "vehicle not found"))
			return
		}
	}
	merged := *existing
	merged.VehicleID = overlay(existing.VehicleID, req.VehicleID)
	merged.MaxSpeedKMH = req.MaxSpeedKMH
	if req.GracePct > 0 {
		merged.GracePct = req.GracePct
	}
	if req.Severity != "" {
		merged.Severity = req.Severity
	}
	if req.Enabled != nil {
		merged.Enabled = *req.Enabled
	}
	merged.ID = id
	if err := s.store.UpdateSpeedConfig(ctx, identity.companyCode, &merged, identity.userID); err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	updated, err := s.store.SpeedConfigByID(ctx, identity.companyCode, id, false)
	if err != nil || updated == nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondOK(c, updated, nil)
}

// handleDeleteSpeedConfig implements DELETE /api/v1/speed-configs/:id.
func (s *Service) handleDeleteSpeedConfig(c *gin.Context) {
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
	if err := s.store.SoftDeleteSpeedConfig(c.Request.Context(), identity.companyCode, id, identity.userID, reason); err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondOK(c, gin.H{"id": id, "deleted": true}, nil)
}

// handleRestoreSpeedConfig implements POST /api/v1/speed-configs/:id/restore.
func (s *Service) handleRestoreSpeedConfig(c *gin.Context) {
	identity, _ := currentIdentity(c)
	id, perr := pathID(c)
	if perr != nil {
		respondError(c, perr)
		return
	}
	ctx := c.Request.Context()
	existing, err := s.store.SpeedConfigByID(ctx, identity.companyCode, id, true)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	if existing == nil {
		respondError(c, errNotFound(CodeSpeedConfigNotFound, "speed config not found"))
		return
	}
	if existing.DeletedAt == nil {
		respondError(c, errBadRequest(CodeConflict, "speed config is not deleted"))
		return
	}
	if err := s.store.RestoreSpeedConfig(ctx, identity.companyCode, id); err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	restored, err := s.store.SpeedConfigByID(ctx, identity.companyCode, id, false)
	if err != nil || restored == nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondOK(c, restored, nil)
}

// derefInt64 maps a nil *int64 to 0 for comparisons.
func derefInt64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}
