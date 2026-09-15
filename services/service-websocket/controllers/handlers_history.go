package controllers

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// handleVehicleHistory implements `GET /api/v1/vehicles/{id}/history`
// (PRD §8.2: positions history with pagination + validated time range).
func (s *Service) handleVehicleHistory(c *gin.Context) {
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
	page, limit, apiErr := s.parsePagination(c)
	if apiErr != nil {
		s.countHTTPError(apiErr.Status, apiErr.Code)
		respondError(c, apiErr)
		return
	}
	from, to, apiErr := s.parseTimeRange(c, 24*time.Hour)
	if apiErr != nil {
		s.countHTTPError(apiErr.Status, apiErr.Code)
		respondError(c, apiErr)
		return
	}

	start := time.Now()
	vehicle, err := s.store.VehicleByID(c.Request.Context(), identity.user.CompanyCode, id, false)
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
		s.denyRequest(c, nil, errForbidden(CodeUnauthorizedVehicle,
			"vehicle "+itoa(id)+" is not assigned to this user"), "vehicle")
		return
	}

	positions, total, err := s.store.VehicleHistory(c.Request.Context(), HistoryQuery{
		CompanyCode: identity.user.CompanyCode,
		VehicleID:   vehicle.ID,
		IMEI:        vehicle.IMEI,
		From:        from,
		To:          to,
		Page:        page,
		Limit:       limit,
	})
	if err != nil {
		s.respondStoreError(c, err)
		return
	}
	respondOK(c, positions, pagination(page, limit, total))
}

// respondStoreError maps a persistence failure to a 503 (graceful degradation,
// PRD §8.1) and logs the cause (never a silent failure).
func (s *Service) respondStoreError(c *gin.Context, err error) {
	if apiErr, ok := err.(*APIError); ok {
		s.countHTTPError(apiErr.Status, apiErr.Code)
		respondError(c, apiErr)
		return
	}
	slog.Error("service-websocket: persistence failure",
		"path", c.FullPath(), "request_id", requestID(c), "error", err)
	s.countHTTPError(http.StatusServiceUnavailable, CodeServiceUnavailable)
	respondError(c, errUnavailable("database unavailable"))
}
