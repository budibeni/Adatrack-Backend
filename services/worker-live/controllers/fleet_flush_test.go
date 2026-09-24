package controllers

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"adatrack_gps/worker-live/models"
)

// fakeFleetStore records the persisted fleet data and can inject failures so the
// retry/re-buffer contract of the flusher is testable without PostgreSQL.
type fakeFleetStore struct {
	mu sync.Mutex

	metering map[fleetKey]meteringDelta
	trips    []TripRecord
	stops    map[int64][]StopRecord

	tripInserts int
	nextTripID  int64

	failMetering bool
	failTrip     bool
	failStops    bool
}

func newFakeFleetStore() *fakeFleetStore {
	return &fakeFleetStore{
		metering:   map[fleetKey]meteringDelta{},
		stops:      map[int64][]StopRecord{},
		nextTripID: 100,
	}
}

// AddMetering implements FleetStore.
func (f *fakeFleetStore) AddMetering(_ context.Context, company string, vehicleID int64, odo, hours float64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failMetering {
		return context.DeadlineExceeded
	}
	key := fleetKey{CompanyCode: company, VehicleID: vehicleID}
	cur := f.metering[key]
	f.metering[key] = meteringDelta{OdometerKM: cur.OdometerKM + odo, EngineHours: cur.EngineHours + hours}
	return nil
}

// InsertTrip implements FleetStore.
func (f *fakeFleetStore) InsertTrip(_ context.Context, _ string, trip TripRecord) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failTrip {
		return 0, context.DeadlineExceeded
	}
	f.trips = append(f.trips, trip)
	f.tripInserts++
	f.nextTripID++
	return f.nextTripID, nil
}

// InsertStops implements FleetStore.
func (f *fakeFleetStore) InsertStops(_ context.Context, _ string, tripID int64, stops []StopRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failStops {
		return context.DeadlineExceeded
	}
	f.stops[tripID] = append(f.stops[tripID], stops...)
	return nil
}

// Readiness implements FleetStore.
func (f *fakeFleetStore) Readiness(context.Context) error { return nil }

// snapshot returns a copy of the persisted state for assertions.
func (f *fakeFleetStore) snapshot() (map[fleetKey]meteringDelta, []TripRecord, map[int64][]StopRecord, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	metering := make(map[fleetKey]meteringDelta, len(f.metering))
	for k, v := range f.metering {
		metering[k] = v
	}
	trips := append([]TripRecord{}, f.trips...)
	stops := make(map[int64][]StopRecord, len(f.stops))
	for k, v := range f.stops {
		stops[k] = append([]StopRecord{}, v...)
	}
	return metering, trips, stops, f.tripInserts
}

// driveOneTrip feeds one full trip (drive → stop ≥60 s → resume) so a trip with
// a stop is closed in the accumulator.
func driveOneTrip(w *Worker, base time.Time, acc *bool) {
	obs := func(ts time.Time, lat, speed float64) {
		w.Fleet().Observe(models.TelemetryMessage{
			IMEI: "864201040512345", CompanyCode: "DEV001", VehicleID: 1,
			Lat: lat, Lon: 106.8000, Speed: speed, ACC: acc, Fix: true, Timestamp: ts.Unix(),
		}, base)
	}
	obs(base, -6.2000, 40)
	obs(base.Add(20*time.Second), -6.2010, 40)
	obs(base.Add(30*time.Second), -6.2010, 0)
	obs(base.Add(160*time.Second), -6.2010, 0)
	obs(base.Add(170*time.Second), -6.2020, 35)
}

// TestFlushFleetPersistsMeteringAndTrips covers the FR-2.5/FR-2.6 flush path:
// the buffered odometer/engine-hour deltas and the closed trip (+ its stop) end
// up in the store, and the PRD metrics move.
func TestFlushFleetPersistsMeteringAndTrips(t *testing.T) {
	store := newFakeFleetStore()
	w := New(testConfig(), nil, nil, store)
	base := time.Now().UTC()
	driveOneTrip(w, base, models.BoolPtr(true))

	odometerBefore := testutil.ToFloat64(odometerUpdates)
	engineBefore := testutil.ToFloat64(engineHoursUpdates)
	w.flushFleet()

	metering, trips, stops, _ := store.snapshot()
	key := fleetKey{CompanyCode: "DEV001", VehicleID: 1}
	delta, ok := metering[key]
	if !ok {
		t.Fatalf("metering not persisted: %+v", metering)
	}
	if delta.OdometerKM <= 0 {
		t.Errorf("odometer delta = %.4f, want > 0", delta.OdometerKM)
	}
	if delta.EngineHours <= 0 {
		t.Errorf("engine hours delta = %.5f, want > 0 (ACC was ON)", delta.EngineHours)
	}
	if len(trips) != 1 {
		t.Fatalf("trips = %d, want 1", len(trips))
	}
	if trips[0].VehicleID != 1 || trips[0].IMEI != "864201040512345" {
		t.Errorf("trip identity = %+v", trips[0])
	}
	if trips[0].StopCount != 1 {
		t.Errorf("trip stop_count = %d, want 1", trips[0].StopCount)
	}
	if len(stops[101]) != 1 {
		t.Fatalf("stops stored for trip 101 = %d, want 1", len(stops[101]))
	}
	if got := stops[101][0].DurationSeconds; got != 130 {
		t.Errorf("stop duration = %d s, want 130", got)
	}
	if after := testutil.ToFloat64(odometerUpdates); after <= odometerBefore {
		t.Errorf("odometer_updates_total = %v, want > %v", after, odometerBefore)
	}
	if after := testutil.ToFloat64(engineHoursUpdates); after <= engineBefore {
		t.Errorf("engine_hours_updates_total = %v, want > %v", after, engineBefore)
	}
	if w.Fleet().Pending() != 0 {
		t.Errorf("Pending = %d after a successful flush, want 0", w.Fleet().Pending())
	}
}

