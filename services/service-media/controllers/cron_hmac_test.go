package controllers

import (
	"testing"
	"time"
)

// TestParseCron covers the documented schedules (default + dense dev sweep).
func TestParseCron(t *testing.T) {
	ok := []string{"0 3 * * *", "*/1 * * * *", "0,30 1-5 * * 1-5", "15 2 1 * *", "* * * * *"}
	for _, expr := range ok {
		if _, err := parseCron(expr); err != nil {
			t.Errorf("parseCron(%q) = %v, want nil", expr, err)
		}
	}
	bad := []string{"", "0 3 * *", "60 3 * * *", "0 24 * * *", "0 3 * * abc", "*/0 * * * *"}
	for _, expr := range bad {
		if _, err := parseCron(expr); err == nil {
			t.Errorf("parseCron(%q) = nil, want an error", expr)
		}
	}
}

// TestCronMatches verifies the matcher (03:00 daily is the FR-8.7 default).
func TestCronMatches(t *testing.T) {
	spec, err := parseCron("0 3 * * *")
	if err != nil {
		t.Fatalf("parseCron: %v", err)
	}
	if !spec.matches(time.Date(2026, 9, 22, 3, 0, 0, 0, time.UTC)) {
		t.Error("03:00 must match the daily schedule")
	}
	if spec.matches(time.Date(2026, 9, 22, 3, 1, 0, 0, time.UTC)) {
		t.Error("03:01 must not match the daily schedule")
	}
}

// TestRetentionSchedulerHonoursTheOverride documents the dev/E2E interval mode
// (MEDIA_RETENTION_SWEEP_SEC) and the once-per-minute cron guard.
func TestRetentionSchedulerHonoursTheOverride(t *testing.T) {
	sched, err := newRetentionScheduler(Settings{SweepOverrideSeconds: 2})
	if err != nil {
		t.Fatalf("interval scheduler: %v", err)
	}
	now := time.Now().UTC()
	if !sched.due(now) {
		t.Error("the first tick must be due (boot sweep)")
	}
	if sched.due(now.Add(time.Second)) {
		t.Error("a 2 s interval must not fire after 1 s")
	}
	if !sched.due(now.Add(3 * time.Second)) {
		t.Error("a 2 s interval must fire after 3 s")
	}

	cronSched, err := newRetentionScheduler(Settings{CleanupCron: "0 3 * * *"})
	if err != nil {
		t.Fatalf("cron scheduler: %v", err)
	}
	three := time.Date(2026, 9, 22, 3, 0, 0, 0, time.UTC)
	if !cronSched.due(three) {
		t.Error("03:00 must be due")
	}
	if cronSched.due(three.Add(20 * time.Second)) {
		t.Error("the same matching minute must not fire twice")
	}
}

// TestHMACSignAndVerify covers the FR-8.1 signature contract.
func TestHMACSignAndVerify(t *testing.T) {
	secret := "tenant-hmac-secret-123456"
	payload := []byte("payload-bytes")
	sig := SignHMAC(secret, payload)
	if !VerifyHMAC(secret, payload, sig) {
		t.Error("a freshly signed payload must verify")
	}
	if !VerifyHMAC(secret, payload, "sha256="+sig) {
		t.Error("the sha256=<hex> form must verify")
	}
	if VerifyHMAC(secret, []byte("tampered"), sig) {
		t.Error("a tampered payload must not verify")
	}
	if VerifyHMAC("other-secret-1234567890", payload, sig) {
		t.Error("a different secret must not verify")
	}
	if VerifyHMAC(secret, payload, "not-hex") || VerifyHMAC(secret, payload, "") {
		t.Error("malformed signatures must not verify")
	}
}

// TestTimestampFresh covers the anti-replay window (PRD 9.6).
func TestTimestampFresh(t *testing.T) {
	now := time.Now().UTC()
	if !TimestampFresh(now.Format(time.RFC3339), 5*time.Minute, now) {
		t.Error("a current timestamp must be fresh")
	}
	if TimestampFresh(now.Add(-10*time.Minute).Format(time.RFC3339), 5*time.Minute, now) {
		t.Error("a 10 minute old timestamp must be rejected")
	}
	if TimestampFresh("", 5*time.Minute, now) {
		t.Error("a missing timestamp must be rejected (fail-closed)")
	}
	if TimestampFresh("yesterday", 5*time.Minute, now) {
		t.Error("a malformed timestamp must be rejected")
	}
}
