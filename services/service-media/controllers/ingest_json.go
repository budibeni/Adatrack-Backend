package controllers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"ajb_gps/service-media/models"

	"github.com/gin-gonic/gin"
)

// ingestJSON — Flow B: JSON metadata → reserve catalog row (uploaded) → return
// presigned PUT URL so the device/gateway uploads the object itself (FR-8.1).
func ingestJSON(c *gin.Context, cfg *mediaConfig, raw []byte) {
	var req models.IngestionEvent
	if err := json.Unmarshal(raw, &req); err != nil {
		mediaIngestErrorsTotal.WithLabelValues(cfg.companyCode, "json").Inc()
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid JSON body")
		return
	}
	if req.VehicleID == 0 {
		mediaIngestErrorsTotal.WithLabelValues(cfg.companyCode, "vehicle").Inc()
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "vehicle_id is required")
		return
	}
	mimeType := lowerTrim(req.MimeType)
	if !allowedContentTypes[mimeType] {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "mime_type must be image/jpeg or video/mp4")
		return
	}
	if !allowedMediaTypes[req.MediaType] {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "media_type must be photo or video_clip")
		return
	}
	if !allowedTriggerTypes[req.TriggerType] {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid trigger_type")
		return
	}

	db, err := companyDBForCode(cfg.companyCode)
	if err != nil {
		writeError(c, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "company unavailable")
		return
	}
	st := newCompanyStore(cfg.companyCode, db, appTenant)
	vmImei, vstatus, err := st.VehicleByID(req.VehicleID)
	if err != nil {
		writeError(c, http.StatusNotFound, "VEHICLE_NOT_FOUND", "vehicle not found in this company")
		return
	}
	if vstatus != "active" {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "vehicle not active")
		return
	}
	imei := vmImei
	if req.IMEI != "" {
		imei = req.IMEI
	}

	takenAt := time.Now().UTC()
	if req.TakenAt != nil {
		takenAt = req.TakenAt.UTC()
	}
	uuid := newUUID()
	key := buildObjectKey(cfg.companyCode, u64str(req.VehicleID), takenAt, uuid)

	ev := models.MediaEvent{
		VehicleID:   req.VehicleID,
		IMEI:        imei,
		MediaType:   req.MediaType,
		TriggerType: req.TriggerType,
		ObjectKey:   key,
		Bucket:      cfg.bucket,
		SizeBytes:   0,
		DurationSec: req.DurationSec,
		MimeType:    mimeType,
		SHA256:      req.SHA256,
		Status:      "uploaded",
		TakenAt:     takenAt,
		Meta:        req.Meta,
	}
	id, ierr := st.InsertMediaEvent(ev)
	if ierr != nil {
		slog.Error("media ingest: reserve catalog failed", "error", ierr, "company", cfg.companyCode, "key", key)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to reserve media event")
		return
	}
	mediaPresignedTotal.WithLabelValues(cfg.companyCode).Inc()

	url, uerr := appStore.PresignPut(c.Request.Context(), cfg.bucket, key, appCfg.Media.PresignTTL)
	if uerr != nil {
		slog.Error("media ingest: presign put failed", "error", uerr, "company", cfg.companyCode, "key", key)
		_ = st.SetMediaStatus(id, "failed")
		mediaIngestErrorsTotal.WithLabelValues(cfg.companyCode, "presign").Inc()
		writeError(c, http.StatusServiceUnavailable, "STORAGE_UNAVAILABLE", "unable to presign upload URL")
		return
	}

	writeSuccess(c, http.StatusCreated, gin.H{
		"media_id":   id,
		"object_key": key,
		"status":     "uploaded",
		"upload_url": url,
	})
}

// companyDBForCode resolves a company pool WITHOUT an auth context (ingest is
// HMAC-authenticated at company level, not per-user).
func companyDBForCode(code string) (*sql.DB, error) {
	if appTenant == nil {
		return nil, errors.New("tenant manager unavailable")
	}
	return appTenant.DB(code)
}

func lowerTrim(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func u64str(v uint64) string {
	// simplest conversion without strconv import churn
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