// TestFlushFleetRetriesOnFailure asserts nothing is lost when the store is down:
// the deltas/trips are re-buffered and persisted by the next flush.
func TestFlushFleetRetriesOnFailure(t *testing.T) {
	store := newFakeFleetStore()
	store.failMetering = true
	store.failTrip = true
	w := New(testConfig(), nil, nil, store)
	driveOneTrip(w, time.Now().UTC(), nil)

	w.flushFleet()
	if got := w.Fleet().Pending(); got == 0 {
		t.Fatal("a failed flush must re-buffer the pending work")
	}
	if _, trips, _, _ := store.snapshot(); len(trips) != 0 {
		t.Fatalf("trips = %d, want 0 while the store is failing", len(trips))
	}

	store.failMetering = false
	store.failTrip = false
	w.flushFleet()

	metering, trips, stops, _ := store.snapshot()
	if len(metering) != 1 || len(trips) != 1 {
		t.Fatalf("after recovery: %d metering / %d trips, want 1/1", len(metering), len(trips))
	}
	if len(stops[101]) != 1 {
		t.Fatalf("after recovery: stops = %d, want 1", len(stops[101]))
	}
	if w.Fleet().Pending() != 0 {
		t.Errorf("Pending = %d after recovery, want 0", w.Fleet().Pending())
	}
}

// TestFlushFleetRetriesOnlyStops keeps the trip row unique when the stop insert
// is the failing stage: the retry must INSERT the stops only.
func TestFlushFleetRetriesOnlyStops(t *testing.T) {
	store := newFakeFleetStore()
	store.failStops = true
	w := New(testConfig(), nil, nil, store)
	driveOneTrip(w, time.Now().UTC(), nil)

	w.flushFleet()
	_, trips, stops, inserts := store.snapshot()
	if len(trips) != 1 || inserts != 1 {
		t.Fatalf("trip inserts = %d (%d rows), want exactly 1 row after the stop failure", inserts, len(trips))
	}
	if len(stops[101]) != 0 {
		t.Fatalf("stops = %d before the retry, want 0", len(stops[101]))
	}

	store.failStops = false
	w.flushFleet()
	_, trips, stops, inserts = store.snapshot()
	if inserts != 1 {
		t.Errorf("trip inserts = %d after the stop retry, want 1 (no duplicate trip)", inserts)
	}
	if len(trips) != 1 {
		t.Errorf("trips = %d after the stop retry, want 1", len(trips))
	}
	if len(stops[101]) != 1 {
		t.Errorf("stops = %d after the retry, want 1", len(stops[101]))
	}
	if w.Fleet().Pending() != 0 {
		t.Errorf("Pending = %d after the stop retry, want 0", w.Fleet().Pending())
	}
}

// TestFlushFleetWithoutStoreKeepsBuffered documents the degraded mode: with no
// persistence wired, the accumulators stay in memory instead of vanishing.
func TestFlushFleetWithoutStoreKeepsBuffered(t *testing.T) {
	w := New(testConfig(), nil, nil, nil)
	base := time.Now().UTC()
	w.Fleet().Observe(models.TelemetryMessage{
		IMEI: "864201040512345", CompanyCode: "DEV001", VehicleID: 1,
		Lat: -6.2000, Lon: 106.8000, Speed: 40, Fix: true, Timestamp: base.Unix(),
	}, base)
	w.Fleet().Observe(models.TelemetryMessage{
		IMEI: "864201040512345", CompanyCode: "DEV001", VehicleID: 1,
		Lat: -6.2010, Lon: 106.8000, Speed: 40, Fix: true,
		Timestamp: base.Add(20 * time.Second).Unix(),
	}, base)

	w.flushFleet()
	if w.Fleet().Pending() == 0 {
		t.Fatal("without a store the buffered deltas must be kept for a later flush")
	}
}
