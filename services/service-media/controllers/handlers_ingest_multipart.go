package controllers

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"adatrack_gps/service-media/models"
)

// maxFormFieldBytes bounds one multipart text field (imei/event_type/...).
const maxFormFieldBytes = 1 << 10

// maxMultipartParts bounds the number of multipart parts (PRD §8.5 rule 4).
const maxMultipartParts = 12

// handleMultipartIngest stores the uploaded object and records a `complete` row.
func (s *Service) handleMultipartIngest(c *gin.Context, ingest *ingestContext) {
	form, aerr := s.readMultipartParts(c, ingest)
	if aerr != nil {
		respondError(c, aerr)
		return
	}
	// The multipart signature binds metadata + content (documented in
	// requireHMAC) and can only be checked once both are known.
	signed := form.IMEI + "\n" + form.EventType + "\n" + string(form.Data)
	if !VerifyHMAC(ingest.Secret, []byte(signed), c.GetHeader("X-Signature")) {
		s.denyIngest(c, "bad_signature")
		return
	}

	imei := strings.TrimSpace(form.IMEI)
	ref, verr := s.resolveDevice(c.Request.Context(), ingest.CompanyCode, imei, form.VehicleID)
	if verr != nil {
		respondError(c, verr)
		return
	}
	ext, mimeOK := models.MimeAllowed(form.MimeType)
	if !mimeOK {
		respondError(c, errBadRequest(CodeMediaTypeNotAllowed,
			"content type is not allowed (image/jpeg, video/mp4)"))
		return
	}
	maxBytes := MaxFileBytes(ingest.Config.EffectiveMaxFileMB(s.settings.MaxFileMB))
	if maxBytes > 0 && int64(len(form.Data)) > maxBytes {
		respondError(c, errBadRequest(CodeMediaTooLarge,
			fmt.Sprintf("upload exceeds the %d byte limit for this company", maxBytes)))
		return
	}

	key, kerr := s.objectKey(ingest.CompanyCode, ref.VehicleID, form.CapturedAt, ext)
	if kerr != nil {
		respondError(c, errInternal("could not allocate an object key"))
		return
	}
	obj, perr := s.storage.Put(c.Request.Context(), key, form.Data, form.MimeType)
	if perr != nil {
		respondError(c, storageError(perr))
		return
	}

	now := time.Now().UTC()
	retention := ingest.Config.EffectiveRetention(s.settings.RetentionDays)
	expires := now.AddDate(0, 0, retention)
	sum := sha256.Sum256(form.Data)
	row := &models.MediaEvent{
		VehicleID:     ref.VehicleID,
		IMEI:          imei,
		EventType:     form.EventType,
		ObjectKey:     key,
		FileSize:      int64(len(form.Data)),
		MimeType:      strings.ToLower(strings.TrimSpace(form.MimeType)),
		ContentSHA256: hex.EncodeToString(sum[:]),
		Status:        models.StatusComplete,
		HMACVerified:  true,
		UploadSource:  models.SourceMultipart,
		ObjectETag:    obj.ETag,
		RetentionDays: retention,
		CapturedAt:    form.CapturedAt,
		CompletedAt:   &now,
		ExpiresAt:     &expires,
	}
	id, cerr := s.store.CreateMediaEvent(c.Request.Context(), ingest.CompanyCode, row)
	if cerr != nil {
		// The object is already stored: delete it so no orphan remains.
		if derr := s.storage.Delete(c.Request.Context(), key); derr != nil {
			slog.Error("media: orphan object cleanup failed", "key", key, "error", derr)
		}
		respondError(c, storageError(cerr))
		return
	}
	row.ID = id

	mediaUploads.WithLabelValues(ingest.CompanyCode, mediaTypeOf(row.MimeType)).Inc()
	mediaUploadBytes.WithLabelValues(ingest.CompanyCode).Add(float64(row.FileSize))
	s.auditIngest(c, ingest.CompanyCode, row, ActionMediaUploaded, "ingest multipart")
	s.notifyMediaEvent(c.Request.Context(), ingest.CompanyCode, row)
	s.updateStorageObjects(c.Request.Context(), ingest.CompanyCode)

	slog.Info("media ingested (multipart)", "company", ingest.CompanyCode, "media_id", id,
		"vehicle_id", row.VehicleID, "bytes", row.FileSize, "request_id", requestID(c))
	respondCreated(c, row)
}

