package consumer

import (
	"testing"
	"time"
)

func TestWorker_Deduplication(t *testing.T) {
	w := NewWorker()

	company := "TESTCO"
	vehicleID := 42
	alertType := "OVERSPEEDING"
	window := 100 * time.Millisecond

	// First call -> not duplicate
	if w.isDuplicate(company, vehicleID, alertType, window) {
		t.Errorf("expected first call to NOT be duplicate")
	}

	// Immediate second call -> must be duplicate
	if !w.isDuplicate(company, vehicleID, alertType, window) {
		t.Errorf("expected immediate second call to be marked as duplicate")
	}

	// Different alert type -> not duplicate
	if w.isDuplicate(company, vehicleID, "SOS", window) {
		t.Errorf("expected different alert type to NOT be duplicate")
	}

	// Different vehicle -> not duplicate
	if w.isDuplicate(company, 43, alertType, window) {
		t.Errorf("expected different vehicle to NOT be duplicate")
	}

	// Wait for window to expire
	time.Sleep(120 * time.Millisecond)
	if w.isDuplicate(company, vehicleID, alertType, window) {
		t.Errorf("expected call after window expiry to NOT be duplicate")
	}
}

func TestWorker_SeverityRanking(t *testing.T) {
	if severityRank("critical") <= severityRank("high") {
		t.Errorf("critical should have higher rank than high")
	}
	if severityRank("high") <= severityRank("medium") {
		t.Errorf("high should have higher rank than medium")
	}
	if severityRank("medium") <= severityRank("low") {
		t.Errorf("medium should have higher rank than low")
	}
	if severityRank("info") != severityRank("low") {
		t.Errorf("info should have equal rank to low")
	}
}

func TestWorker_OverspeedMath(t *testing.T) {
	cfg := SpeedConfig{
		MaxSpeed:    80.0,
		GraceMargin: 10.0, // 10% -> 88.0 km/h
		Severity:    "high",
	}

	effectiveLimit := cfg.MaxSpeed * (1.0 + cfg.GraceMargin/100.0)
	if effectiveLimit != 88.0 {
		t.Fatalf("expected effective limit 88.0, got %f", effectiveLimit)
	}

	// Speed 85 km/h -> not overspeeding (within grace margin)
	if 85.0 > effectiveLimit {
		t.Errorf("85 km/h should not exceed effective limit of 88 km/h")
	}

	// Speed 95 km/h -> overspeeding high
	if 95.0 <= effectiveLimit {
		t.Errorf("95 km/h should exceed effective limit")
	}
	if 95.0 >= cfg.MaxSpeed*1.5 {
		t.Errorf("95 km/h should not be critical (< 120 km/h)")
	}

	// Speed 125 km/h -> critical tier (> 1.5x limit = 120 km/h)
	if 125.0 < cfg.MaxSpeed*1.5 {
		t.Errorf("125 km/h must be critical tier")
	}
}
