package controllers

// alert_worker_test.go — the worker plumbing on the hermetic harness: the
// per-company cache (fresh/stale/error paths), company tracking, the background
// loop drain and telemetry ingestion guards.

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"adatrack_gps/worker-alert/models"
)

func TestWorkerCache(t *testing.T) {
	store := newFakeAlertStore()
	w, _, cfg := newMiniredisWorker(t, store)
	cfg.Alert.GeoFenceRefresh = 30 * time.Second
	cfg.Alert.RouteDeviationRefresh = 30 * time.Second
	ctx := context.Background()

	store.geofences = []models.Geofence{circleZone(5, 7, true, true)}
	store.speeds = []models.SpeedConfig{{ID: 1, VehicleID: 7, MaxSpeed: 80, Enabled: true}}
	store.assignments = []models.Assignment{{ID: 42, VehicleID: 7, Waypoints: []models.Waypoint{{Lat: 1, Lon: 1}}}}
	store.vehicles = []VehicleRef{{ID: 7, IMEI: "it-imei-7"}}

	// First lookup loads; the second is served from the cache (the store slice
	// can be swapped without affecting the cached copy).
	first := w.cachedGeofences(ctx, "DEV001")
	if len(first) != 1 {
		t.Fatalf("geofence load = %d, want 1", len(first))
	}
	store.geofences = nil
	if got := w.cachedGeofences(ctx, "DEV001"); len(got) != 1 {
		t.Errorf("cached lookup = %d, want the cached 1", len(got))
	}
	if got := w.cachedSpeedConfigs(ctx, "DEV001"); len(got) != 1 {
		t.Errorf("speed load = %d, want 1", len(got))
	}
	if got := w.cachedAssignments(ctx, "DEV001"); len(got) != 1 {
		t.Errorf("assignment load = %d, want 1", len(got))
	}
	if got := w.cachedVehicles(ctx, "DEV001"); len(got) != 1 {
		t.Errorf("vehicle load = %d, want 1", len(got))
	}

	// After the cadence elapses the store is consulted again — including when
	// it now fails, in which case the STALE copy is served (availability over
	// freshness, §FR-4.4).
	w.cache("DEV001").geofencedAt = time.Time{}
	store.geofenceErr = context.DeadlineExceeded
	if got := w.cachedGeofences(ctx, "DEV001"); len(got) != 1 {
		t.Errorf("stale-serve = %d, want the previous copy", len(got))
	}
	w.cache("DEV001").speedsAt = time.Time{}
	store.speedsErr = context.DeadlineExceeded
	if got := w.cachedSpeedConfigs(ctx, "DEV001"); len(got) != 1 {
		t.Errorf("stale speeds = %d, want the previous copy", len(got))
	}
	w.cache("DEV001").assignedAt = time.Time{}
	store.assignmentsErr = context.DeadlineExceeded
	if got := w.cachedAssignments(ctx, "DEV001"); len(got) != 1 {
		t.Errorf("stale assignments = %d, want the previous copy", len(got))
	}
	w.cache("DEV001").vehiclesAt = time.Time{}
	store.vehiclesErr = context.DeadlineExceeded
	if got := w.cachedVehicles(ctx, "DEV001"); len(got) != 1 {
		t.Errorf("stale vehicles = %d, want the previous copy", len(got))
	}

	// A cold cache with a failing store serves an empty copy (len 0, never a
	// panic — callers branch on len).
	w2, _, _ := newMiniredisWorker(t, newFakeAlertStore())
	fs := w2.store.(*fakeAlertStore)
	fs.geofenceErr = context.DeadlineExceeded
	if got := w2.cachedGeofences(ctx, "DEV001"); len(got) != 0 {
		t.Errorf("a cold failing lookup = %v, want an empty copy", got)
	}
}

func TestCompanyTracking(t *testing.T) {
	store := newFakeAlertStore()
	w, _, _ := newMiniredisWorker(t, store)

	w.rememberCompany("DEV001")
	w.rememberCompany("LOADT2")
	got := w.companiesSnapshot()
	if len(got) != 2 {
		t.Errorf("companies = %v, want DEV001 + LOADT2", got)
	}
}

func TestWorkerStopDrains(t *testing.T) {
	store := newFakeAlertStore()
	w, _, _ := newMiniredisWorker(t, store)
	// Launch short-interval loops and stop them — Stop must return quickly.
	w.launch(func(context.Context) {}, 10*time.Millisecond)
	w.launch(func(context.Context) {}, 10*time.Millisecond)
	done := make(chan struct{})
	go func() { w.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(6 * time.Second):
		t.Fatal("Stop must drain the loops within its 5s budget")
	}
}

func TestHandleMessageGuards(t *testing.T) {
	store := newFakeAlertStore()
	w, _, _ := newMiniredisWorker(t, store)
	store.inserted = false

	// Malformed JSON is swallowed (counted at ingestion, not here).
	if err := w.handleMessage(natsMsg([]byte("{broken"))); err != nil {
		t.Errorf("malformed payload = %v, want nil", err)
	}
	// No tenant binding → ignored.
	if err := w.handleMessage(natsMsg([]byte(`{"imei":"x"}`))); err != nil {
		t.Errorf("unbound payload = %v, want nil", err)
	}
	if got := w.companiesSnapshot(); len(got) != 0 {
		t.Errorf("unbound messages must not register a company: %v", got)
	}

	// A valid message registers the company and runs every detector (none
	// trigger: no alarm, no zones, no configs, no fuel).
	payload := `{"imei":"it-imei-7","company_code":"DEV001","vehicle_id":7,` +
		`"lat":-6.2,"lon":106.8,"speed":30,"timestamp":` + strconv.FormatInt(time.Now().Unix(), 10) + `}`
	if err := w.handleMessage(natsMsg([]byte(payload))); err != nil {
		t.Fatalf("valid payload = %v", err)
	}
	found := false
	for _, c := range w.companiesSnapshot() {
		if c == "DEV001" {
			found = true
		}
	}
	if !found {
		t.Error("the message company must be remembered for the sweepers")
	}
	if len(store.alerts) != 0 {
		t.Errorf("quiet telemetry must not raise: %+v", store.alerts)
	}
}

// natsMsg builds a bare *nats.Msg for the handler tests.
func natsMsg(data []byte) *nats.Msg { return &nats.Msg{Data: data} }
