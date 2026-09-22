package controllers

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"adatrack_gps/internal"
)

// Audit actions (PRD §9.4 catalogue).
const (
	ActionMediaUploaded    = "ENTITY_CREATED"
	ActionMediaURL         = "MEDIA_URL_ACCESS"
	ActionMediaSoftDeleted = "ENTITY_SOFT_DELETED"
	ActionMediaRestored    = "ENTITY_RESTORED"
	ActionHardDelete       = "HARD_DELETE"
	ActionAccessDenied     = "ACCESS_DENIED"
	ActionMediaCompleted   = "ENTITY_UPDATED"
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

// Auditor persists audit rows with retry + dead-letter (no silent drop, §9.4).
type Auditor struct {
	store   Store
	nats    *internal.NATSClient
	enabled bool
}

// NewAuditor builds the audit writer.
func NewAuditor(store Store, nats *internal.NATSClient, enabled bool) *Auditor {
	return &Auditor{store: store, nats: nats, enabled: enabled}
}

// Enabled reports whether audit persistence is active.
func (a *Auditor) Enabled() bool { return a != nil && a.enabled }

// Write persists one row (retry + backoff) and dead-letters a definitive
// failure. Callers that must be fail-closed (MEDIA_URL_ACCESS, §9.4) propagate
// the returned error and refuse to serve the request.
func (a *Auditor) Write(ctx context.Context, row AuditRow) error {
	if !a.Enabled() {
		return nil
	}
	if row.Outcome == "" {
		row.Outcome = OutcomeSuccess
	}
	row.BeforeState = redactAuditState(row.BeforeState)
	row.AfterState = redactAuditState(row.AfterState)

	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	err := internal.RetryWithBackoff(writeCtx, nil, 2, func(ctx context.Context) error {
		return a.store.WriteAudit(ctx, []AuditRow{row})
	})
	if err == nil {
		return nil
	}

	auditWriteErrors.Inc()
	if internal.DeadLetterTotal != nil {
		internal.DeadLetterTotal.WithLabelValues("audit").Inc()
	}
	slog.Error("service-media: audit write failed", "action", row.Action,
		"entity_id", row.EntityID, "request_id", row.RequestID, "error", err)
	a.deadLetter(row, err)
	return err
}

// deadLetter publishes a failed audit row so an operator can replay it.
func (a *Auditor) deadLetter(row AuditRow, cause error) {
	if a.nats == nil {
		return
	}
	subject := a.nats.SubjectPlain("notify", "deadletter")
	body, merr := json.Marshal(map[string]any{
		"source":     "service-media",
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
		slog.Error("service-media: audit dead-letter publish failed", "subject", subject, "error", perr)
	}
}

// sensitiveAuditKeys are never persisted in before/after state (§9.4).
var sensitiveAuditKeys = []string{"password", "token", "secret", "hmac", "authorization", "api_key", "credential"}

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
