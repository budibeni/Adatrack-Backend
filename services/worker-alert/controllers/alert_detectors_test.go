package controllers

// alert_detectors_test.go — the per-detector contract on the hermetic harness
// (miniredis + fake store). Triggers are verified up to the RaiseAlert boundary
// (the captured InsertAlert draft); the live publish happens in the IT suite.

import (
	"context"
	"testing"
	"time"

	"adatrack_gps/worker-alert/models"
)

func telem(vID int64) models.TelemetryMessage {
	return models.TelemetryMessage{
		IMEI: "it-imei-7", CompanyCode: "DEV001", VehicleID: vID,
		Lat: -6.2, Lon: 106.8, Speed: 40, Timestamp: time.Now().Unix(),
	}
}

// ---------------------------------------------------------------------------
// overspeed (PRD §5.9.4)
// ---------------------------------------------------------------------------

func TestDetectOverspeed(t *testing.T) {
	store := newFakeAlertStore()
	w, _, _ := newMiniredisWorker(t, store)
	ctx := context.Background()

	// No config → nothing.
	w.detectOverspeed(ctx, telem(7), time.Now())
	if len(store.alerts) != 0 {
		t.Fatalf("no config must not raise: %+v", store.alerts)
	}

	// Config present but the speed is inside limit+grace → nothing.
	store.speeds = []models.SpeedConfig{{ID: 1, VehicleID: 7, MaxSpeed: 100, GracePct: 20, Enabled: true, Severity: models.SeverityHigh}}
	w.cache("DEV001").speedsAt = time.Time{} // bust the refresh cadence
	msg := telem(7)
	msg.Speed = 119 // below 100*1.2
	w.detectOverspeed(ctx, msg, time.Now())
	if len(store.alerts) != 0 {
		t.Fatalf("in-grace speed must not raise: %+v", store.alerts)
	}

	// Zero speed never raises.
	msg.Speed = 0
	w.detectOverspeed(ctx, msg, time.Now())
	if len(store.alerts) != 0 {
		t.Fatalf("zero speed must not raise: %+v", store.alerts)
	}

	// Breach → OVERSPEEDING with the configured severity.
	msg.Speed = 130
	w.detectOverspeed(ctx, msg, time.Now())
	if len(store.alerts) != 1 {
		t.Fatalf("breach must raise, drafts=%d", len(store.alerts))
	}
	a := store.alerts[0]
	if a.Type != models.AlertOverspeeding || a.Severity != models.SeverityHigh {
		t.Errorf("raised = (%s,%s), want (overspeeding, high)", a.Type, a.Severity)
	}
	if a.DedupKey != "speed:DEV001:7:1" || a.VehicleID != 7 || a.Speed != 130 {
		t.Errorf("draft = %+v", a)
	}
	if a.Metadata["effective_kmh"] != 120.0 || a.Metadata["vehicle_scoped"] != true {
		t.Errorf("metadata = %v", a.Metadata)
	}

	// >1.5× the limit escalates to critical; a global config applies to any
	// vehicle.
	store.alerts = nil
	store.speeds = []models.SpeedConfig{{ID: 2, VehicleID: 0, MaxSpeed: 80, Enabled: true}}
	w.cache("DEV001").speedsAt = time.Time{}
	msg2 := telem(9)
	msg2.Speed = 125 // > 1.5*80
	w.detectOverspeed(ctx, msg2, time.Now())
	if len(store.alerts) != 1 {
		t.Fatalf("global-config breach must raise, drafts=%d", len(store.alerts))
	}
	a2 := store.alerts[0]
	if a2.Severity != models.SeverityCritical {
		t.Errorf("severity = %s, want critical beyond 1.5×", a2.Severity)
	}
	if a2.DedupKey != "speed:DEV001:9:2" {
		t.Errorf("dedup key = %s, want the global config id", a2.DedupKey)
	}

	// An empty configured severity defaults to medium (above limit, below 1.5×).
	store.alerts = nil
	_ = w.red.Del(ctx, dedupKeyPrefix+"DEV001:speed:DEV001:9:2")
	msg3 := telem(9)
	msg3.Speed = 100 // > 80 limit, ≤ 1.5×80
	w.detectOverspeed(ctx, msg3, time.Now())
	if len(store.alerts) != 1 || store.alerts[0].Severity != models.SeverityMedium {
		t.Errorf("empty severity = %+v, want medium", store.alerts)
	}
}

// ---------------------------------------------------------------------------
// battery (PRD §5.9.6)
// ---------------------------------------------------------------------------

