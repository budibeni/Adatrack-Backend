package controllers

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"ajb_gps/service-media/models"

	"github.com/gin-gonic/gin"
)

// mediaEventToDTO converts a model row to the API DTO.
func mediaEventToDTO(ev models.MediaEvent) models.MediaEventDTO {
	dto := models.MediaEventDTO{
		ID:          ev.ID,
		VehicleID:   ev.VehicleID,
		IMEI:        ev.IMEI,
		CompanyCode: ev.CompanyCode,
		MediaType:   ev.MediaType,
		TriggerType: ev.TriggerType,
		ObjectKey:   ev.ObjectKey,
		Bucket:      ev.Bucket,
		SizeBytes:   ev.SizeBytes,
		DurationSec: ev.DurationSec,
		MimeType:    ev.MimeType,
		SHA256:      ev.SHA256,
		Status:      ev.Status,
		TakenAt:     ev.TakenAt.UTC().Format(time.RFC3339),
	}
	if len(ev.Meta) > 0 {
		dto.Meta = json.RawMessage(ev.Meta)
	}
	return dto
}

func allowedScope(c *gin.Context) (map[uint64]struct{}, bool) {
	if v, ok := c.Get(ctxAllowedKey); ok {
		if m, ok2 := v.(map[uint64]struct{}); ok2 {
			return m, true
		}
	}
	return nil, false
}

// canAccessVehicle reports whether the caller may view media for a vehicle.
func canAccessVehicle(c *gin.Context, vehicleID uint64) bool {
	if v, ok := c.Get(ctxAdminKey); ok {
		if b, ok2 := v.(bool); ok2 && b {
			return true
		}
	}
	scope, ok := allowedScope(c)
	if !ok {
		return false
	}
	_, ok = scope[vehicleID]
	return ok
}

// isAdminCaller reports whether the authenticated caller is an Admin.
func isAdminCaller(c *gin.Context) bool {
	if v, ok := c.Get(ctxAdminKey); ok {
		b, _ := v.(bool)
		return b
	}
	return false
}

// companyCodeOf reads the company code from gin context (set by requireAuth).
func companyCodeOf(c *gin.Context) string {
	if v, ok := c.Get(ctxCompanyCodeKey); ok {
		if s, ok2 := v.(string); ok2 {
			return s
		}
	}
	return ""
}

// mediaListHandler — GET /api/v1/media (FR-8.4, RBAC row-level user_vehicles).
func mediaListHandler(c *gin.Context) {
	db, err := companyDB(c)
	if err != nil {
		writeError(c, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "no company context")
		return
	}
	st := newCompanyStore(companyCodeOf(c), db, appTenant)

	var scope map[uint64]struct{}
	if !isAdminCaller(c) {
		scope, _ = allowedScope(c)
	}
	trigger := strings.ToLower(strings.TrimSpace(c.Query("trigger_type")))
	status := strings.ToLower(strings.TrimSpace(c.Query("status")))
	page, limit := paginationParams(c)

	items, total, err := st.ListMediaEvents(scope, trigger, status, page, limit)
	if err != nil {
		slog.Error("media list failed", "error", err, "company", companyCodeOf(c))
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list media")
		return
	}
	out := make([]models.MediaEventDTO, 0, len(items))
	for _, it := range items {
		out = append(out, mediaEventToDTO(it))
	}
	writeSuccess(c, http.StatusOK, out, &models.PaginationInfo{Page: page, Limit: limit, Total: total})
}

// mediaDetailHandler — GET /api/v1/media/:id.
func mediaDetailHandler(c *gin.Context) {
	id := atoiDefault(c.Param("id"), 0)
	if id == 0 {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid media id")
		return
	}
	db, err := companyRead(c)
	if err != nil {
		writeError(c, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "no company context")
		return
	}
	st := newCompanyStore(companyCodeOf(c), db, appTenant)
	ev, err := st.GetMediaEvent(uint64(id))
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(c, http.StatusNotFound, "MEDIA_NOT_FOUND", "media event not found")
			return
		}
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to load media")
		return
	}
	if ev.DeletedAt != nil {
		writeError(c, http.StatusNotFound, "MEDIA_NOT_FOUND", "media event not found")
		return
	}
	if !canAccessVehicle(c, ev.VehicleID) {
		writeError(c, http.StatusForbidden, "FORBIDDEN", "no access to this vehicle's media")
		return
	}
	writeSuccess(c, http.StatusOK, mediaEventToDTO(*ev))
}
