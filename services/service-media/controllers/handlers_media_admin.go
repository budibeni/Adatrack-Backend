package controllers

import (
	"bytes"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"adatrack_gps/service-media/models"
)

// handleDeleteMedia implements `DELETE /api/v1/media/:id` (FR-8.9, Admin): a SOFT
// delete — the catalog row keeps its history and the physical object stays until
// the retention sweep expires it.
func (s *Service) handleDeleteMedia(c *gin.Context) {
	identity, _ := currentIdentity(c)
	id, perr := pathID(c)
	if perr != nil {
		respondError(c, perr)
		return
	}
	ctx := c.Request.Context()

	row, err := s.store.MediaEventByID(ctx, identity.companyCode, id, false)
	if err != nil {
		respondError(c, storageError(err))
		return
	}
	if row == nil {
		respondError(c, errNotFound(CodeMediaNotFound, "media event not found"))
		return
	}
	if !vehicleAllowed(identity, row.VehicleID) {
		respondError(c, errForbidden(CodeUnauthorizedVehicle,
			"media event exists but its vehicle is not assigned to you"))
		return
	}

	reason := strings.TrimSpace(c.Query("reason"))
	if len(bytes.TrimSpace(readBodyIfPresent(c))) > 0 {
		var req models.DeleteRequest
		if verr := bindJSON(c, &req); verr != nil {
			respondError(c, verr)
			return
		}
		if strings.TrimSpace(req.Reason) != "" {
			reason = strings.TrimSpace(req.Reason)
		}
	}
	if reason == "" {
		reason = "admin soft delete"
	}

	if derr := s.store.SoftDeleteMediaEvent(ctx, identity.companyCode, id, identity.userID, reason); derr != nil {
		respondError(c, derr)
		return
	}
	row.Status = models.StatusDeleted
	row.DeleteReason = reason

	s.auditMutation(c, identity, row, ActionMediaSoftDeleted, reason)
	s.updateStorageObjects(ctx, identity.companyCode)
	respondOK(c, row, nil)
}

// handleRestoreMedia implements `POST /api/v1/media/:id/restore` (§6.0.1, Admin):
// the reason is mandatory and audited as ENTITY_RESTORED.
func (s *Service) handleRestoreMedia(c *gin.Context) {
	identity, _ := currentIdentity(c)
	id, perr := pathID(c)
	if perr != nil {
		respondError(c, perr)
		return
	}
	ctx := c.Request.Context()

	var req models.RestoreRequest
	if verr := bindJSON(c, &req); verr != nil {
		respondError(c, verr)
		return
	}
	row, err := s.store.MediaEventByID(ctx, identity.companyCode, id, true)
	if err != nil {
		respondError(c, storageError(err))
		return
	}
	if row == nil {
		respondError(c, errNotFound(CodeMediaNotFound, "media event not found"))
		return
	}
	if row.DeletedAt == nil {
		respondError(c, errConflict(CodeConflict, "media event is not deleted"))
		return
	}
	if rerr := s.store.RestoreMediaEvent(ctx, identity.companyCode, id); rerr != nil {
		respondError(c, rerr)
		return
	}
	row.DeletedAt = nil
	row.DeleteReason = ""
	if row.ExpiresAt != nil && !row.ExpiresAt.After(time.Now().UTC()) {
		row.Status = models.StatusExpired
	} else {
		row.Status = models.StatusComplete
	}

	s.auditMutation(c, identity, row, ActionMediaRestored, strings.TrimSpace(req.Reason))
	s.updateStorageObjects(ctx, identity.companyCode)
	respondOK(c, row, nil)
}

// maxBodyPeek bounds the pre-read of a small JSON body (delete reason).
const maxBodyPeek = 8 << 10

// readBodyIfPresent returns the request body: the ingest middleware already
// buffered it; JWT routes read it here (bounded) and restore it for the binder.
func readBodyIfPresent(c *gin.Context) []byte {
	if c.Request.Body == nil {
		return nil
	}
	if ingest, ok := ingestOf(c); ok {
		return ingest.Body
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxBodyPeek+1))
	if err != nil || len(body) > maxBodyPeek {
		return nil
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	return body
}

// auditMutation records a delete/restore row (best-effort, retried + dead-lettered).
func (s *Service) auditMutation(c *gin.Context, identity *tenantIdentity, row *models.MediaEvent, action, reason string) {
	if s.auditor == nil {
		return
	}
	_ = s.auditor.Write(c.Request.Context(), AuditRow{
		Action:         action,
		Outcome:        OutcomeSuccess,
		ActorUserID:    identity.userID,
		ActorEmail:     identity.email,
		ActorRole:      identity.role,
		ActorIP:        c.ClientIP(),
		ActorUserAgent: c.GetHeader("User-Agent"),
		CompanyCode:    identity.companyCode,
		EntityType:     "media_event",
		EntityID:       strconv.FormatInt(row.ID, 10),
		AfterState:     row,
		Reason:         reason,
		RequestID:      requestID(c),
	})
}
