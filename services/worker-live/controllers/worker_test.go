package controllers

import (
	"testing"
	"time"

	"ajb_gps/internal"
	"ajb_gps/worker-live/models"
)

// testConfig builds a minimal config for the pure-logic tests.
func testConfig() *internal.Config {
	cfg := internal.LoadConfig()
	cfg.Live.IdleAfter = 90 * time.Second
	cfg.Live.OfflineAfterMinutes = 3
	cfg.Live.MaxBatch = 10
	return cfg
}

// TestCalculateStatusMatrix covers the FR-2.2 state machine.
func TestCalculateStatusMatrix(t *testing.T) {
	idle := 90 * time.Second
	offline := 3 * time.Minute

	cases := []struct {
		name  string
		speed float64
		age   time.Duration
		want  string
	}{
		{"moving device is ONLINE", 30, 0, models.StatusOnline},
		{"stationary but fresh is ONLINE", 0, 10 * time.Second, models.StatusOnline},
		{"stationary beyond the idle window is IDLE", 0, 2 * time.Minute, models.StatusIdle},
		{"stale beyond the offline window is OFFLINE", 0, 5 * time.Minute, models.StatusOffline},
		{"a moving device that stopped reporting is still OFFLINE", 40, 10 * time.Minute, models.StatusOffline},
	}
	for _, tc := range cases {
		if got := CalculateStatus(tc.speed, tc.age, idle, offline); got != tc.want {
			t.Errorf("%s: CalculateStatus(%v, %s) = %s, want %s",
				tc.name, tc.speed, tc.age, got, tc.want)
		}
	}
}

// TestShouldMarkOffline covers the sweeper decision (including idempotency).
func TestShouldMarkOffline(t *testing.T) {
	now := time.Now().Unix()
	offline := 3 * time.Minute

	fresh := models.LiveState{Status: models.StatusOnline, LastSeen: now - 10}
	if shouldMarkOffline(fresh, now, offline) {
		t.Error("a fresh state must not be marked OFFLINE")
	}

	boundary := models.LiveState{Status: models.StatusOnline, LastSeen: now - int64(offline.Seconds())}
	if shouldMarkOffline(boundary, now, offline) {
		t.Error("a state exactly at the boundary must not be flagged (must exceed it)")
	}

	stale := models.LiveState{Status: models.StatusIdle, LastSeen: now - int64(offline.Seconds()) - 1}
	if !shouldMarkOffline(stale, now, offline) {
		t.Error("a state beyond the offline window must be flagged")
	}

	already := models.LiveState{Status: models.StatusOffline, LastSeen: now - 3600}
	if shouldMarkOffline(already, now, offline) {
		t.Error("an already-OFFLINE state must not be re-flagged")
	}
}

// TestIsFuelOnly distinguishes partial fuel readings from position frames.
func TestIsFuelOnly(t *testing.T) {
	level := 42.0
	fuelOnly := models.TelemetryMessage{FuelLevel: &level}
	if !isFuelOnly(fuelOnly) {
		t.Error("a fuel reading without position must be treated as fuel-only")
	}

	withPosition := models.TelemetryMessage{FuelLevel: &level, Lat: -6.2, Lon: 106.8}
	if isFuelOnly(withPosition) {
		t.Error("a message carrying a position is not fuel-only")
	}

	positionOnly := models.TelemetryMessage{Lat: -6.2, Lon: 106.8, Speed: 12}
	if isFuelOnly(positionOnly) {
		t.Error("a position message without fuel is not fuel-only")
	}
}

// TestBuildStateFromTelemetry verifies the live-state projection (FR-2.1),
// including ACC, satellites, altitude, GSM and the online status.
func TestBuildStateFromTelemetry(t *testing.T) {
	w := New(testConfig(), nil, nil)

	msg := models.TelemetryMessage{
		IMEI: "864201040512345", CompanyCode: "DEV001", VehicleID: 1,
		Lat: -6.2088, Lon: 106.8456, Speed: 42.5, Heading: 90,
		Satellites: 9, Altitude: 120, Battery: 13, GsmSignal: 4,
		ACC: true, Mileage: 1000, Fix: true, Timestamp: 1767000000,
	}
	state := w.buildState("adatrack_gps:dev001:vehicle:state:864201040512345", msg)

	if state.Lat != msg.Lat || state.Lon != msg.Lon || state.Speed != msg.Speed {
		t.Errorf("position/speed not projected: %+v", state)
	}
	if state.ACC == nil || !*state.ACC {
		t.Error("ACC was not projected into the live state")
	}
	if state.Satellites != 9 || state.Altitude != 120 || state.GsmSignal != 4 || state.Battery != 13 {
		t.Errorf("telemetry detail fields lost: %+v", state)
	}
	if state.Status != models.StatusOnline {
		t.Errorf("status = %s, want ONLINE for a moving device", state.Status)
	}
	if state.LastSeen == 0 {
		t.Error("LastSeen must be stamped by the worker (offline detection)")
	}
	if state.Timestamp != 1767000000 {
		t.Errorf("device timestamp = %d, want 1767000000", state.Timestamp)
	}
}

// TestBuildStateStationaryIsIdle documents that a parked device is IDLE, not
// ONLINE, once it exceeds the idle window (the sweeper handles OFFLINE).
func TestBuildStateStationaryIsIdle(t *testing.T) {
	w := New(testConfig(), nil, nil)
	msg := models.TelemetryMessage{IMEI: "86001", CompanyCode: "DEV001", Lat: -6.2, Lon: 106.8}
	state := w.buildState("key", msg)
	if state.Status != models.StatusOnline {
		t.Errorf("status = %s, want ONLINE for a fresh inbound message", state.Status)
	}

	// A message that arrives with an old reference age becomes IDLE.
	if got := CalculateStatus(0, 2*time.Minute, w.idleAfter(), w.offlineAfter()); got != models.StatusIdle {
		t.Errorf("status = %s, want IDLE for a stationary device past the idle window", got)
	}
}

// TestLiveStateKeyIsTenantScoped verifies Redis key isolation between tenants
// (FR-2.1: adatrack_gps:{company}:vehicle:state:{IMEI}).
func TestLiveStateKeyIsTenantScoped(t *testing.T) {
	red := &internal.RedisClient{}
	// RedisClient zero value has no config: the helper must fall back to the
	// documented prefix instead of panicking.
	key := red.LiveStateKey("DEV001", "864201040512345")
	want := "adatrack_gps:dev001:vehicle:state:864201040512345"
	if key != want {
		t.Errorf("LiveStateKey = %q, want %q", key, want)
	}
	if other := red.LiveStateKey("ACME", "864201040512345"); other == key {
		t.Error("different tenants must produce different live-state keys")
	}
}
