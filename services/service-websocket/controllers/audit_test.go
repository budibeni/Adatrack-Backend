package controllers

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestAuditRecordBuffersAndFlushes asserts the async path persists rows without
// blocking the caller (PRD §9.4 "worker async").
func TestAuditRecordBuffersAndFlushes(t *testing.T) {
	store := newFakeStore()
	auditor := NewAuditor(store, nil, Settings{AuditEnabled: true, AuditBatchSize: 100, AuditFlushEvery: 10 * time.Millisecond})
	auditor.Start()
	defer auditor.Stop()

	auditor.Record(AuditRow{Action: ActionSoftDeletedViewed, Outcome: OutcomeSuccess, EntityType: "vehicle", EntityID: "1"})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(store.auditsSnapshot()) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	rows := store.auditsSnapshot()
	if len(rows) != 1 || rows[0].Action != ActionSoftDeletedViewed {
		t.Fatalf("audit rows = %v, want the buffered row", rows)
	}
}

// TestAuditOverflowFlushesSynchronously asserts the buffer never drops a row:
// reaching the batch size forces an immediate write.
func TestAuditOverflowFlushesSynchronously(t *testing.T) {
	store := newFakeStore()
	// A long flush interval proves the write came from the synchronous overflow
	// path, not from the ticker.
	auditor := NewAuditor(store, nil, Settings{AuditEnabled: true, AuditBatchSize: 1, AuditFlushEvery: time.Hour})
	auditor.Start()
	defer auditor.Stop()

	auditor.Record(AuditRow{Action: ActionAccessDenied, Outcome: OutcomeDenied})
	if len(store.auditsSnapshot()) != 1 {
		t.Fatalf("overflow flush did not persist synchronously: %v", store.auditsSnapshot())
	}
}

// TestAuditRecordSyncFailClosed asserts a sensitive action fails when the audit
// sink is down (PRD §9.4 "aksi sensitif gagal-audit → fail-closed").
func TestAuditRecordSyncFailClosed(t *testing.T) {
	store := newFakeStore()
	store.auditErr = errAuditDown
	auditor := NewAuditor(store, nil, Settings{AuditEnabled: true, AuditBatchSize: 10})
	auditor.Start()
	defer auditor.Stop()

	if err := auditor.RecordSync(context.Background(), AuditRow{Action: ActionLoginSuccess}); err == nil {
		t.Fatalf("RecordSync returned nil while the audit sink was down")
	}
}

// TestAuditStopDrainsBuffer asserts graceful shutdown never loses a row.
func TestAuditStopDrainsBuffer(t *testing.T) {
	store := newFakeStore()
	auditor := NewAuditor(store, nil, Settings{AuditEnabled: true, AuditBatchSize: 500, AuditFlushEvery: time.Hour})
	auditor.Start()
	auditor.Record(AuditRow{Action: ActionUserCreated, Outcome: OutcomeSuccess})
	auditor.Stop()

	if len(store.auditsSnapshot()) != 1 {
		t.Fatalf("buffer was not drained on Stop: %v", store.auditsSnapshot())
	}
}

// TestAuditDisabledWritesNothing asserts AUDIT_ENABLED=false is honoured.
func TestAuditDisabledWritesNothing(t *testing.T) {
	store := newFakeStore()
	auditor := NewAuditor(store, nil, Settings{AuditEnabled: false, AuditBatchSize: 1})
	auditor.Start()
	auditor.Record(AuditRow{Action: ActionLoginSuccess, Outcome: OutcomeSuccess})
	if err := auditor.RecordSync(context.Background(), AuditRow{Action: ActionLoginSuccess}); err != nil {
		t.Fatalf("disabled auditor returned an error: %v", err)
	}
	auditor.Stop()
	if len(store.auditsSnapshot()) != 0 {
		t.Fatalf("disabled auditor wrote rows: %v", store.auditsSnapshot())
	}
}

// TestAuditRedaction asserts sensitive fields never reach the audit table
// (PRD §9.4: password/token/secret/HMAC are redacted).
func TestAuditRedaction(t *testing.T) {
	state := map[string]any{
		"email":    "admin@dev001.io",
		"password": "Admin@123",
		"nested": map[string]any{
			"refresh_token": "abc",
			"hmac_secret":   "shh",
			"safe":          "value",
		},
		"list": []any{
			map[string]any{"api_key": "k", "name": "n"},
		},
	}
	redacted, ok := redactAuditState(state).(map[string]any)
	if !ok {
		t.Fatalf("redactAuditState returned %T", redactAuditState(state))
	}
	if redacted["password"] != "[REDACTED]" {
		t.Fatalf("password not redacted: %v", redacted["password"])
	}
	if redacted["email"] != "admin@dev001.io" {
		t.Fatalf("non-sensitive field was altered: %v", redacted["email"])
	}
	nested := redacted["nested"].(map[string]any)
	if nested["refresh_token"] != "[REDACTED]" || nested["hmac_secret"] != "[REDACTED]" {
		t.Fatalf("nested secrets not redacted: %v", nested)
	}
	if nested["safe"] != "value" {
		t.Fatalf("nested safe field altered: %v", nested)
	}
	list := redacted["list"].([]any)
	if list[0].(map[string]any)["api_key"] != "[REDACTED]" {
		t.Fatalf("list entry secret not redacted: %v", list)
	}
	// A JSON rendering of the state must not contain the secret material.
	if body := mustJSON(t, redacted); strings.Contains(body, "Admin@123") || strings.Contains(body, "shh") {
		t.Fatalf("redacted state still contains a secret: %s", body)
	}
}

// TestAuditSensitiveKeyDetection asserts the key matcher.
func TestAuditSensitiveKeyDetection(t *testing.T) {
	sensitive := []string{"password", "PasswordHash", "refresh_token", "HMAC_SECRET", "api_key", "authorization"}
	for _, key := range sensitive {
		if !isSensitiveKey(key) {
			t.Fatalf("isSensitiveKey(%q) = false, want true", key)
		}
	}
	for _, key := range []string{"email", "full_name", "vehicle_id", "action"} {
		if isSensitiveKey(key) {
			t.Fatalf("isSensitiveKey(%q) = true, want false", key)
		}
	}
}

// TestAuditRedactNil asserts a nil payload stays nil (no spurious "null" state).
func TestAuditRedactNil(t *testing.T) {
	if got := redactAuditState(nil); got != nil {
		t.Fatalf("redactAuditState(nil) = %v, want nil", got)
	}
}
