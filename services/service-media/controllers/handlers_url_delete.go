package controllers

import (
	"log/slog"
	"net/http"
	"strconv"

	"ajb_gps/internal"
	"ajb_gps/service-media/models"

	"github.com/gin-gonic/gin"
)

// mediaURLHandler — GET /api/v1/media/:id/url → short-TTL presigned URL (+audit).
func mediaURLHandler(c *gin.Context) {
	id := atoiDefault(c.Param("id"), 0)
	if id == 0 {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid media id")
		return
	}
	db, err := companyDB(c)
	if err != nil {
		writeError(c, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "no company context")
		return
	}
	st := newCompanyStore(companyCodeOf(c), db, appTenant)
	ev, err := st.GetMediaEvent(uint64(id))
	if err != nil || ev.DeletedAt != nil {
		writeError(c, http.StatusNotFound, "MEDIA_NOT_FOUND", "media event not found")
		return
	}
	if ev.Status == "expired" || ev.Status == "failed" {
		writeError(c, http.StatusGone, "MEDIA_EXPIRED", "media is not available")
		return
	}
	if !canAccessVehicle(c, ev.VehicleID) {
		internal.RBACDenialsTotal.WithLabelValues("media.url", "vehicle_scope").Inc()
		writeError(c, http.StatusForbidden, "FORBIDDEN", "no access to this vehicle's media")
		return
	}
	url, perr := appStore.PresignGet(c.Request.Context(), ev.Bucket, ev.ObjectKey, appCfg.Media.PresignTTL)
	if perr != nil {
		slog.Error("media presign get failed", "error", perr, "media_id", id)
		writeError(c, http.StatusServiceUnavailable, "STORAGE_UNAVAILABLE", "unable to issue download URL")
		return
	}
	mediaPresignedTotal.WithLabelValues(companyCodeOf(c)).Inc()
	// Audit trail (B4): catat akses presigned download URL.
	internal.LogAudit(masterDB(), internal.AuditEntry{
		UserID:      authenticatedUserID(c),
		CompanyCode: companyCodeOf(c),
		EventType:   "MEDIA_URL_ACCESS",
		Action:      "media.url",
		Entity:      "media_events",
		EntityID:    strconv.FormatUint(ev.ID, 10),
		IP:          c.ClientIP(),
		UserAgent:   c.Request.UserAgent(),
	})
	writeSuccess(c, http.StatusOK, gin.H{"url": url, "expires_in": int(appCfg.Media.PresignTTL.Seconds())})
}

// mediaDeleteHandler — DELETE /api/v1/media/:id (Admin only, soft-delete FR-8.4).
func mediaDeleteHandler(c *gin.Context) {
	if !isAdminCaller(c) {
		internal.RBACDenialsTotal.WithLabelValues("media.delete", "not_admin").Inc()
		writeError(c, http.StatusForbidden, "FORBIDDEN", "only Admin may delete media")
		return
	}
	id := atoiDefault(c.Param("id"), 0)
	if id == 0 {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid media id")
		return
	}
	db, err := companyDB(c)
	if err != nil {
		writeError(c, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "no company context")
		return
	}
	st := newCompanyStore(companyCodeOf(c), db, appTenant)
	if err := st.SoftDeleteMediaEvent(uint64(id)); err != nil {
		slog.Error("media delete failed", "error", err, "media_id", id)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to delete media")
		return
	}
	writeSuccess(c, http.StatusOK, gin.H{"id": id, "status": "deleted"})
}

// authenticatedUserID returns the caller's master user id (0 when absent).
func authenticatedUserID(c *gin.Context) uint64 {
	if v, ok := c.Get(ctxUserKey); ok {
		if au, ok2 := v.(models.AuthUser); ok2 {
			return au.ID
		}
	}
	return 0
}
