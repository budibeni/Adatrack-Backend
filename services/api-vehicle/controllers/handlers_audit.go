package controllers

import (
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// handleListAuditLogs implements GET /api/v1/audit-logs (Admin only): the tenant
// slice of the append-only audit trail (PRD §9.4). Reading the trail is itself an
// audited action (AUDIT_LOGS_VIEWED) so access to history is never invisible.
func (s *Service) handleListAuditLogs(c *gin.Context) {
	identity, _ := currentIdentity(c)
	page, limit, perr := s.parsePagination(c)
	if perr != nil {
		respondError(c, perr)
		return
	}
	query := AuditLogQuery{
		CompanyCode: identity.companyCode,
		Action:      strings.TrimSpace(c.Query("action")),
		Outcome:     strings.TrimSpace(c.Query("outcome")),
		EntityType:  strings.ToUpper(strings.TrimSpace(c.Query("entity_type"))),
		Page:        page,
		Limit:       limit,
	}
	if outcome := query.Outcome; outcome != "" &&
		outcome != OutcomeSuccess && outcome != OutcomeFailure && outcome != OutcomeDenied {
		respondError(c, errValidation("invalid outcome filter",
			map[string]string{"outcome": "must be one of: success failure denied"}))
		return
	}
	if raw := strings.TrimSpace(c.Query("actor_user_id")); raw != "" {
		actor, err := parsePositiveInt(raw)
		if err != nil {
			respondError(c, errValidation("invalid actor_user_id",
				map[string]string{"actor_user_id": "must be a positive integer"}))
			return
		}
		query.ActorUserID = actor
	}
	from, ferr := parseAuditTime(c.Query("from"), "from")
	if ferr != nil {
		respondError(c, ferr)
		return
	}
	to, terr := parseAuditTime(c.Query("to"), "to")
	if terr != nil {
		respondError(c, terr)
		return
	}
	query.From, query.To = from, to

	items, total, err := s.store.ListAuditLogs(c.Request.Context(), query)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	if s.auditor != nil && s.auditor.Enabled() {
		s.recordAudit(c, AuditRow{
			Action:         ActionAuditLogsViewed,
			Outcome:        OutcomeSuccess,
			ActorUserID:    identity.userID,
			ActorEmail:     identity.email,
			ActorRole:      identity.role,
			CompanyCode:    identity.companyCode,
			EntityType:     "AUDIT_LOG",
			RequestID:      requestID(c),
			ActorIP:        c.ClientIP(),
			ActorUserAgent: c.GetHeader("User-Agent"),
		})
	}
	respondOK(c, items, pagination(page, limit, total))
}

// parseAuditTime parses an RFC3339 bound; an empty value means "unbounded".
func parseAuditTime(raw, field string) (*time.Time, *APIError) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, errValidation("invalid "+field+" timestamp",
			map[string]string{field: "must be RFC3339, e.g. 2026-09-26T00:00:00Z"})
	}
	return &parsed, nil
}
