package controllers

import (
	"strings"

	"github.com/gin-gonic/gin"

	"adatrack_gps/api-vehicle/models"
	protocolreg "adatrack_gps/internal/protocol"
	"adatrack_gps/internal/validate"
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
	if err := validate.SearchTerm(c.Query("search"), 100); err != nil {
		respondError(c, errValidation("invalid search term",
			map[string]string{"search": "max 100 characters, no control characters"}))
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
	// Phase B6: overlay the freshest live telemetry (fuel_level/volume/temp, the
	// REAL device ACC flag and the last position) in ONE batched Redis read.
	items = s.enrichLiveStates(c.Request.Context(), identity.companyCode, items)
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
	// Phase B6: the detail view carries the same live overlay as the list, so a
	// detail poll and a WS VEHICLE_UPDATE describe the vehicle identically.
	enriched := s.enrichLiveStates(c.Request.Context(), identity.companyCode, []models.Vehicle{*v})
	respondOK(c, &enriched[0], nil)
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
	// B10 hardening (PRD §8.5/§9.6): the IMEI is the anti-spoofing identity of the
	// device (FR-1.4), so it must be exactly the 15-digit form the allowlist
	// `master.tm_vehicle_imei_map` can resolve — validated here instead of relying
	// on the loose binding rule.
	if err := validate.IMEI(req.IMEI); err != nil {
		respondError(c, errValidation("invalid IMEI",
			map[string]string{"imei": "must be exactly 15 digits"}))
		return
	}
	exists, err := s.store.IMEIExists(ctx, identity.companyCode, req.IMEI, 0)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	if exists {
		respondError(c, errConflict("a vehicle with this IMEI already exists"))
		return
	}
	// B11 universal brand support: the protocol/brand must be in the registry.
	proto, port, brand, perr := resolveProtocol(req.Protocol, req.Brand)
	if perr != nil {
		respondError(c, perr)
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
		Protocol:     proto,
		ProtocolPort: port,
		Brand:        brand,
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

// resolveProtocol validates an optional protocol/brand pair against the universal
// registry (PRD Module 1c, B11). The listener port is derived SERVER-SIDE so a
// client can never point a device at an arbitrary port; an unsupported brand is a
// 400 instead of a device registered on a listener that does not exist.
func resolveProtocol(code, brand *string) (*string, *int, *string, *APIError) {
	trimmedCode := ""
	if code != nil {
		trimmedCode = strings.TrimSpace(*code)
	}
	if trimmedCode == "" {
		if brand != nil && strings.TrimSpace(*brand) != "" {
			return nil, nil, nil, errValidation("protocol is required when brand is set",
				map[string]string{"protocol": "required, one of: " + strings.Join(protocolreg.Codes(), ", ")})
		}
		return nil, nil, nil, nil
	}
	p, ok := protocolreg.Lookup(trimmedCode)
	if !ok {
		return nil, nil, nil, errValidation("unsupported device protocol",
			map[string]string{"protocol": "must be one of: " + strings.Join(protocolreg.Codes(), ", ")})
	}
	normalized := p.Code
	port := p.DefaultPort
	resolvedBrand := p.Brand
	if brand != nil && strings.TrimSpace(*brand) != "" {
		resolvedBrand = strings.TrimSpace(*brand)
	}
	return &normalized, &port, &resolvedBrand, nil
}
