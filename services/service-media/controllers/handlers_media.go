package controllers

import (
	"bytes"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"adatrack_gps/service-media/models"
)

// handleCompleteEvent implements `POST /api/v1/media/events/:id/complete`
// (FR-8.1/FR-8.3, HMAC tier): the JSON flow finalises the `pending` row once the
// object has landed in the bucket.
func (s *Service) handleCompleteEvent(c *gin.Context) {
	ingest, ok := ingestOf(c)
	if !ok {
		s.denyIngest(c, "missing_signature")
		return
	}
	id, perr := pathID(c)
	if perr != nil {
		respondError(c, perr)
		return
	}
	ctx := c.Request.Context()

	// The body is optional; when present it must carry valid JSON.
	var req models.CompleteRequest
	if len(bytes.TrimSpace(ingest.Body)) > 0 {
		if verr := bindJSON(c, &req); verr != nil {
			respondError(c, verr)
			return
		}
	}

	row, err := s.store.MediaEventByID(ctx, ingest.CompanyCode, id, false)
	if err != nil {
		respondError(c, storageError(err))
		return
	}
	if row == nil {
		respondError(c, errNotFound(CodeMediaNotFound, "media event not found"))
		return
	}
	if row.Status != models.StatusPending {
		respondError(c, errConflict(CodeInvalidTransition,
			"only a pending media event can be completed"))
		return
	}

	obj, herr := s.storage.Head(ctx, row.ObjectKey)
	if herr != nil {
		respondError(c, storageError(herr))
		return
	}
	maxBytes := MaxFileBytes(ingest.Config.EffectiveMaxFileMB(s.settings.MaxFileMB))
	if maxBytes > 0 && obj.Size > maxBytes {
		respondError(c, errBadRequest(CodeMediaTooLarge,
			"uploaded object exceeds the configured limit for this company"))
		return
	}
	if req.FileSize > 0 && req.FileSize != obj.Size {
		respondError(c, errValidation("declared file_size does not match the uploaded object",
			map[string]string{"file_size": "mismatch"}))
		return
	}
	if row.FileSize > 0 && row.FileSize != obj.Size {
		respondError(c, errValidation("uploaded object does not match the size declared at ingest",
			map[string]string{"file_size": "mismatch"}))
		return
	}

	now := time.Now().UTC()
	retention := row.RetentionDays
	if retention <= 0 {
		retention = ingest.Config.EffectiveRetention(s.settings.RetentionDays)
	}
	expires := now.AddDate(0, 0, retention)
	if req.ContentSHA256 != "" {
		row.ContentSHA256 = strings.ToLower(strings.TrimSpace(req.ContentSHA256))
	}
	row.FileSize = obj.Size
	row.ObjectETag = obj.ETag
	row.RetentionDays = retention
	row.CompletedAt = &now
	row.ExpiresAt = &expires
	row.NotifiedAt = &now
	row.Status = models.StatusComplete

	if cerr := s.store.CompleteMediaEvent(ctx, ingest.CompanyCode, row); cerr != nil {
		respondError(c, cerr)
		return
	}

	mediaUploads.WithLabelValues(ingest.CompanyCode, mediaTypeOf(row.MimeType)).Inc()
	mediaUploadBytes.WithLabelValues(ingest.CompanyCode).Add(float64(row.FileSize))
	s.auditIngest(c, ingest.CompanyCode, row, ActionMediaCompleted, "complete")
	s.notifyMediaEvent(ctx, ingest.CompanyCode, row)
	s.updateStorageObjects(ctx, ingest.CompanyCode)

	respondOK(c, row, nil)
}

// handleMediaURL implements `GET /api/v1/media/:id/url` (FR-8.4): a short-lived
// presigned GET plus a FAIL-CLOSED audit row (`MEDIA_URL_ACCESS`, §9.4) — the URL
// is never handed out when it cannot be recorded.
func (s *Service) handleMediaURL(c *gin.Context) {
	identity, _ := currentIdentity(c)
	id, perr := pathID(c)
	if perr != nil {
		respondError(c, perr)
		return
	}
	row, err := s.loadMedia(c, identity, id, false)
	if err != nil {
		respondError(c, err)
		return
	}
	if row.Status == models.StatusPending {
		respondError(c, errConflict(CodeInvalidTransition,
			"media object is not available yet (upload incomplete)"))
		return
	}
	if s.auditor != nil && s.auditor.Enabled() {
		auditErr := s.auditor.Write(c.Request.Context(), AuditRow{
			Action:         ActionMediaURL,
			Outcome:        OutcomeSuccess,
			ActorUserID:    identity.userID,
			ActorEmail:     identity.email,
			ActorRole:      identity.role,
			ActorIP:        c.ClientIP(),
			ActorUserAgent: c.GetHeader("User-Agent"),
			CompanyCode:    identity.companyCode,
			EntityType:     "media_event",
			EntityID:       strconv.FormatInt(row.ID, 10),
			Reason:         "presigned url",
			RequestID:      requestID(c),
		})
		if auditErr != nil {
			// Fail-closed: no audit row ⇒ no access.
			s.countHTTPError(http.StatusServiceUnavailable, CodeServiceUnavailable)
			respondError(c, errUnavailable("audit trail unavailable"))
			return
		}
	}

	purl, perr2 := s.storage.PresignGet(c.Request.Context(), row.ObjectKey, s.settings.PresignTTL)
	if perr2 != nil {
		respondError(c, storageError(perr2))
		return
	}
	mediaPresigned.WithLabelValues("read").Inc()
	respondOK(c, models.URLResponse{URL: purl, ExpiresIn: int(s.settings.PresignTTL.Seconds())}, nil)
}

