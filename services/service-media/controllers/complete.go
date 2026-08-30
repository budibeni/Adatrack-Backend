package controllers

import (
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// mediaCompleteHandler — POST /api/v1/media/events/:id/complete (HMAC, FR-8.1).
// Setelah device/gateway meng-upload objek melalui presigned PUT (alur JSON),
// endpoint ini mem-finalize katalog: stat objek di storage → status
// 'uploaded' → 'available' + isi size_bytes (FR-8.3), lalu publish
// media.event.<company> supaya WS MEDIA_EVENT diterima klien berhak.
func mediaCompleteHandler(c *gin.Context) {
	id := atoiDefault(c.Param("id"), 0)
	if id == 0 {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid media id")
		return
	}
	cfg, err := resolveMediaConfig(c.GetHeader("X-Company-Code"))
	if err != nil {
		mediaIngestErrorsTotal.WithLabelValues("?", "config").Inc()
		writeError(c, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "media config unavailable")
		return
	}

	// Verify HMAC over the raw body (exact bytes, no mutation of it).
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		mediaIngestErrorsTotal.WithLabelValues(cfg.companyCode, "read").Inc()
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "unable to read request body")
		return
	}
	if !verifySignatureHex(c.GetHeader("X-Signature"), cfg.hmacSecret, raw) {
		slog.Warn("media complete: HMAC mismatch", "company", cfg.companyCode)
		mediaIngestErrorsTotal.WithLabelValues(cfg.companyCode, "hmac").Inc()
		writeError(c, http.StatusUnauthorized, "UNAUTHORIZED", "invalid X-Signature (HMAC mismatch)")
		return
	}

	db, err := companyDBForCode(cfg.companyCode)
	if err != nil {
		mediaIngestErrorsTotal.WithLabelValues(cfg.companyCode, "tenant").Inc()
		writeError(c, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "company unavailable")
		return
	}
	st := newCompanyStore(cfg.companyCode, db, appTenant)

	ev, err := st.GetMediaEvent(uint64(id))
	if err != nil {
		writeError(c, http.StatusNotFound, "MEDIA_NOT_FOUND", "media event not found")
		return
	}
	if ev.Status == "available" {
		writeError(c, http.StatusConflict, "ALREADY_AVAILABLE", "media already available")
		return
	}
	if ev.Status != "uploaded" {
		writeError(c, http.StatusGone, "MEDIA_EXPIRED", "media is not in an uploadable state")
		return
	}

	// Confirm the object exists and capture its real size (no silent drop).
	size, serr := appStore.Stat(c.Request.Context(), ev.Bucket, ev.ObjectKey)
	if serr != nil {
		slog.Error("media complete: stat object failed", "error", serr,
			"company", cfg.companyCode, "key", ev.ObjectKey)
		mediaIngestErrorsTotal.WithLabelValues(cfg.companyCode, "stat").Inc()
		writeError(c, http.StatusNotFound, "OBJECT_NOT_FOUND", "object not yet uploaded")
		return
	}

	if err := st.CompleteMediaEvent(uint64(id), size); err != nil {
		slog.Error("media complete: finalize failed", "error", err, "company", cfg.companyCode, "media_id", id)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to finalize media")
		return
	}

	mediaUploadsTotal.WithLabelValues(cfg.companyCode, ev.MediaType).Inc()
	mediaUploadBytesTotal.WithLabelValues(cfg.companyCode).Add(float64(size))

	publishMediaEvent(cfg.companyCode, ev.ID, ev.VehicleID, ev.IMEI, ev.MediaType,
		ev.TriggerType, "available", size, ev.TakenAt)

	writeSuccess(c, http.StatusOK, gin.H{
		"media_id": id, "status": "available", "size_bytes": size,
		"object_key": ev.ObjectKey, "complete_at": time.Now().UTC().Format(time.RFC3339),
	})
}