func TestDetectBatteryLow(t *testing.T) {
	store := newFakeAlertStore()
	w, _, cfg := newMiniredisWorker(t, store)
	ctx := context.Background()

	notReported := telem(7)
	w.detectBatteryLow(ctx, notReported, time.Now()) // battery 0 = not reported

	healthy := telem(7)
	healthy.Battery = 90
	w.detectBatteryLow(ctx, healthy, time.Now())

	low := telem(7)
	low.Battery = 15 // below 20, above half → low
	w.detectBatteryLow(ctx, low, time.Now())

	if len(store.alerts) != 1 {
		t.Fatalf("drafts = %d, want only the low battery", len(store.alerts))
	}
	a := store.alerts[0]
	if a.Type != models.AlertBatteryLow || a.Severity != models.SeverityLow {
		t.Errorf("raised = (%s,%s), want (battery_low, low)", a.Type, a.Severity)
	}
	if a.DedupKey != "battery:DEV001:7" {
		t.Errorf("dedup key = %s", a.DedupKey)
	}

	// At/below half the threshold → medium (clear the engine dedup key first —
	// same vehicle identity).
	store.alerts = nil
	_ = w.red.Del(ctx, dedupKeyPrefix+"DEV001:battery:DEV001:7")
	empty := telem(7)
	empty.Battery = 5
	w.detectBatteryLow(ctx, empty, time.Now())
	if len(store.alerts) != 1 || store.alerts[0].Severity != models.SeverityMedium {
		t.Errorf("critical battery = %+v, want medium severity", store.alerts)
	}

	// A misconfigured threshold (<=0) falls back to 20.
	cfg.Alert.BatteryLowPercent = 0
	store.alerts = nil
	healthy.Battery = 25 // ≥ default 20 → silent
	w.detectBatteryLow(ctx, healthy, time.Now())
	if len(store.alerts) != 0 {
		t.Errorf("default threshold must not raise at 25%%: %+v", store.alerts)
	}
}

// ---------------------------------------------------------------------------
// SOS (PRD §5.9.5)
// ---------------------------------------------------------------------------

func TestDetectSOS(t *testing.T) {
	store := newFakeAlertStore()
	w, _, cfg := newMiniredisWorker(t, store)
	ctx := context.Background()

	// No alarm marker → nothing.
	w.detectSOS(ctx, telem(7), time.Now())
	if len(store.alerts) != 0 {
		t.Fatalf("plain telemetry must not raise SOS: %+v", store.alerts)
	}

	// GT06 alarm frame → CRITICAL SOS with the source metadata.
	msg := telem(7)
	msg.AlarmCode = 0x26
	w.detectSOS(ctx, msg, time.Now())
	if len(store.alerts) != 1 {
		t.Fatalf("alarm frame must raise SOS, drafts=%d", len(store.alerts))
	}
	a := store.alerts[0]
	if a.Type != models.AlertSOS || a.Severity != models.SeverityCritical {
		t.Errorf("raised = (%s,%s), want (sos, critical)", a.Type, a.Severity)
	}
	if a.Metadata["alarm_source"] != "gt06_0x26" {
		t.Errorf("alarm source = %v", a.Metadata["alarm_source"])
	}

	// LBS alarm → the 0x19 marker (both the SOS cooldown key AND the engine
	// dedup key from the first trigger must be cleared — same device identity).
	store.alerts = nil
	_ = w.red.Del(ctx, sosDedupKeyPrefix+"DEV001:it-imei-7")
	_ = w.red.Del(ctx, dedupKeyPrefix+"DEV001:sos:DEV001:7:it-imei-7")
	lbs := telem(7)
	lbs.AlarmLBS = true
	w.detectSOS(ctx, lbs, time.Now())
	if len(store.alerts) != 1 || store.alerts[0].Metadata["alarm_source"] != "gt06_0x19_lbs" {
		t.Errorf("LBS alarm = %+v", store.alerts)
	}

	// The cooldown suppresses the immediate repeat (Redis fast path).
	store.alerts = nil
	_ = w.red.Del(ctx, sosDedupKeyPrefix+"DEV001:it-imei-7")
	_ = w.red.Del(ctx, dedupKeyPrefix+"DEV001:sos:DEV001:7:it-imei-7")
	cfg.Alert.SOSCooldownSeconds = 300
	w.detectSOS(ctx, telem(7), time.Now()) // primes the cooldown key
	before := len(store.alerts)
	w.detectSOS(ctx, telem(7), time.Now()) // inside the cooldown
	if len(store.alerts) != before {
		t.Errorf("cooldown must suppress the repeat (before=%d after=%d)", before, len(store.alerts))
	}
}
