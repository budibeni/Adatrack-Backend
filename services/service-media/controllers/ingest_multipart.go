package controllers

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"ajb_gps/service-media/models"

	"github.com/gin-gonic/gin"
)

// ingestMultipart — Flow A: multipart/form-data direct upload (FR-8.1).
func ingestMultipart(c *gin.Context, cfg *mediaConfig) {
	companyCode := cfg.companyCode
	vehicleID := uint64(atoiDefault(c.PostForm("vehicle_id"), 0))
	imei := strings.TrimSpace(c.PostForm("imei"))
	mediaType := strings.ToLower(c.PostForm("media_type"))
	triggerType := strings.ToLower(c.PostForm("trigger_type"))
	duration := atoiDefault(c.PostForm("duration_seconds"), 0)

	if vehicleID == 0 {
		mediaIngestErrorsTotal.WithLabelValues(companyCode, "vehicle").Inc()
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "vehicle_id is required")
		return
	}
	if !allowedMediaTypes[mediaType] {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "media_type must be photo or video_clip")
		return
	}
	if !allowedTriggerTypes[triggerType] {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid trigger_type")
		return
	}

	db, err := companyDBForCode(companyCode)
	if err != nil {
		mediaIngestErrorsTotal.WithLabelValues(companyCode, "tenant").Inc()
		writeError(c, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "company unavailable")
		return
	}
	st := newCompanyStore(companyCode, db, appTenant)
	vmImei, vstatus, err := st.VehicleByID(vehicleID)
	if err != nil {
		mediaIngestErrorsTotal.WithLabelValues(companyCode, "vehicle").Inc()
		writeError(c, http.StatusNotFound, "VEHICLE_NOT_FOUND", "vehicle not found in this company")
		return
	}
	if vstatus != "active" {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "vehicle not active")
		return
	}
	if imei == "" {
		imei = vmImei
	}
	if vmImei != "" && imei != vmImei {
		imei = vmImei // authoritative company DB wins
	}

	fileHeader, _ := c.FormFile("file")
	if fileHeader == nil {
		mediaIngestErrorsTotal.WithLabelValues(companyCode, "file").Inc()
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "file field is required")
		return
	}
	mimeType := fileHeader.Header.Get("Content-Type")
	if mimeType == "" {
		name := strings.ToLower(fileHeader.Filename)
		switch {
		case strings.HasSuffix(name, ".jpg"), strings.HasSuffix(name, ".jpeg"):
			mimeType = "image/jpeg"
		case strings.HasSuffix(name, ".mp4"):
			mimeType = "video/mp4"
		}
	}
	if !allowedContentTypes[mimeType] {
		mediaIngestErrorsTotal.WithLabelValues(companyCode, "content_type").Inc()
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "content-type must be image/jpeg or video/mp4")
		return
	}
	if fileHeader.Size > int64(cfg.maxFileMB)*1024*1024 {
		mediaIngestErrorsTotal.WithLabelValues(companyCode, "too_large").Inc()
		writeError(c, http.StatusBadRequest, "FILE_TOO_LARGE", fmt.Sprintf("file exceeds max %d MB", cfg.maxFileMB))
		return
	}

	takenAt := parseTakenAt(c.PostForm("taken_at"))
	uuid := newUUID()
	key := buildObjectKey(companyCode, fmt.Sprintf("%d", vehicleID), takenAt, uuid)

	f, err := fileHeader.Open()
	if err != nil {
		mediaIngestErrorsTotal.WithLabelValues(companyCode, "file_open").Inc()
		writeError(c, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "unable to read upload")
		return
	}
	defer f.Close()

	size, perr := appStore.PutObject(c.Request.Context(), cfg.bucket, key, f, fileHeader.Size, mimeType)
	if perr != nil {
		slog.Error("media ingest: s3 put failed", "error", perr, "company", companyCode, "key", key)
		mediaIngestErrorsTotal.WithLabelValues(companyCode, "storage").Inc()
		writeError(c, http.StatusServiceUnavailable, "STORAGE_UNAVAILABLE", "object store put failed")
		return
	}

	mediaUploadsTotal.WithLabelValues(companyCode, mediaType).Inc()
	mediaUploadBytesTotal.WithLabelValues(companyCode).Add(float64(size))

	var durPtr *int
	if duration > 0 {
		durPtr = &duration
	}
	ev := models.MediaEvent{
		VehicleID:   vehicleID,
		IMEI:        imei,
		MediaType:   mediaType,
		TriggerType: triggerType,
		ObjectKey:   key,
		Bucket:      cfg.bucket,
		SizeBytes:   size,
		DurationSec: durPtr,
		MimeType:    mimeType,
		SHA256:      strings.TrimSpace(c.PostForm("sha256")),
		Status:      "available",
		TakenAt:     takenAt,
	}
	id, ierr := st.InsertMediaEvent(ev)
	if ierr != nil {
		slog.Error("media ingest: insert catalog failed", "error", ierr, "company", companyCode, "key", key)
		mediaIngestErrorsTotal.WithLabelValues(companyCode, "catalog").Inc()
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to record media event")
		return
	}

	publishMediaEvent(companyCode, id, vehicleID, imei, mediaType, triggerType, "available", size, takenAt)
	writeSuccess(c, http.StatusCreated, gin.H{"media_id": id, "object_key": key, "status": "available"})
}
