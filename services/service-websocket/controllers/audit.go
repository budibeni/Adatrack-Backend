package controllers

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"

	"ajb_gps/internal"
)

// Audit actions (PRD §9.4 catalogue — the API only ever writes these values).
const (
	ActionLoginSuccess         = "LOGIN_SUCCESS"
	ActionLoginFailure         = "LOGIN_FAILURE"
	ActionLogout               = "LOGOUT"
	ActionTokenRefresh         = "TOKEN_REFRESH"
	ActionTokenRevoked         = "TOKEN_REVOKED"
	ActionAccessDenied         = "ACCESS_DENIED"
	ActionUserCreated          = "USER_CREATED"
	ActionCompanyCreated       = "COMPANY_CREATED"
	ActionTenantProvisioned    = "TENANT_PROVISIONED"
	ActionAdminUserAutocreated = "ADMIN_USER_AUTOCREATED"
	ActionSoftDeletedViewed    = "SOFT_DELETED_VIEWED"
)

// Audit outcomes (PRD §9.4).
const (
	OutcomeSuccess = "success"
	OutcomeFailure = "failure"
	OutcomeDenied  = "denied"
)

// AuditRow is one append-only `tm_audit_logs` row (PRD §9.4 schema).
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

// Auditor buffers audit rows and writes them in batches so an audit write never
// adds request latency, while a FAILED write is retried and finally dead-lettered
// (no silent drop, PRD §9.4). Sensitive actions use RecordSync and are therefore
// fail-closed.
type Auditor struct {
	store Store
	nats  *internal.NATSClient
	cfg   Settings

	mu      sync.Mutex
	pending []AuditRow
	flushCh chan struct{}

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewAuditor builds the audit writer.
func NewAuditor(store Store, nats *internal.NATSClient, cfg Settings) *Auditor {
	ctx, cancel := context.WithCancel(context.Background())
	return &Auditor{
		store:   store,
		nats:    nats,
		cfg:     cfg,
		flushCh: make(chan struct{}, 1),
		ctx:     ctx,
		cancel:  cancel,
	}
}

// Start launches the batch flusher.
func (a *Auditor) Start() {
	if !a.enabled() {
		slog.Warn("audit disabled (AUDIT_ENABLED=false) — sensitive actions will not be recorded")
		return
	}
	a.wg.Add(1)
	go a.loop()
}

// Stop drains pending rows (graceful shutdown) and waits for the flusher.
func (a *Auditor) Stop() {
	if !a.enabled() {
		return
	}
	a.cancel()
	done := make(chan struct{})
	go func() {
		a.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		slog.Warn("service-websocket: timed out draining the audit buffer")
	}
	a.flush(context.Background())
}

// enabled reports whether audit persistence is active.
func (a *Auditor) enabled() bool { return a.cfg.AuditEnabled }

// Record buffers a row asynchronously (non-critical actions). When the bounded
// buffer is full the flush is forced synchronously instead of dropping the row
// (PRD §9.4 "overflow → flush sinkron (bukan drop)").
func (a *Auditor) Record(row AuditRow) {
	if !a.enabled() {
		return
	}
	a.mu.Lock()
	a.pending = append(a.pending, row)
	full := len(a.pending) >= a.maxBatch()
	a.mu.Unlock()

	auditEvents.WithLabelValues(row.Action, row.Outcome).Inc()
	if full {
		a.flush(a.ctx)
		return
	}
	select {
	case a.flushCh <- struct{}{}:
	default:
	}
}

// RecordSync writes a row immediately and returns an error when it could not be
// persisted — sensitive actions (login, provisioning, access denial) are
// FAIL-CLOSED: the caller must reject the request (PRD §9.4).
func (a *Auditor) RecordSync(ctx context.Context, row AuditRow) error {
	if !a.enabled() {
		return nil
	}
	auditEvents.WithLabelValues(row.Action, row.Outcome).Inc()
	return a.persist(ctx, []AuditRow{row})
}

// maxBatch bounds one INSERT.
func (a *Auditor) maxBatch() int {
	if a.cfg.AuditBatchSize > 0 {
		return a.cfg.AuditBatchSize
	}
	return 100
}

// loop flushes on the configured cadence until cancelled.
func (a *Auditor) loop() {
	defer a.wg.Done()
	interval := a.cfg.AuditFlushEvery
	if interval <= 0 {
		interval = time.Second
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-a.ctx.Done():
			a.flush(context.WithoutCancel(a.ctx))
			return
		case <-a.flushCh:
			a.flush(a.ctx)
		case <-tick.C:
			a.flush(a.ctx)
		}
	}
}

// flush drains the buffer and persists it.
func (a *Auditor) flush(ctx context.Context) {
	a.mu.Lock()
	if len(a.pending) == 0 {
		a.mu.Unlock()
		return
	}
	batch := a.pending
	a.pending = nil
	a.mu.Unlock()

	if err := a.persist(ctx, batch); err != nil {
		slog.Error("service-websocket: audit flush failed", "rows", len(batch), "error", err)
	}
}

// persist writes rows with retry + backoff; an exhausted retry is dead-lettered
// to NATS and counted (never silently dropped — PRD §9.4).
func (a *Auditor) persist(ctx context.Context, rows []AuditRow) error {
	if len(rows) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	err := internal.RetryWithBackoff(ctx, nil, 2, func(ctx context.Context) error {
		return a.store.WriteAudit(ctx, rows)
	})
	if err == nil {
		return nil
	}

	auditWriteErrors.Inc()
	if internal.DeadLetterTotal != nil {
		internal.DeadLetterTotal.WithLabelValues("audit").Add(float64(len(rows)))
	}
	a.deadLetter(rows, err)
	return err
}

// deadLetter publishes failed audit rows to `notify.deadletter` so an operator
// can replay them (PRD §9.4).
func (a *Auditor) deadLetter(rows []AuditRow, cause error) {
	if a.nats == nil {
		return
	}
	subject := a.nats.SubjectPlain("notify", "deadletter")
	for _, row := range rows {
		body, merr := json.Marshal(map[string]any{
			"source":     "service-websocket",
			"kind":       "audit_write_failure",
			"action":     row.Action,
			"outcome":    row.Outcome,
			"request_id": row.RequestID,
			"error":      cause.Error(),
		})
		if merr != nil {
			continue
		}
		if perr := a.nats.Publish(subject, body); perr != nil {
			slog.Error("service-websocket: audit dead-letter publish failed",
				"subject", subject, "error", perr)
		}
	}
}

// sensitiveAuditKeys are never persisted in before/after state (PRD §9.4:
// password/token/secret/HMAC are redacted).
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
