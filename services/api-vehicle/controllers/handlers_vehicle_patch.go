package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"adatrack_gps/api-vehicle/models"
)

// handleUpdateVehicle implements PATCH /api/v1/vehicles/:id. The IMEI is
// immutable through the API — changing the device identity would break the
// anti-spoofing map (FR-1.4); re-point a device by deleting + recreating.
func (s *Service) handleUpdateVehicle(c *gin.Context) {
	identity, _ := currentIdentity(c)
	id, perr := pathID(c)
	if perr != nil {
		respondError(c, perr)
		return
	}
	var req models.UpsertVehicleRequest
	if verr := bindJSON(c, &req); verr != nil {
		respondError(c, verr)
		return
	}
	ctx := c.Request.Context()
	existing, err := s.store.VehicleByID(ctx, identity.companyCode, id, false)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	if existing == nil {
		respondError(c, errNotFound(CodeVehicleNotFound, "vehicle not found"))
		return
	}
	if req.IMEI != existing.IMEI {
		respondError(c, NewAPIError(http.StatusConflict, CodeConflict, "IMEI cannot be changed"))
		return
	}

	// PATCH overlay: provided fields win, absent fields keep the stored value.
	if req.PlateNumber != "" {
		existing.PlateNumber = req.PlateNumber
	}
	existing.Make = overlay(existing.Make, req.Make)
	existing.Model = overlay(existing.Model, req.Model)
	existing.Year = overlay(existing.Year, req.Year)
	existing.Color = overlay(existing.Color, req.Color)
	existing.FuelType = overlay(existing.FuelType, req.FuelType)
	existing.CategoryCode = overlay(existing.CategoryCode, req.CategoryCode)
	existing.TypeCode = overlay(existing.TypeCode, req.TypeCode)
	existing.DriverUserID = overlay(existing.DriverUserID, req.DriverUserID)
	existing.DriverName = overlay(existing.DriverName, req.DriverName)
	existing.DeviceModel = overlay(existing.DeviceModel, req.DeviceModel)
	if req.Status != nil {
		existing.Status = *req.Status
	}

	if err := s.store.UpdateVehicle(ctx, identity.companyCode, existing, identity.userID); err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	updated, err := s.store.VehicleByID(ctx, identity.companyCode, id, false)
	if err != nil || updated == nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondOK(c, updated, nil)
}

// handleDeleteVehicle implements DELETE /api/v1/vehicles/:id (soft delete,
// PRD §6.0.1) + master IMEI map deactivation (FR-1.4 anti-spoofing).
func (s *Service) handleDeleteVehicle(c *gin.Context) {
	identity, _ := currentIdentity(c)
	id, perr := pathID(c)
	if perr != nil {
		respondError(c, perr)
		return
	}
	var body deleteReasonRequest
	_ = c.ShouldBindJSON(&body) // the body is optional
	reason := body.Reason
	if reason == "" {
		reason = "deleted via api"
	}
	if err := s.store.SoftDeleteVehicle(c.Request.Context(), identity.companyCode, id, identity.userID, reason); err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondOK(c, gin.H{"id": id, "deleted": true}, nil)
}

// handleRestoreVehicle implements POST /api/v1/vehicles/:id/restore.
func (s *Service) handleRestoreVehicle(c *gin.Context) {
	identity, _ := currentIdentity(c)
	id, perr := pathID(c)
	if perr != nil {
		respondError(c, perr)
		return
	}
	ctx := c.Request.Context()
	existing, err := s.store.VehicleByID(ctx, identity.companyCode, id, true)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	if existing == nil {
		respondError(c, errNotFound(CodeVehicleNotFound, "vehicle not found"))
		return
	}
	if existing.DeletedAt == nil {
		respondError(c, errBadRequest(CodeConflict, "vehicle is not deleted"))
		return
	}
	if err := s.store.RestoreVehicle(ctx, identity.companyCode, id); err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	restored, err := s.store.VehicleByID(ctx, identity.companyCode, id, false)
	if err != nil || restored == nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondOK(c, restored, nil)
}

// overlay implements PATCH semantics: keep dst when src is nil.
func overlay[T any](dst, src *T) *T {
	if src != nil {
		return src
	}
	return dst
}
