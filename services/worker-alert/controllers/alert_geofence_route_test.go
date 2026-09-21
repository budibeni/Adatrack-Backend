package controllers

// alert_geofence_route_test.go — geofence state machine + entry/exit detection
// (PRD §5.9.1), route deviation with max-deviation refresh (§5.9.2) and the
// OFFLINE sweeper (§5.9.7) on the hermetic harness.

import (
	"context"
	"strconv"
	"testing"
	"time"

	"adatrack_gps/worker-alert/models"
)

func circleZone(id int64, vID int64, onEntry, onExit bool) models.Geofence {
	return models.Geofence{
		ID: id, Name: "yard", AreaType: "circle",
		CenterLat: -6.2, CenterLon: 106.8, RadiusM: 500,
		Severity: models.SeverityHigh, OnEntry: onEntry, OnExit: onExit,
		VehicleIDs: map[int64]bool{vID: true},
	}
}

func TestGeofenceStatePersistence(t *testing.T) {
	store := newFakeAlertStore()
	w, _, _ := newMiniredisWorker(t, store)
	ctx := context.Background()

	// Missing/corrupt state starts fresh.
	if got := w.loadGeofenceState(ctx, "DEV001", "it-imei-7"); len(got) != 0 {
		t.Errorf("missing state = %v, want empty", got)
	}
	_ = w.red.Set(ctx, w.geofenceStateKey("DEV001", "it-imei-7"), "{not json", time.Minute)
	if got := w.loadGeofenceState(ctx, "DEV001", "it-imei-7"); len(got) != 0 {
		t.Errorf("corrupt state = %v, want empty (fresh start)", got)
	}

	// Round trip.
	want := map[int64]bool{3: true, 4: false}
	w.saveGeofenceState(ctx, "DEV001", "it-imei-7", want)
	if got := w.loadGeofenceState(ctx, "DEV001", "it-imei-7"); len(got) != 2 || !got[3] || got[4] {
		t.Errorf("round trip = %v, want %v", got, want)
	}
}

