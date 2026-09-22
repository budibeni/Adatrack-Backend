package controllers

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"adatrack_gps/service-media/models"
)

// handleIngestEvent implements `POST /api/v1/media/events` (FR-8.1): multipart
// upload (object stored immediately) or JSON + presigned PUT (ticket first).
func (s *Service) handleIngestEvent(c *gin.Context) {
	ingest, ok := ingestOf(c)
	if !ok {
		s.denyIngest(c, "missing_signature")
		return
	}
	if ingest.Multipart {
		s.handleMultipartIngest(c, ingest)
		return
	}
	s.handleJSONIngest(c, ingest)
}

// handleJSONIngest issues a presigned PUT ticket and records a `pending` row.
func (s *Service) handleJSONIngest(c *gin.Context, ingest *ingestContext) {
	var req models.JSONUploadRequest
	if verr := bindJSON(c, &req); verr != nil {
		respondError(c, verr)
		return
	}
	imei := strings.TrimSpace(req.IMEI)
	ref, aerr := s.resolveDevice(c.Request.Context(), ingest.CompanyCode, imei, req.VehicleID)
	if aerr != nil {
		respondError(c, aerr)
		return
	}
	ext, mimeOK := models.MimeAllowed(req.MimeType)
	if !mimeOK {
		respondError(c, errBadRequest(CodeMediaTypeNotAllowed,
			"content type is not allowed (image/jpeg, video/mp4)"))
		return
	}
	maxBytes := MaxFileBytes(ingest.Config.EffectiveMaxFileMB(s.settings.MaxFileMB))
	if maxBytes > 0 && req.FileSize > maxBytes {
		respondError(c, errBadRequest(CodeMediaTooLarge,
			fmt.Sprintf("file_size exceeds the %d byte limit for this company", maxBytes)))
		return
	}
	capturedAt, terr := parseCapturedAt(req.CapturedAt)
	if terr != nil {
		respondError(c, terr)
		return
	}

	key, kerr := s.objectKey(ingest.CompanyCode, ref.VehicleID, capturedAt, ext)
	if kerr != nil {
		respondError(c, errInternal("could not allocate an object key"))
		return
	}
	uploadURL, perr := s.storage.PresignPut(c.Request.Context(), key, req.MimeType, s.uploadTTL())
	if perr != nil {
		respondError(c, storageError(perr))
		return
	}
	mediaPresigned.WithLabelValues("upload").Inc()

	row := &models.MediaEvent{
		VehicleID:     ref.VehicleID,
		IMEI:          imei,
		EventType:     req.EventType,
		ObjectKey:     key,
		FileSize:      req.FileSize,
		MimeType:      strings.ToLower(strings.TrimSpace(req.MimeType)),
		ContentSHA256: strings.ToLower(strings.TrimSpace(req.ContentSHA256)),
		Status:        models.StatusPending,
		HMACVerified:  true,
		UploadSource:  models.SourceJSON,
		RetentionDays: ingest.Config.EffectiveRetention(s.settings.RetentionDays),
		CapturedAt:    capturedAt,
	}
	id, cerr := s.store.CreateMediaEvent(c.Request.Context(), ingest.CompanyCode, row)
	if cerr != nil {
		respondError(c, storageError(cerr))
		return
	}
	row.ID = id

	s.auditIngest(c, ingest.CompanyCode, row, ActionMediaUploaded, "ingest json")
	slog.Info("media ingest ticket issued", "company", ingest.CompanyCode, "media_id", id,
		"vehicle_id", row.VehicleID, "event_type", row.EventType, "request_id", requestID(c))

	respondCreated(c, models.UploadTicket{
		ID:              id,
		ObjectKey:       key,
		UploadURL:       uploadURL,
		UploadExpiresIn: int(s.uploadTTL().Seconds()),
		Status:          models.StatusPending,
		CompleteURL:     "/api/v1/media/events/" + strconv.FormatInt(id, 10) + "/complete",
	})
}

// uploadTTL bounds the presigned PUT window (short: the agent uploads at once).
func (s *Service) uploadTTL() time.Duration {
	ttl := s.settings.PresignTTL
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return ttl
}
