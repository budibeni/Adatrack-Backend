package controllers

import (
	"github.com/gin-gonic/gin"

	"adatrack_gps/api-vehicle/models"
	"adatrack_gps/internal/validate"
)

// handleUpdateVehicle implements PATCH /api/v1/vehicles/:id.
//
// The IMEI may be re-pointed (tracker replacement) but ONLY with a valid 15-digit
// value that is free in this tenant: it is the anti-spoofing identity (FR-1.4), so a
// malformed value is a 400 and a collision is a 409 — the master allowlist map is
// moved by the store, never duplicated. Omitting the field keeps the current IMEI.
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
	if req.IMEI != "" && req.IMEI != existing.IMEI {
		// B10: same strict rule as create — the identity must be the 15-digit form
		// the allowlist can resolve, and it must be free.
		if err := validate.IMEI(req.IMEI); err != nil {
			respondError(c, errValidation("invalid IMEI",
				map[string]string{"imei": "must be exactly 15 digits"}))
			return
		}
		taken, err := s.store.IMEIExists(ctx, identity.companyCode, req.IMEI, id)
		if err != nil {
			respondError(c, vehicleStoreErr(err))
			return
		}
		if taken {
			respondError(c, errConflict("a vehicle with this IMEI already exists"))
			return
		}
		existing.IMEI = req.IMEI
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
	// B11 universal brand support: a provided protocol must be in the registry and
	// its listener port is derived server-side (never client-supplied).
	if req.Protocol != nil || req.Brand != nil {
		proto, port, brand, perr := resolveProtocol(req.Protocol, req.Brand)
		if perr != nil {
			respondError(c, perr)
			return
		}
		if proto != nil {
			existing.Protocol = proto
			existing.ProtocolPort = port
			existing.Brand = brand
		}
	}
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
