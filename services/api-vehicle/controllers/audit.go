package controllers

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"adatrack_gps/internal"
)

// Audit actions of api-vehicle (PRD §9.4 catalogue, shared vocabulary with
// service-websocket/service-media so a platform-wide audit query is possible).
const (
	ActionEntityCreated      = "ENTITY_CREATED"
	ActionEntityUpdated      = "ENTITY_UPDATED"
	ActionEntitySoftDeleted  = "ENTITY_SOFT_DELETED"
	ActionEntityRestored     = "ENTITY_RESTORED"
	ActionAlertAcknowledged  = "ALERT_ACKNOWLEDGED"
	ActionAlertResolved      = "ALERT_RESOLVED"
	ActionCommandRequested   = "COMMAND_REQUESTED"
	ActionAccessDenied       = "ACCESS_DENIED"
	ActionAuditLogsViewed    = "AUDIT_LOGS_VIEWED"
	ActionModuleLicenseSet   = "MODULE_LICENSE_SET"
	ActionIntegrationCreated = "INTEGRATION_CREATED"
	ActionIntegrationUpdated = "INTEGRATION_UPDATED"
	ActionIntegrationRemoved = "INTEGRATION_SOFT_DELETED"
	ActionShareLinkCreated   = "SHARE_LINK_CREATED"
	ActionShareLinkRevoked   = "SHARE_LINK_REVOKED"
	ActionMenuAccessUpdated  = "MENU_ACCESS_UPDATED"
)

// Audit outcomes (PRD §9.4).
const (
	OutcomeSuccess = "success"
	OutcomeFailure = "failure"
	OutcomeDenied  = "denied"
)

// AuditRow is one append-only `tm_audit_logs` row (master schema).
type AuditRow struct {
	Action         string
	Outcome        string
	ActorUserID    int64
	ActorEmail     string
	ActorRole      string
	ActorIP        string
	ActorUserAgent string
	CompanyCode    string
	EntityType     string
	EntityID       string
	BeforeState    any
	AfterState     any
	Reason         string
	RequestID      string
}

// AuditStore persists audit rows (implemented by PostgresStore, faked in tests).
type AuditStore interface {
	WriteAudit(ctx context.Context, rows []AuditRow) error
}

// Auditor writes the mandatory audit trail (PRD §9.4). api-vehicle mutations are
// data mutations (no credential/secret change — that fail-closed path belongs to
// service-websocket provisioning/login), so a write failure is never silent but
// does not reject the already-applied mutation: it is retried, counted
// (`deadletter_total{kind="audit"}`) and dead-lettered for replay.
type Auditor struct {
	store   AuditStore
	nats    *internal.NATSClient
	enabled bool
}

// NewAuditor builds the audit writer.
func NewAuditor(store AuditStore, nats *internal.NATSClient, enabled bool) *Auditor {
	return &Auditor{store: store, nats: nats, enabled: enabled}
}

// Enabled reports whether audit persistence is active.
func (a *Auditor) Enabled() bool { return a != nil && a.enabled && a.store != nil }

// Write persists one row (bounded retry + backoff) and dead-letters a definitive
// failure. It never returns an error to the caller on purpose: the mutation has
// already been applied, so refusing the response would misreport the state.
func (a *Auditor) Write(ctx context.Context, row AuditRow) {
	if !a.Enabled() {
		return
	}
	if row.Outcome == "" {
		row.Outcome = OutcomeSuccess
	}
	row.BeforeState = redactAuditState(row.BeforeState)
	row.AfterState = redactAuditState(row.AfterState)
	// The request context is already cancelled when the client disconnects, but
	// the audit row must still be persisted (§9.4 "no silent drop").
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()

	err := internal.RetryWithBackoff(writeCtx, nil, 2, func(ctx context.Context) error {
		return a.store.WriteAudit(ctx, []AuditRow{row})
	})
	if err == nil {
		return
	}
	auditWriteErrors.Inc()
	if internal.DeadLetterTotal != nil {
		internal.DeadLetterTotal.WithLabelValues("audit").Inc()
	}
	slog.Error("api-vehicle: audit write failed", "action", row.Action, "outcome", row.Outcome,
		"entity_type", row.EntityType, "entity_id", row.EntityID, "request_id", row.RequestID, "error", err)
	a.deadLetter(row, err)
}

// deadLetter publishes a failed audit row so an operator can replay it.
func (a *Auditor) deadLetter(row AuditRow, cause error) {
	if a.nats == nil {
		return
	}
	subject := a.nats.SubjectPlain("notify", "deadletter")
	body, merr := json.Marshal(map[string]any{
		"source":     "api-vehicle",
		"kind":       "audit_write_failure",
		"action":     row.Action,
		"outcome":    row.Outcome,
		"entity_id":  row.EntityID,
		"request_id": row.RequestID,
		"error":      cause.Error(),
	})
	if merr != nil {
		return
	}
	if perr := a.nats.Publish(subject, body); perr != nil {
		slog.Error("api-vehicle: audit dead-letter publish failed", "subject", subject, "error", perr)
	}
}

// sensitiveAuditKeys are never persisted in before/after state (§9.4).
var sensitiveAuditKeys = []string{"password", "token", "secret", "hmac", "authorization", "api_key", "apikey", "credential"}

// redactAuditState removes sensitive fields (recursively) from an audit payload.
func redactAuditState(state any) any {
	if state == nil {
		return nil
	}
	body, err := json.Marshal(state)
	if err != nil {
		return nil
	}
	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil
	}
	return redactValue(decoded)
}

// redactValue walks a decoded JSON value and drops sensitive keys.
func redactValue(v any) any {
	switch typed := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for k, val := range typed {
			if isSensitiveKey(k) {
				out[k] = "[REDACTED]"
				continue
			}
			out[k] = redactValue(val)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, val := range typed {
			out[i] = redactValue(val)
		}
		return out
	default:
		return v
	}
}

// isSensitiveKey reports whether a JSON key must be redacted.
func isSensitiveKey(key string) bool {
	lower := strings.ToLower(key)
	for _, needle := range sensitiveAuditKeys {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}
