package controllers

import (
	"time"

	"github.com/gin-gonic/gin"
)

// allowedAlertFilters mirrors the th_alerts CHECK constraints (migration 011).
var (
	allowedAlertTypes = map[string]bool{
		"geofence_breach": true, "overspeeding": true, "battery_low": true,
		"offline": true, "sos": true, "route_deviation": true,
		"fuel_drop": true, "refuel": true,
	}
	allowedAlertSeverities = map[string]bool{"low": true, "medium": true, "high": true, "critical": true}
	allowedAlertStatuses   = map[string]bool{"open": true, "acknowledged": true, "resolved": true}
)

// handleListAlerts implements GET /api/v1/alerts (PRD §8.1 pagination + filters
// + row-level RBAC via the request identity).
func (s *Service) handleListAlerts(c *gin.Context) {
	identity, _ := currentIdentity(c)
	page, limit, perr := s.parsePagination(c)
	if perr != nil {
		respondError(c, perr)
		return
	}
	q := AlertQuery{
		CompanyCode: identity.companyCode,
		AssignedIDs: identity.assigned,
		AllVehicles: identity.allVehicles,
		Type:        c.Query("type"),
		Severity:    c.Query("severity"),
		Status:      c.Query("status"),
		Page:        page,
		Limit:       limit,
	}
	if q.Type != "" && !allowedAlertTypes[q.Type] {
		respondError(c, errValidation("invalid alert type filter",
			map[string]string{"type": "must be one of: geofence_breach overspeeding battery_low offline sos route_deviation fuel_drop refuel"}))
		return
	}
	if q.Severity != "" && !allowedAlertSeverities[q.Severity] {
		respondError(c, errValidation("invalid alert severity filter",
			map[string]string{"severity": "must be one of: low medium high critical"}))
		return
	}
	if q.Status != "" && !allowedAlertStatuses[q.Status] {
		respondError(c, errValidation("invalid alert status filter",
			map[string]string{"status": "must be one of: open acknowledged resolved"}))
		return
	}
	if raw := c.Query("from"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			respondError(c, errValidation("from must be an RFC3339 timestamp",
				map[string]string{"from": "invalid"}))
			return
		}
		q.From = t
	}
	if raw := c.Query("to"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			respondError(c, errValidation("to must be an RFC3339 timestamp",
				map[string]string{"to": "invalid"}))
			return
		}
		q.To = t
	}
	items, total, err := s.store.ListAlerts(c.Request.Context(), q)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondOK(c, items, pagination(page, limit, total))
}

// handleAcknowledgeAlert implements POST /api/v1/alerts/:id/acknowledge
// (PRD §5.9.7): open → acknowledged; a second acknowledge conflicts and the
// SOS time-to-acknowledge is written exactly once by the conditional UPDATE.
func (s *Service) handleAcknowledgeAlert(c *gin.Context) {
	identity, _ := currentIdentity(c)
	id, perr := pathID(c)
	if perr != nil {
		respondError(c, perr)
		return
	}
	ctx := c.Request.Context()
	existing, err := s.store.AlertByID(ctx, identity.companyCode, id)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	if existing == nil {
		respondError(c, errNotFound(CodeAlertNotFound, "alert not found"))
		return
	}
	if existing.Status != "open" {
		respondError(c, errBadRequest(CodeConflict, "alert is not open"))
		return
	}
	rows, err := s.store.AcknowledgeAlert(ctx, identity.companyCode, id, identity.userID)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	if rows == 0 {
		respondError(c, errBadRequest(CodeConflict, "alert is not open"))
		return
	}
	after, err := s.store.AlertByID(ctx, identity.companyCode, id)
	if err != nil || after == nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondOK(c, after, nil)
}

// handleResolveAlert implements POST /api/v1/alerts/:id/resolve
// (open|acknowledged → resolved; PRD §5.9: manual or automatic resolution).
func (s *Service) handleResolveAlert(c *gin.Context) {
	identity, _ := currentIdentity(c)
	id, perr := pathID(c)
	if perr != nil {
		respondError(c, perr)
		return
	}
	ctx := c.Request.Context()
	existing, err := s.store.AlertByID(ctx, identity.companyCode, id)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	if existing == nil {
		respondError(c, errNotFound(CodeAlertNotFound, "alert not found"))
		return
	}
	if existing.Status == "resolved" {
		respondError(c, errBadRequest(CodeConflict, "alert is already resolved"))
		return
	}
	rows, err := s.store.ResolveAlert(ctx, identity.companyCode, id, identity.userID)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	if rows == 0 {
		respondError(c, errBadRequest(CodeConflict, "alert is already resolved"))
		return
	}
	after, err := s.store.AlertByID(ctx, identity.companyCode, id)
	if err != nil || after == nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondOK(c, after, nil)
}