func TestDetectGeofences(t *testing.T) {
	store := newFakeAlertStore()
	w, _, _ := newMiniredisWorker(t, store)
	ctx := context.Background()
	now := time.Now()

	// No zones → nothing.
	w.detectGeofences(ctx, telem(7), now)
	if len(store.alerts) != 0 {
		t.Fatalf("no zones must not raise: %+v", store.alerts)
	}

	// One circle zone, both transitions armed (bust the refresh cadence — the
	// first "no zones" lookup cached an empty list as fresh).
	store.geofences = []models.Geofence{circleZone(5, 7, true, true)}
	w.cache("DEV001").geofencedAt = time.Time{}

	// Inside → ENTRY alert.
	inside := telem(7) // -6.2,106.8 = centre
	w.detectGeofences(ctx, inside, now)
	if len(store.alerts) != 1 {
		t.Fatalf("entry must raise, drafts=%d", len(store.alerts))
	}
	a := store.alerts[0]
	if a.Type != models.AlertGeofenceBreach || a.DedupKey != "geofence:DEV001:7:5:entry" {
		t.Errorf("entry draft = %+v", a)
	}
	if a.Metadata["direction"] != "entry" || a.Metadata["geofence_id"] != int64(5) {
		t.Errorf("entry metadata = %v", a.Metadata)
	}

	// No transition → nothing new.
	w.detectGeofences(ctx, inside, now)
	if len(store.alerts) != 1 {
		t.Fatalf("staying inside must not re-raise: %+v", store.alerts)
	}

	// Outside → EXIT alert.
	store.alerts = nil
	_ = w.red.Del(ctx, dedupKeyPrefix+"DEV001:geofence:DEV001:7:5:exit")
	outside := telem(7)
	outside.Lat, outside.Lon = -6.3, 106.9 // ~14 km away
	w.detectGeofences(ctx, outside, now)
	if len(store.alerts) != 1 {
		t.Fatalf("exit must raise, drafts=%d", len(store.alerts))
	}
	if store.alerts[0].DedupKey != "geofence:DEV001:7:5:exit" {
		t.Errorf("exit draft = %+v", store.alerts[0])
	}

	// State was persisted (outside=false for zone 5).
	if state := w.loadGeofenceState(ctx, "DEV001", "it-imei-7"); state[5] {
		t.Errorf("persisted state = %v, want zone 5 false (outside)", state)
	}

	// A vehicle not mapped to the zone never evaluates it.
	store.alerts = nil
	other := telem(9) // inside the circle but not mapped
	other.Lat, other.Lon = -6.2, 106.8
	w.detectGeofences(ctx, other, now)
	if len(store.alerts) != 0 {
		t.Errorf("unmapped vehicle must be skipped: %+v", store.alerts)
	}

	// A disarmed direction (OnExit=false) transitions state silently.
	store.alerts = nil
	store.geofences = []models.Geofence{circleZone(5, 7, true, false)}
	_ = w.red.Del(ctx, w.geofenceStateKey("DEV001", "it-imei-7"))
	_ = w.red.Del(ctx, dedupKeyPrefix+"DEV001:geofence:DEV001:7:5:entry")
	w.detectGeofences(ctx, inside, now) // entry armed → raises + state true
	before := len(store.alerts)
	w.detectGeofences(ctx, outside, now) // exit NOT armed → no alert
	if len(store.alerts) != before {
		t.Errorf("disarmed exit must stay silent (before=%d after=%d)", before, len(store.alerts))
	}
	if state := w.loadGeofenceState(ctx, "DEV001", "it-imei-7"); state[5] {
		t.Error("the state must still flip even when the alert is disarmed")
	}

	// Polygon zones use the ray-casting test.
	store.alerts = nil
	_ = w.red.Del(ctx, w.geofenceStateKey("DEV001", "it-imei-7"))
	_ = w.red.Del(ctx, dedupKeyPrefix+"DEV001:geofence:DEV001:7:6:entry")
	poly := models.Geofence{
		ID: 6, Name: "poly", AreaType: "polygon",
		Boundary: [][2]float64{{-6.21, 106.79}, {-6.21, 106.81}, {-6.19, 106.81}, {-6.19, 106.79}},
		Severity: models.SeverityMedium, OnEntry: true, OnExit: true,
		VehicleIDs: map[int64]bool{7: true},
	}
	store.geofences = append(store.geofences, poly)
	w.cache("DEV001").geofencedAt = time.Time{} // reload with the polygon zone
	inPoly := telem(7)
	inPoly.Lat, inPoly.Lon = -6.2, 106.8
	w.detectGeofences(ctx, inPoly, now) // inside both zones → entries for 5 and 6
	found6 := false
	for _, d := range store.alerts {
		if d.DedupKey == "geofence:DEV001:7:6:entry" {
			found6 = true
		}
	}
	if !found6 {
		t.Errorf("polygon entry must raise, drafts=%v", store.alerts)
	}
}

func TestDetectRouteDeviation(t *testing.T) {
	store := newFakeAlertStore()
	w, _, _ := newMiniredisWorker(t, store)
	ctx := context.Background()
	now := time.Now()

	// Zero coordinates → skip entirely.
	blank := telem(7)
	blank.Lat, blank.Lon = 0, 0
	w.detectRouteDeviation(ctx, blank, now)
	if len(store.alerts) != 0 {
		t.Fatalf("blank fix must be skipped: %+v", store.alerts)
	}

	// An assignment far from every waypoint → deviation alert with metadata.
	store.assignments = []models.Assignment{{
		ID: 42, VehicleID: 7,
		Waypoints: []models.Waypoint{{Lat: -6.2, Lon: 106.8}, {Lat: -6.25, Lon: 106.85}},
	}}
	far := telem(7)
	far.Lat, far.Lon = -6.5, 107.2 // many km away
	w.detectRouteDeviation(ctx, far, now)
	if len(store.alerts) != 1 {
		t.Fatalf("deviation must raise, drafts=%d", len(store.alerts))
	}
	a := store.alerts[0]
	if a.Type != models.AlertRouteDeviation || a.Severity != models.SeverityHigh {
		t.Errorf("raised = (%s,%s), want (route_deviation, high)", a.Type, a.Severity)
	}
	if a.DedupKey != "route:DEV001:7:42" || a.Metadata["assignment_id"] != int64(42) {
		t.Errorf("draft = %+v", a)
	}
	if a.Metadata["deviation_meters"].(float64) <= 200 {
		t.Errorf("deviation = %v, want above the threshold", a.Metadata["deviation_meters"])
	}

	// A repeat while the open alert exists → the MAX deviation is refreshed
	// (UpdateRouteDeviation path; the fake's dedup guard makes every raise
	// return nil, so the update runs on both calls).
	store.alerts = nil
	farther := telem(7)
	farther.Lat, farther.Lon = -6.9, 107.6 // even farther
	w.detectRouteDeviation(ctx, farther, now)
	if len(store.deviations) != 2 {
		t.Fatalf("deviation updates = %v, want one per evaluation", store.deviations)
	}
	if store.deviations[1] <= store.deviations[0] {
		t.Errorf("the farther fix must report a larger deviation: %v", store.deviations)
	}

	// Another vehicle's assignment is ignored.
	store.alerts = nil
	store.deviations = nil
	otherVehicle := telem(9)
	otherVehicle.Lat, otherVehicle.Lon = -6.9, 107.6
	w.detectRouteDeviation(ctx, otherVehicle, now)
	if len(store.alerts) != 0 || len(store.deviations) != 0 {
		t.Errorf("other vehicle must be ignored: %+v / %v", store.alerts, store.deviations)
	}
}

