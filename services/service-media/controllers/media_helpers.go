package controllers

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"adatrack_gps/internal/storage"
	"adatrack_gps/service-media/models"
)

// resolveDevice validates an IMEI against the master allowlist and returns the
// tenant vehicle (anti-spoofing FR-1.4 + cross-tenant guard FR-3.2). A declared
// vehicle_id must match the IMEI mapping (anti-IDOR).
func (s *Service) resolveDevice(ctx context.Context, company, imei string, declaredVehicle int64) (*VehicleRef, *APIError) {
	imei = strings.TrimSpace(imei)
	if len(imei) != 15 || strings.IndexFunc(imei, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return nil, errValidation("imei must be exactly 15 digits", map[string]string{"imei": "invalid"})
	}
	ref, err := s.store.ResolveVehicleByIMEI(ctx, imei)
	if err != nil {
		return nil, errUnavailable("device registry unavailable")
	}
	if ref == nil || !ref.IsActive || ref.VehicleID <= 0 {
		return nil, errNotFound(CodeVehicleNotFound, "device is not registered")
	}
	if !strings.EqualFold(ref.CompanyCode, company) {
		return nil, errForbidden(CodeForbidden, "device belongs to a different company")
	}
	if declaredVehicle > 0 && declaredVehicle != ref.VehicleID {
		return nil, errForbidden(CodeForbidden, "vehicle_id does not match the device mapping")
	}
	vehicle, verr := s.store.VehicleByID(ctx, company, ref.VehicleID, false)
	if verr != nil {
		return nil, errUnavailable("vehicle lookup unavailable")
	}
	if vehicle == nil {
		return nil, errNotFound(CodeVehicleNotFound, "vehicle is not active for this company")
	}
	return ref, nil
}

// parseCapturedAt validates the optional capture timestamp (RFC3339 or date).
// Empty means "now"; a future timestamp beyond the replay skew is rejected.
func parseCapturedAt(raw string) (time.Time, *APIError) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Now().UTC(), nil
	}
	for _, layout := range []string{time.RFC3339, time.RFC3339Nano, "2006-01-02"} {
		if ts, err := time.Parse(layout, raw); err == nil {
			ts = ts.UTC()
			if ts.After(time.Now().UTC().Add(5 * time.Minute)) {
				return time.Time{}, errValidation("captured_at is in the future",
					map[string]string{"captured_at": "in the future"})
			}
			return ts, nil
		}
	}
	return time.Time{}, errValidation("captured_at must be RFC3339 or a date",
		map[string]string{"captured_at": "invalid"})
}

// storageError maps an object-storage failure onto the PRD §8.1 contract: a
// missing object is 404, an unreachable backend is 503 (never a bare 500).
func storageError(err error) *APIError {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, storage.ErrNotFound):
		return errNotFound(CodeMediaNotFound, "media object not found")
	case errors.Is(err, storage.ErrUnavailable):
		slog.Error("service-media: object storage unavailable", "error", err)
		return errUnavailable("object storage unavailable")
	default:
		slog.Error("service-media: object storage error", "error", err)
		return errUnavailable("object storage error")
	}
}

// mediaTypeOf buckets a mime type for the FR-8.8 metric label.
func mediaTypeOf(mime string) string {
	switch {
	case strings.HasPrefix(strings.ToLower(mime), "image/"):
		return "image"
	case strings.HasPrefix(strings.ToLower(mime), "video/"):
		return "video"
	default:
		return "other"
	}
}

// auditIngest records an ingest/manipulation row (best-effort: the mutation has
// already happened, failures are retried + dead-lettered by the Auditor).
func (s *Service) auditIngest(c *gin.Context, company string, row *models.MediaEvent, action, reason string) {
	if s.auditor == nil {
		return
	}
	_ = s.auditor.Write(c.Request.Context(), AuditRow{
		Action:         action,
		Outcome:        OutcomeSuccess,
		ActorIP:        c.ClientIP(),
		ActorUserAgent: c.GetHeader("User-Agent"),
		CompanyCode:    company,
		EntityType:     "media_event",
		EntityID:       itoa64(row.ID),
		AfterState:     row,
		Reason:         reason,
		RequestID:      requestID(c),
	})
}

// notifyMediaEvent publishes the FR-8.5 frame with a short-lived presigned URL so
// the dashboard can render the event immediately. A publish failure is logged
// (the catalog remains the source of truth) and counted.
func (s *Service) notifyMediaEvent(ctx context.Context, company string, row *models.MediaEvent) {
	data := models.MediaEventData{
		ID:          row.ID,
		CompanyCode: strings.ToUpper(strings.TrimSpace(company)),
		VehicleID:   row.VehicleID,
		IMEI:        row.IMEI,
		EventType:   row.EventType,
		ObjectKey:   row.ObjectKey,
		MimeType:    row.MimeType,
		FileSize:    row.FileSize,
		Status:      row.Status,
		CapturedAt:  row.CapturedAt.UTC().Format(time.RFC3339),
	}
	if url, err := s.storage.PresignGet(ctx, row.ObjectKey, s.settings.PresignTTL); err == nil {
		data.URL = url
	} else {
		slog.Warn("service-media: presign for the WS payload failed", "media_id", row.ID, "error", err)
	}
	if err := s.publishMediaEvent(company, data); err != nil {
		slog.Error("service-media: media.event publish failed", "media_id", row.ID, "error", err)
	}
}

// itoa64 renders an id for the audit entity_id column.
func itoa64(v int64) string {
	if v == 0 {
		return ""
	}
	return strconv.FormatInt(v, 10)
}
