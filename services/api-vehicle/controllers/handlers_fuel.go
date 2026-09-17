package controllers

import (
	"time"

	"github.com/gin-gonic/gin"

	"adatrack_gps/api-vehicle/models"
)

// handleListFuelConfigs implements GET /api/v1/fuel-configs (FR-7.6 CRUD).
func (s *Service) handleListFuelConfigs(c *gin.Context) {
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
	items, err := s.store.ListFuelConfigs(c.Request.Context(), identity.companyCode, includeDel)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondOK(c, items, nil)
}

// handleFuelConfigDetail implements GET /api/v1/fuel-configs/:id.
func (s *Service) handleFuelConfigDetail(c *gin.Context) {
	identity, _ := currentIdentity(c)
	id, perr := pathID(c)
	if perr != nil {
		respondError(c, perr)
		return
	}
	fc, err := s.store.FuelConfigByID(c.Request.Context(), identity.companyCode, id, false)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	if fc == nil {
		respondError(c, errNotFound(CodeFuelConfigNotFound, "fuel config not found"))
		return
	}
	respondOK(c, fc, nil)
}

// fuelConfigFromRequest maps the request body onto a config row.
func fuelConfigFromRequest(req *models.UpsertFuelConfigRequest) *models.FuelConfig {
	fc := &models.FuelConfig{
		VehicleID:          req.VehicleID,
		DropThresholdPct:   req.DropThresholdPct,
		RefuelThresholdPct: req.RefuelThresholdPct,
		WindowSeconds:      req.WindowSeconds,
		Severity:           req.Severity,
		RequireACC:         req.RequireACC != nil && *req.RequireACC,
		ACCStaleSeconds:    req.ACCStaleSeconds,
		Enabled:            true,
	}
	if req.Enabled != nil {
		fc.Enabled = *req.Enabled
	}
	if fc.WindowSeconds <= 0 {
		fc.WindowSeconds = 600
	}
	if fc.Severity == "" {
		fc.Severity = "critical"
	}
	if fc.ACCStaleSeconds <= 0 {
		fc.ACCStaleSeconds = 600
	}
	return fc
}

// handleCreateFuelConfig implements POST /api/v1/fuel-configs (PRD §5.9.9:
// vehicle_id null = tenant-wide default; the vehicle row must exist otherwise).
func (s *Service) handleCreateFuelConfig(c *gin.Context) {
	identity, _ := currentIdentity(c)
	var req models.UpsertFuelConfigRequest
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
	fc := fuelConfigFromRequest(&req)
	if _, err := s.store.CreateFuelConfig(ctx, identity.companyCode, fc, identity.userID); err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	created, err := s.store.FuelConfigByID(ctx, identity.companyCode, fc.ID, false)
	if err != nil || created == nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondCreated(c, created)
}

// handleUpdateFuelConfig implements PATCH /api/v1/fuel-configs/:id.
func (s *Service) handleUpdateFuelConfig(c *gin.Context) {
	identity, _ := currentIdentity(c)
	id, perr := pathID(c)
	if perr != nil {
		respondError(c, perr)
		return
	}
	var req models.UpsertFuelConfigRequest
	if verr := bindJSON(c, &req); verr != nil {
		respondError(c, verr)
		return
	}
	ctx := c.Request.Context()
	existing, err := s.store.FuelConfigByID(ctx, identity.companyCode, id, false)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	if existing == nil {
		respondError(c, errNotFound(CodeFuelConfigNotFound, "fuel config not found"))
		return
	}
	if req.VehicleID != nil && derefInt64(req.VehicleID) != derefInt64(existing.VehicleID) {
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
	merged.DropThresholdPct = req.DropThresholdPct
	merged.RefuelThresholdPct = req.RefuelThresholdPct
	if req.WindowSeconds > 0 {
		merged.WindowSeconds = req.WindowSeconds
	}
	if req.Severity != "" {
		merged.Severity = req.Severity
	}
	if req.RequireACC != nil {
		merged.RequireACC = *req.RequireACC
	}
	if req.ACCStaleSeconds > 0 {
		merged.ACCStaleSeconds = req.ACCStaleSeconds
	}
	if req.Enabled != nil {
		merged.Enabled = *req.Enabled
	}
	merged.ID = id
	if err := s.store.UpdateFuelConfig(ctx, identity.companyCode, &merged, identity.userID); err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	updated, err := s.store.FuelConfigByID(ctx, identity.companyCode, id, false)
	if err != nil || updated == nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondOK(c, updated, nil)
}

// handleDeleteFuelConfig implements DELETE /api/v1/fuel-configs/:id.
func (s *Service) handleDeleteFuelConfig(c *gin.Context) {
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
	if err := s.store.SoftDeleteFuelConfig(c.Request.Context(), identity.companyCode, id, identity.userID, reason); err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondOK(c, gin.H{"id": id, "deleted": true}, nil)
}

// handleRestoreFuelConfig implements POST /api/v1/fuel-configs/:id/restore.
func (s *Service) handleRestoreFuelConfig(c *gin.Context) {
	identity, _ := currentIdentity(c)
	id, perr := pathID(c)
	if perr != nil {
		respondError(c, perr)
		return
	}
	ctx := c.Request.Context()
	existing, err := s.store.FuelConfigByID(ctx, identity.companyCode, id, true)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	if existing == nil {
		respondError(c, errNotFound(CodeFuelConfigNotFound, "fuel config not found"))
		return
	}
	if existing.DeletedAt == nil {
		respondError(c, errBadRequest(CodeConflict, "fuel config is not deleted"))
		return
	}
	if err := s.store.RestoreFuelConfig(ctx, identity.companyCode, id); err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	restored, err := s.store.FuelConfigByID(ctx, identity.companyCode, id, false)
	if err != nil || restored == nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondOK(c, restored, nil)
}

// handleVehicleFuelHistory implements GET /api/v1/vehicles/:id/fuel/history
// (FR-7.7: ?from&to RFC3339, pagination, row-level RBAC via requireVehicleAccess).
func (s *Service) handleVehicleFuelHistory(c *gin.Context) {
	identity, _ := currentIdentity(c)
	id, perr := pathID(c)
	if perr != nil {
		respondError(c, perr)
		return
	}
	page, limit, perr := s.parsePagination(c)
	if perr != nil {
		respondError(c, perr)
		return
	}
	var from, to time.Time
	if raw := c.Query("from"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			respondError(c, errValidation("from must be an RFC3339 timestamp",
				map[string]string{"from": "invalid"}))
			return
		}
		from = t
	}
	if raw := c.Query("to"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			respondError(c, errValidation("to must be an RFC3339 timestamp",
				map[string]string{"to": "invalid"}))
			return
		}
		to = t
	}
	items, total, err := s.store.ListFuelHistory(c.Request.Context(),
		identity.companyCode, id, from, to, page, limit)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondOK(c, items, pagination(page, limit, total))
}
