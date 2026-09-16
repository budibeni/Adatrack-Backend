package controllers

import (
	"github.com/gin-gonic/gin"

	"ajb_gps/api-vehicle/models"
)

// deleteReasonRequest is the optional body of DELETE endpoints (PRD §6.0.1
// stores `delete_reason` for audit purposes).
type deleteReasonRequest struct {
	Reason string `json:"reason" binding:"omitempty,max=255"`
}

// allowedVehicleStatus is the PRD §6.2 fleet status set.
var allowedVehicleStatus = map[string]bool{"active": true, "inactive": true, "maintenance": true}

// handleListVehicles implements GET /api/v1/vehicles: pagination + status and
// search filters; rows are pre-filtered by the row-level grants (PRD §9.2).
func (s *Service) handleListVehicles(c *gin.Context) {
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
	status := c.Query("status")
	if status != "" && !allowedVehicleStatus[status] {
		respondError(c, errValidation("invalid status filter",
			map[string]string{"status": "must be one of: active inactive maintenance"}))
		return
	}
	q := VehicleQuery{
		CompanyCode: identity.companyCode,
		AssignedIDs: identity.assigned,
		AllVehicles: identity.allVehicles,
		Status:      status,
		Search:      c.Query("search"),
		IncludeDel:  includeDel,
		Page:        page,
		Limit:       limit,
	}
	items, total, err := s.store.ListVehicles(c.Request.Context(), q)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondOK(c, items, pagination(page, limit, total))
}

// handleVehicleDetail implements GET /api/v1/vehicles/:id.
func (s *Service) handleVehicleDetail(c *gin.Context) {
	identity, _ := currentIdentity(c)
	id, perr := pathID(c)
	if perr != nil {
		respondError(c, perr)
		return
	}
	v, err := s.store.VehicleByID(c.Request.Context(), identity.companyCode, id, false)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	if v == nil {
		respondError(c, errNotFound(CodeVehicleNotFound, "vehicle not found"))
		return
	}
	respondOK(c, v, nil)
}

// handleCreateVehicle implements POST /api/v1/vehicles (PRD §6.2): validates
// the IMEI uniqueness, defaults the status, then syncs the master IMEI map.
func (s *Service) handleCreateVehicle(c *gin.Context) {
	identity, _ := currentIdentity(c)
	var req models.UpsertVehicleRequest
	if verr := bindJSON(c, &req); verr != nil {
		respondError(c, verr)
		return
	}
	ctx := c.Request.Context()
	exists, err := s.store.IMEIExists(ctx, identity.companyCode, req.IMEI, 0)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	if exists {
		respondError(c, errConflict("a vehicle with this IMEI already exists"))
		return
	}
	v := &models.Vehicle{
		IMEI:         req.IMEI,
		PlateNumber:  req.PlateNumber,
		Make:         req.Make,
		Model:        req.Model,
		Year:         req.Year,
		Color:        req.Color,
		FuelType:     req.FuelType,
		CategoryCode: req.CategoryCode,
		TypeCode:     req.TypeCode,
		DriverUserID: req.DriverUserID,
		DriverName:   req.DriverName,
		DeviceModel:  req.DeviceModel,
		Status:       deref(req.Status),
	}
	if v.Status == "" {
		v.Status = "active"
	}
	if _, err := s.store.CreateVehicle(ctx, identity.companyCode, v, identity.userID); err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	created, err := s.store.VehicleByID(ctx, identity.companyCode, v.ID, false)
	if err != nil || created == nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondCreated(c, created)
}
