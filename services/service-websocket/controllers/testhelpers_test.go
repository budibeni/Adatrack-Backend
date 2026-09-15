package controllers

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// errAuditDown simulates an unavailable audit sink (fail-closed tests).
var errAuditDown = errors.New("audit sink unavailable")

// errProvisionFailed simulates a failing tenant provisioning.
var errProvisionFailed = errors.New("provisioning failed")

// mustJSON serialises a value for assertions (fails the test on error).
func mustJSON(t *testing.T, v any) string {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(body)
}

// countAction counts audit rows with a given action.
func countAction(rows []AuditRow, action string) int {
	count := 0
	for _, row := range rows {
		if row.Action == action {
			count++
		}
	}
	return count
}

// countActionOutcome counts audit rows with a given action AND outcome.
func countActionOutcome(rows []AuditRow, action, outcome string) int {
	count := 0
	for _, row := range rows {
		if row.Action == action && row.Outcome == outcome {
			count++
		}
	}
	return count
}

// auditActionExists reports whether any row carries the action.
func auditActionExists(rows []AuditRow, action string) bool {
	return countAction(rows, action) > 0
}

// waitForAudit polls the audit sink until the predicate holds (the writer is
// asynchronous by design, PRD §9.4).
func waitForAudit(t *testing.T, h *testHarness, predicate func([]AuditRow) bool, description string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if predicate(h.store.auditsSnapshot()) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s (rows: %v)", description, h.store.auditsSnapshot())
}