// readMultipartParts walks the multipart body collecting the documented fields
// (imei, event_type, captured_at, vehicle_id, file). Bounds: part count, field
// length and total file size (PRD §8.5 rule 4).
func (s *Service) readMultipartParts(c *gin.Context, ingest *ingestContext) (*multipartIngest, *APIError) {
	mr := multipart.NewReader(bytes.NewReader(ingest.Body), boundaryOf(c.GetHeader("Content-Type")))
	out := &multipartIngest{CapturedAt: time.Now().UTC(), MimeType: "application/octet-stream"}
	maxBytes := MaxFileBytes(ingest.Config.EffectiveMaxFileMB(s.settings.MaxFileMB))
	if maxBytes <= 0 {
		maxBytes = 1 << 30
	}
	parts := 0
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, errValidation("malformed multipart body", map[string]string{"body": "malformed"})
		}
		parts++
		if parts > maxMultipartParts {
			return nil, errValidation("too many multipart parts", map[string]string{"body": "too many parts"})
		}
		name := strings.ToLower(strings.TrimSpace(part.FormName()))
		if name == "file" || name == "media" || name == "upload" {
			data, rerr := io.ReadAll(io.LimitReader(part, maxBytes+1))
			if rerr != nil {
				return nil, errValidation("could not read the uploaded file",
					map[string]string{"file": "unreadable"})
			}
			if int64(len(data)) > maxBytes {
				return nil, errBadRequest(CodeMediaTooLarge,
					fmt.Sprintf("upload exceeds the %d byte limit for this company", maxBytes))
			}
			out.Data = data
			if ct := strings.TrimSpace(part.Header.Get("Content-Type")); ct != "" {
				out.MimeType = ct
			}
			continue
		}
		value, rerr := io.ReadAll(io.LimitReader(part, maxFormFieldBytes+1))
		if rerr != nil {
			return nil, errValidation("could not read a form field", map[string]string{name: "unreadable"})
		}
		if len(value) > maxFormFieldBytes {
			return nil, errValidation("form field too long", map[string]string{name: "too long"})
		}
		switch name {
		case "imei":
			out.IMEI = strings.TrimSpace(string(value))
		case "event_type":
			out.EventType = strings.TrimSpace(string(value))
		case "captured_at":
			ts, terr := parseCapturedAt(strings.TrimSpace(string(value)))
			if terr != nil {
				return nil, terr
			}
			out.CapturedAt = ts
		case "vehicle_id":
			id, perr := parsePositiveInt(strings.TrimSpace(string(value)))
			if perr != nil {
				return nil, errValidation("vehicle_id must be a positive integer",
					map[string]string{"vehicle_id": "invalid"})
			}
			out.VehicleID = id
		}
	}

	if out.IMEI == "" || out.EventType == "" || len(out.Data) == 0 {
		return nil, errValidation("imei, event_type and a file part are required",
			map[string]string{"imei": "required", "event_type": "required", "file": "required"})
	}
	if !models.ValidEventType(out.EventType) {
		return nil, errValidation("event_type is not in the allowlist",
			map[string]string{"event_type": "invalid"})
	}
	return out, nil
}

// boundaryOf extracts the multipart boundary from the Content-Type header.
func boundaryOf(contentType string) string {
	lower := strings.ToLower(contentType)
	idx := strings.Index(lower, "boundary=")
	if idx < 0 {
		return ""
	}
	value := strings.TrimSpace(contentType[idx+len("boundary="):])
	if cut := strings.Index(value, ";"); cut >= 0 {
		value = value[:cut]
	}
	return strings.Trim(strings.TrimSpace(value), `"`)
}

// multipartIngest is the parsed multipart payload.
type multipartIngest struct {
	IMEI       string
	EventType  string
	VehicleID  int64
	MimeType   string
	CapturedAt time.Time
	Data       []byte
}
