package controllers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// auditEntityTypes maps the route resource segment to the audit entity label
// (PRD §9.4). Unknown segments fall back to the upper-snake form of the segment.
var auditEntityTypes = map[string]string{
	"vehicles":         "VEHICLE",
	"geofences":        "GEOFENCE",
	"routes":           "ROUTE",
	"speed-configs":    "SPEED_CONFIG",
	"fuel-configs":     "FUEL_CONFIG",
	"alerts":           "ALERT",
	"audit-logs":       "AUDIT_LOG",
	"drivers":          "DRIVER",
	"groups":           "GROUP",
	"personnel":        "PERSONNEL",
	"cards":            "CARD",
	"access-logs":      "ACCESS_LOG",
	"assets":           "ASSET",
	"incidents":        "INCIDENT",
	"maintenance":      "MAINTENANCE",
	"maintenance-logs": "MAINTENANCE_LOG",
	"organizations":    "ORGANIZATION",
	"integrations":     "INTEGRATION",
	"share-links":      "SHARE_LINK",
	"heatmap":          "HEATMAP",
	"access":           "ACCESS",
	"industry":         "INDUSTRY",
	"reports":          "REPORT",
	"modules":          "MODULE",
}

// auditActionFor derives the audit action from the HTTP method + route pattern.
func auditActionFor(method, route string) string {
	switch method {
	case http.MethodPost:
		switch {
		case strings.HasSuffix(route, "/restore"):
			return ActionEntityRestored
		case strings.HasSuffix(route, "/acknowledge"):
			return ActionAlertAcknowledged
		case strings.HasSuffix(route, "/resolve"):
			return ActionAlertResolved
		case strings.HasSuffix(route, "/commands"):
			return ActionCommandRequested
		case strings.HasSuffix(route, "/share-links"):
			return ActionShareLinkCreated
		default:
			return ActionEntityCreated
		}
	case http.MethodPatch, http.MethodPut:
		switch {
		case strings.Contains(route, "/access/menu/role/"):
			return ActionMenuAccessUpdated
		case strings.Contains(route, "/modules/"):
			return ActionModuleLicenseSet
		case strings.Contains(route, "/integrations/"):
			return ActionIntegrationUpdated
		default:
			return ActionEntityUpdated
		}
	case http.MethodDelete:
		switch {
		case strings.Contains(route, "/integrations/"):
			return ActionIntegrationRemoved
		case strings.HasSuffix(route, "/share-links/:id") || strings.Contains(route, "/share-links/"):
			return ActionShareLinkRevoked
		default:
			return ActionEntitySoftDeleted
		}
	default:
		return ""
	}
}

// auditEntityFor extracts the entity label from the route pattern.
func auditEntityFor(route string) string {
	segments := strings.Split(strings.Trim(route, "/"), "/")
	// /api/v1/<resource>/... → index 2 is the resource.
	if len(segments) < 3 {
		return ""
	}
	resource := segments[2]
	if label, ok := auditEntityTypes[resource]; ok {
		return label
	}
	return strings.ToUpper(strings.ReplaceAll(resource, "-", "_"))
}

// auditMutationMiddleware writes the mandatory tm_audit_logs row for EVERY
// mutating request (PRD §9.4). It is mounted on the authenticated tenant group,
// so the actor/tenant are already resolved; GET/HEAD/OPTIONS are ignored.
//
// before/after snapshots: api-vehicle does not re-read the row before mutation,
// so the redacted request payload is recorded as `after_state` (the intended
// change) and DELETE/restore bodies contribute `reason`. The immutable outcome,
// actor, tenant, entity and correlation id are always exact.
func (s *Service) auditMutationMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		action := auditActionFor(c.Request.Method, c.FullPath())
		if action == "" {
			c.Next()
			return
		}
		var payload any
		var reason string
		if raw := captureAuditBody(c); len(raw) > 0 {
			var decoded map[string]any
			if err := json.Unmarshal(raw, &decoded); err == nil {
				payload = decoded
				if r, ok := decoded["reason"].(string); ok {
					reason = r
				}
			}
		}

		c.Next()

		if s.auditor == nil || !s.auditor.Enabled() {
			return
		}
		status := c.Writer.Status()
		outcome := OutcomeSuccess
		switch {
		case status == http.StatusUnauthorized || status == http.StatusForbidden:
			outcome = OutcomeDenied
		case status >= http.StatusBadRequest:
			outcome = OutcomeFailure
		}
		row := AuditRow{
			Action:         action,
			Outcome:        outcome,
			CompanyCode:    "",
			EntityType:     auditEntityFor(c.FullPath()),
			EntityID:       c.Param("id"),
			Reason:         reason,
			RequestID:      requestID(c),
			AfterState:     payload,
			ActorIP:        c.ClientIP(),
			ActorUserAgent: c.GetHeader("User-Agent"),
		}
		if identity, ok := currentIdentity(c); ok && identity != nil {
			row.ActorUserID = identity.userID
			row.ActorEmail = identity.email
			row.ActorRole = identity.role
			row.CompanyCode = identity.companyCode
		}
		if row.Outcome == OutcomeDenied && row.Reason == "" {
			row.Reason = "request denied"
		}
		s.recordAudit(c, row)
	}
}

// captureAuditBody reads (and restores) the request body for the audit payload.
func captureAuditBody(c *gin.Context) []byte {
	if c.Request.Body == nil {
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
	if err != nil {
		return nil
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(raw))
	return raw
}

// recordAudit persists one row + feeds the audit metrics.
func (s *Service) recordAudit(c *gin.Context, row AuditRow) {
	auditEvents.WithLabelValues(row.Action, row.Outcome).Inc()
	s.auditor.Write(c.Request.Context(), row)
}
