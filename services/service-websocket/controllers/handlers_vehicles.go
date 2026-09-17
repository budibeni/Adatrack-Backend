package controllers

import (
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"adatrack_gps/service-websocket/models"
)

// handleListVehicles implements `GET /api/v1/vehicles` (PRD §8.2, FR-5.1): the
// server-side row-level filter is applied from `tm_user_vehicles` and every row is
// enriched with the Redis live state.
func (s *Service) handleListVehicles(c *gin.Context) {
	identity, ok := currentIdentity(c)
	if !ok {
		respondError(c, errUnauthorized("unauthenticated"))
		return
	}
	page, limit, apiErr := s.parsePagination(c)
	if apiErr != nil {
		s.countHTTPError(apiErr.Status, apiErr.Code)
		respondError(c, apiErr)
		return
	}
	includeDeleted, apiErr := parseIncludeDeleted(c)
	if apiErr != nil {
		s.countHTTPError(apiErr.Status, apiErr.Code)
		respondError(c, apiErr)
		return
	}
	if includeDeleted && !identity.canViewDeleted() {
		s.denyRequest(c, nil, errForbidden(CodeForbidden,
			"include_deleted requires the Admin role"), "include_deleted")
		return
	}

	status := strings.TrimSpace(c.Query("status"))
	if status != "" && !validVehicleStatus(status) {
		apiErr := errValidation("status must be one of: active, inactive, maintenance",
			map[string]string{"status": "invalid"})
		s.countHTTPError(apiErr.Status, apiErr.Code)
		respondError(c, apiErr)
		return
	}

	start := time.Now()
	vehicles, total, err := s.store.ListVehicles(c.Request.Context(), VehicleQuery{
		CompanyCode: identity.user.CompanyCode,
		AllVehicles: identity.allVehicles,
		AssignedIDs: identity.assigned,
		Status:      status,
		Search:      strings.TrimSpace(c.Query("search")),
		IncludeDel:  includeDeleted,
		Page:        page,
		Limit:       limit,
	})
	if err != nil {
		s.respondStoreError(c, err)
		return
	}
	observeRBAC(start)

	if includeDeleted {
		_ = s.auditor.RecordSync(c.Request.Context(), AuditRow{
			Action: ActionSoftDeletedViewed, Outcome: OutcomeSuccess,
			ActorUserID: identity.user.ID, ActorEmail: identity.user.Email,
			ActorRole: identity.user.Role, CompanyCode: identity.user.CompanyCode,
			ActorIP: c.ClientIP(), EntityType: "vehicle", EntityID: "list",
			Reason: "include_deleted=true", RequestID: requestID(c),
		})
	}

	enrichWithLive(s, c, identity.user.CompanyCode, vehicles)
	respondOK(c, vehicles, pagination(page, limit, total))
}

// handleVehicleDetail implements `GET /api/v1/vehicles/{id}` (PRD §8.2).
func (s *Service) handleVehicleDetail(c *gin.Context) {
	identity, ok := currentIdentity(c)
	if !ok {
		respondError(c, errUnauthorized("unauthenticated"))
		return
	}
	id, apiErr := parseIDParam(c, "id")
	if apiErr != nil {
		s.countHTTPError(apiErr.Status, apiErr.Code)
		respondError(c, apiErr)
		return
	}
	includeDeleted, apiErr := parseIncludeDeleted(c)
	if apiErr != nil {
		s.countHTTPError(apiErr.Status, apiErr.Code)
		respondError(c, apiErr)
		return
	}
	if includeDeleted && !identity.canViewDeleted() {
		s.denyRequest(c, nil, errForbidden(CodeForbidden,
			"include_deleted requires the Admin role"), "include_deleted")
		return
	}

	start := time.Now()
	vehicle, err := s.store.VehicleByID(c.Request.Context(), identity.user.CompanyCode, id, includeDeleted)
	if err != nil {
		s.respondStoreError(c, err)
		return
	}
	observeRBAC(start)
	if vehicle == nil {
		respondError(c, errNotFound(CodeVehicleNotFound, "vehicle "+itoa(id)+" not found"))
		return
	}
	if !identity.canAccessVehicle(vehicle.ID) {
		// Exists but not assigned to this user (row-level RBAC, PRD §3.1).
		s.denyRequest(c, nil, errForbidden(CodeUnauthorizedVehicle,
			"vehicle "+itoa(id)+" is not assigned to this user"), "vehicle")
		return
	}

	if includeDeleted {
		_ = s.auditor.RecordSync(c.Request.Context(), AuditRow{
			Action: ActionSoftDeletedViewed, Outcome: OutcomeSuccess,
			ActorUserID: identity.user.ID, ActorEmail: identity.user.Email,
			ActorRole: identity.user.Role, CompanyCode: identity.user.CompanyCode,
			ActorIP: c.ClientIP(), EntityType: "vehicle", EntityID: itoa(vehicle.ID),
			Reason: "include_deleted=true", RequestID: requestID(c),
		})
	}

	enrichable := []models.Vehicle{*vehicle}
	enrichWithLive(s, c, identity.user.CompanyCode, enrichable)
	respondOK(c, enrichable[0], nil)
}

// canAccessVehicle applies the row-level rule for one vehicle id (PRD §3.1).
func (i *tenantIdentity) canAccessVehicle(vehicleID int64) bool {
	if i.allVehicles {
		return true
	}
	for _, id := range i.assigned {
		if id == vehicleID {
			return true
		}
	}
	return false
}

// canViewDeleted reports whether the role may read soft-deleted rows (§6.0.1).
func (i *tenantIdentity) canViewDeleted() bool {
	return i.user.Role == models.RoleAdmin || i.user.Role == models.RoleSuperAdmin
}

// enrichWithLive attaches the Redis live state to each vehicle (best effort:
// Redis being down degrades to a list without live data, never an error).
func enrichWithLive(s *Service, c *gin.Context, companyCode string, vehicles []models.Vehicle) {
	if s.live == nil || len(vehicles) == 0 {
		return
	}
	imeis := make([]string, 0, len(vehicles))
	for _, v := range vehicles {
		imeis = append(imeis, v.IMEI)
	}
	states := s.live.LiveStates(c.Request.Context(), companyCode, imeis)
	for idx := range vehicles {
		if st, ok := states[vehicles[idx].IMEI]; ok {
			state := st
			vehicles[idx].Live = &state
		}
	}
}

// validVehicleStatus whitelists the `status` filter (PRD §8.5 rule 2).
func validVehicleStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case "active", "inactive", "maintenance":
		return true
	default:
		return false
	}
}

// parseIDParam validates a positive integer path parameter.
func parseIDParam(c *gin.Context, name string) (int64, *APIError) {
	raw := strings.TrimSpace(c.Param(name))
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, errValidation(name+" must be a positive integer", map[string]string{name: "invalid"})
	}
	return id, nil
}