// handleListMedia implements `GET /api/v1/media` (FR-8.4) with pagination and
// row-level RBAC.
func (s *Service) handleListMedia(c *gin.Context) {
	identity, _ := currentIdentity(c)
	page, limit, perr := s.parsePagination(c)
	if perr != nil {
		respondError(c, perr)
		return
	}
	includeDel, ierr := parseIncludeDeleted(c)
	if ierr != nil {
		respondError(c, ierr)
		return
	}
	if includeDel && identity.role != RoleAdmin {
		respondError(c, errForbidden(CodeForbidden, "only Admin may list deleted media"))
		return
	}

	query := MediaQuery{
		CompanyCode: identity.companyCode,
		EventType:   strings.TrimSpace(c.Query("event_type")),
		Status:      strings.TrimSpace(c.Query("status")),
		IMEI:        strings.TrimSpace(c.Query("imei")),
		AssignedIDs: identity.assigned,
		AllVehicles: identity.allVehicles,
		IncludeDel:  includeDel,
		Page:        page,
		Limit:       limit,
	}
	if query.EventType != "" && !models.ValidEventType(query.EventType) {
		respondError(c, errValidation("event_type is not in the allowlist",
			map[string]string{"event_type": "invalid"}))
		return
	}
	if query.Status != "" && !validStatus(query.Status) {
		respondError(c, errValidation("status is not a known lifecycle value",
			map[string]string{"status": "invalid"}))
		return
	}
	if raw := strings.TrimSpace(c.Query("vehicle_id")); raw != "" {
		id, err := parsePositiveInt(raw)
		if err != nil {
			respondError(c, errValidation("vehicle_id must be a positive integer",
				map[string]string{"vehicle_id": "invalid"}))
			return
		}
		query.VehicleID = id
	}
	from, ferr := parseTimeQuery(c, "from")
	if ferr != nil {
		respondError(c, ferr)
		return
	}
	to, terr := parseTimeQuery(c, "to")
	if terr != nil {
		respondError(c, terr)
		return
	}
	if !from.IsZero() && !to.IsZero() && from.After(to) {
		respondError(c, errValidation("from must be before to", map[string]string{"from": "invalid"}))
		return
	}
	query.From, query.To = from, to

	items, total, err := s.store.ListMediaEvents(c.Request.Context(), query)
	if err != nil {
		respondError(c, storageError(err))
		return
	}
	respondOK(c, items, &models.Pagination{Page: page, Limit: limit, Total: total})
}

// handleMediaDetail implements `GET /api/v1/media/:id` (FR-8.4).
func (s *Service) handleMediaDetail(c *gin.Context) {
	identity, _ := currentIdentity(c)
	id, perr := pathID(c)
	if perr != nil {
		respondError(c, perr)
		return
	}
	includeDel, ierr := parseIncludeDeleted(c)
	if ierr != nil {
		respondError(c, ierr)
		return
	}
	if includeDel && identity.role != RoleAdmin {
		respondError(c, errForbidden(CodeForbidden, "only Admin may view deleted media"))
		return
	}
	row, err := s.loadMedia(c, identity, id, includeDel)
	if err != nil {
		respondError(c, err)
		return
	}
	respondOK(c, row, nil)
}

// loadMedia loads one catalog row and enforces the row-level RBAC check.
func (s *Service) loadMedia(c *gin.Context, identity *tenantIdentity, id int64, includeDel bool) (*models.MediaEvent, *APIError) {
	row, err := s.store.MediaEventByID(c.Request.Context(), identity.companyCode, id, includeDel)
	if err != nil {
		return nil, storageError(err)
	}
	if row == nil {
		return nil, errNotFound(CodeMediaNotFound, "media event not found")
	}
	if !vehicleAllowed(identity, row.VehicleID) {
		return nil, errForbidden(CodeUnauthorizedVehicle,
			"media event exists but its vehicle is not assigned to you")
	}
	return row, nil
}

// validStatus reports whether a lifecycle value is known (whitelist §8.5).
func validStatus(raw string) bool {
	switch raw {
	case models.StatusPending, models.StatusComplete, models.StatusExpired, models.StatusDeleted:
		return true
	default:
		return false
	}
}

// parseTimeQuery validates an RFC3339/date query parameter.
func parseTimeQuery(c *gin.Context, key string) (time.Time, *APIError) {
	raw := strings.TrimSpace(c.Query(key))
	if raw == "" {
		return time.Time{}, nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if ts, err := time.Parse(layout, raw); err == nil {
			return ts.UTC(), nil
		}
	}
	return time.Time{}, errValidation(key+" must be RFC3339 or a date", map[string]string{key: "invalid"})
}