func TestSweepOffline(t *testing.T) {
	store := newFakeAlertStore()
	w, _, _ := newMiniredisWorker(t, store)
	ctx := context.Background()
	now := time.Now()

	vehicles := []VehicleRef{{ID: 7, IMEI: "it-imei-7"}, {ID: 8, IMEI: "it-imei-8"}}

	// Fresh live state for both devices → no alert.
	freshState := `{"last_seen":` + strconv.Itoa(int(now.Unix())) + `}`
	for _, v := range vehicles {
		_ = w.red.Set(ctx, w.red.LiveStateKey("DEV001", v.IMEI), freshState, time.Minute)
	}
	w.sweepOffline(ctx, "DEV001", vehicles, now)
	if len(store.alerts) != 0 {
		t.Fatalf("fresh devices must not raise: %+v", store.alerts)
	}

	// One device goes stale (no live state at all) → exactly one OFFLINE alert.
	_ = w.red.Del(ctx, w.red.LiveStateKey("DEV001", "it-imei-8"))
	w.sweepOffline(ctx, "DEV001", vehicles, now)
	if len(store.alerts) != 1 || store.alerts[0].DedupKey != "offline:DEV001:8" {
		t.Fatalf("stale sweep = %+v, want only vehicle 8", store.alerts)
	}
	for _, d := range store.alerts {
		if d.Type != models.AlertOffline {
			t.Errorf("draft type = %s, want offline", d.Type)
		}
	}

	// Corrupt live state counts as stale.
	store.alerts = nil
	_ = w.red.Set(ctx, w.red.LiveStateKey("DEV001", "it-imei-7"), "not-json", time.Minute)
	w.sweepOffline(ctx, "DEV001", vehicles[:1], now)
	if len(store.alerts) != 1 {
		t.Errorf("corrupt state must count as stale: %+v", store.alerts)
	}
}

func TestStateFreshBranches(t *testing.T) {
	store := newFakeAlertStore()
	w, _, _ := newMiniredisWorker(t, store)
	ctx := context.Background()
	now := time.Now()
	after := 3 * time.Minute

	// Missing → stale.
	if w.stateFresh(ctx, "DEV001", "missing", after, now) {
		t.Error("missing state must be stale")
	}

	// Fresh timestamp → fresh.
	_ = w.red.Set(ctx, w.red.LiveStateKey("DEV001", "it-imei-7"), `{"last_seen":`+strconv.Itoa(int(now.Unix()))+`}`, time.Minute)
	if !w.stateFresh(ctx, "DEV001", "it-imei-7", after, now) {
		t.Error("a just-seen device must be fresh")
	}

	// Old timestamp → stale.
	_ = w.red.Set(ctx, w.red.LiveStateKey("DEV001", "it-imei-7"), `{"last_seen":`+strconv.Itoa(int(now.Add(-10*time.Minute).Unix()))+`}`, time.Minute)
	if w.stateFresh(ctx, "DEV001", "it-imei-7", after, now) {
		t.Error("a 10-minute-old device must be stale")
	}

	// Zero/invalid last_seen → stale.
	_ = w.red.Set(ctx, w.red.LiveStateKey("DEV001", "it-imei-7"), `{"last_seen":0}`, time.Minute)
	if w.stateFresh(ctx, "DEV001", "it-imei-7", after, now) {
		t.Error("last_seen=0 must be stale")
	}

	// Transport failure → fail-safe fresh (never raise on errors).
	_ = w.red.Close()
	if !w.stateFresh(ctx, "DEV001", "it-imei-7", after, now) {
		t.Error("a transport error must fail safe to fresh")
	}
}
