package controllers

// b8_driver_maintenance_test.go — B8 driver behaviour + maintenance reminders.
//
// The suites are hermetic: the fake store records the rows the detectors would
// write, so the score formula, the speeding-episode measurement and the
// maintenance thresholds are all asserted without PostgreSQL or NATS.

import (
	"context"
	"testing"
	"time"

	"adatrack_gps/worker-alert/models"
)

// newDriverWorker builds a worker with the fields the driver/maintenance paths
// touch. It reuses the miniredis harness (so the dedup fast path and the engine
// are real) and keeps the NATS client nil — the fake store never reports an
// inserted alert, which is what stops the engine before publish/notify.
func newDriverWorker(t *testing.T, store Store) *Worker {
	t.Helper()
	fake, ok := store.(*fakeAlertStore)
	if !ok {
		t.Fatalf("newDriverWorker expects the fake store, got %T", store)
	}
	w, _, _ := newMiniredisWorker(t, fake)
	w.driverMinSpeeding = 10
	return w
}

// TestScoreFromDistance covers the distance-normalised score (migration 025): the
// same event count must score differently for 10 km and for 400 km.
func TestScoreFromDistance(t *testing.T) {
	counts := models.DriverEventCounts{HarshAcceleration: 1, HarshBraking: 1} // 20 points

	// Below the minimum distance → count-based fallback, rate stays nil so the
	// caller records `events_per_100km = NULL`.
	score, grade, rate := scoreFromDistance(counts, 2.0, 5)
	if rate != nil {
		t.Fatalf("short day returned a rate %v, want nil (count fallback)", *rate)
	}
	if score != 80 || grade != "B" {
		t.Fatalf("short day = %v/%s, want 80/B (count formula)", score, grade)
	}

	// 20 points in 400 km = 5 points/100 km → A/100.
	score, grade, rate = scoreFromDistance(counts, 400, 5)
	if rate == nil || !approxFloat(*rate, 5, 1e-9) {
		t.Fatalf("400 km rate = %v, want 5", rate)
	}
	if score != 100 || grade != "A" {
		t.Fatalf("400 km = %v/%s, want 100/A", score, grade)
	}

	// 20 points in 40 km = 50 points/100 km → beyond the last bucket: linear drop.
	score, grade, _ = scoreFromDistance(counts, 40, 5)
	if score != 60 || grade != "D" {
		t.Fatalf("40 km = %v/%s, want 60/D", score, grade)
	}

	// An extremely bad rate floors at 0 rather than going negative.
	bad := models.DriverEventCounts{HarshAcceleration: 40}
	score, grade, _ = scoreFromDistance(bad, 10, 5)
	if score != 0 || grade != "E" {
		t.Fatalf("extreme day = %v/%s, want 0/E", score, grade)
	}

	// Without distance information the documented count formula still applies.
	score, grade, rate = scoreFromDistance(counts, 0, 5)
	if rate != nil || score != 80 || grade != "B" {
		t.Fatalf("no-distance day = %v/%s/%v, want 80/B/nil", score, grade, rate)
	}
}

// TestRefreshDriverScoreUsesDistance proves the worker passes the trip distance of
// the day into the score row (the B7.2 normaliser, migration 025).
func TestRefreshDriverScoreUsesDistance(t *testing.T) {
	store := newFakeAlertStore()
	store.counts = models.DriverEventCounts{HarshBraking: 2} // 20 points
	store.distanceKM = 200                                   // → 10 points/100 km → B/90
	w := newDriverWorker(t, store)
	w.driverMinDistanceKM = 5

	ev := &models.DriverEvent{CompanyCode: "DEV001", VehicleID: 7,
		EventType: models.DriverHarshBraking, Severity: models.SeverityHigh,
		Timestamp: time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)}
	w.refreshDriverScore(context.Background(), ev)

	if len(store.scores) != 1 {
		t.Fatalf("scores written = %d, want 1", len(store.scores))
	}
	sc := store.scores[0]
	if !approxFloat(sc.DistanceKM, 200, 1e-9) {
		t.Fatalf("distance_km = %v, want 200", sc.DistanceKM)
	}
	if sc.EventsPer100KM == nil || !approxFloat(*sc.EventsPer100KM, 10, 1e-9) {
		t.Fatalf("events_per_100km = %v, want 10", sc.EventsPer100KM)
	}
	if sc.Score != 85 || sc.Grade != "B" {
		t.Fatalf("score = %v/%s, want 85/B (rate mode)", sc.Score, sc.Grade)
	}
	if sc.ScoreByCounts == nil || *sc.ScoreByCounts != 80 {
		t.Fatalf("score_by_counts = %v, want 80 (audit of the count formula)", sc.ScoreByCounts)
	}
}

// approxFloat compares two floats with a tolerance.
func approxFloat(got, want, tol float64) bool {
	d := got - want
	return d <= tol && d >= -tol
}

func TestScoreFromCounts(t *testing.T) {
	cases := []struct {
		name      string
		counts    models.DriverEventCounts
		wantScore float64
		wantGrade string
	}{
		{"clean day", models.DriverEventCounts{}, 100, "A"},
		{"one harsh braking", models.DriverEventCounts{HarshBraking: 1}, 90, "A"},
		{"two harsh pulses", models.DriverEventCounts{HarshAcceleration: 1, HarshBraking: 1}, 80, "B"},
		{"cornering + speeding", models.DriverEventCounts{HarshCornering: 2, Speeding: 2}, 80, "B"},
		{"heavy day", models.DriverEventCounts{HarshAcceleration: 3, HarshBraking: 3, HarshCornering: 3}, 25, "E"},
		{"floor at zero", models.DriverEventCounts{HarshBraking: 20}, 0, "E"},
	}
	for _, tc := range cases {
		score, grade := scoreFromCounts(tc.counts)
		if score != tc.wantScore || grade != tc.wantGrade {
			t.Errorf("%s: score/grade = %v/%s, want %v/%s",
				tc.name, score, grade, tc.wantScore, tc.wantGrade)
		}
	}
}

func TestDetectDriverRecordsDevicePulses(t *testing.T) {
	store := newFakeAlertStore()
	w := newDriverWorker(t, store)
	at := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)

	w.detectDriver(context.Background(), models.TelemetryMessage{
		IMEI: "123456789012345", CompanyCode: "ACME", VehicleID: 7,
		Lat: -6.2, Lon: 106.8, Speed: 55, HarshBraking: true,
		Timestamp: at.Unix(),
	}, at)

	if len(store.driverEvents) != 1 {
		t.Fatalf("driver events = %d, want 1", len(store.driverEvents))
	}
	ev := store.driverEvents[0]
	if ev.EventType != models.DriverHarshBraking || ev.Source != "device_alarm" {
		t.Fatalf("event = %s/%s, want harsh_braking/device_alarm", ev.EventType, ev.Source)
	}
	if ev.RawCode != 0x30 {
		t.Fatalf("raw alarm code = 0x%02x, want 0x30", ev.RawCode)
	}
	if !ev.Timestamp.Equal(at) {
		t.Fatalf("event timestamp = %s, want %s (device time)", ev.Timestamp, at)
	}
	if len(store.scores) != 1 || store.scores[0].Score != 90 || store.scores[0].Grade != "A" {
		t.Fatalf("score rows = %+v, want one row with score 90 grade A", store.scores)
	}

	// A frame WITHOUT the device flags must not invent an event.
	w.detectDriver(context.Background(), models.TelemetryMessage{
		IMEI: "123456789012345", CompanyCode: "ACME", VehicleID: 7, Speed: 55,
		Timestamp: at.Add(time.Second).Unix(),
	}, at.Add(time.Second))
	if len(store.driverEvents) != 1 {
		t.Fatalf("a frame without flags created %d extra event(s)", len(store.driverEvents)-1)
	}
}

func TestSpeedingEpisodeIsMeasuredAndClosedOnce(t *testing.T) {
	store := newFakeAlertStore()
	store.speeds = []models.SpeedConfig{{ID: 1, VehicleID: 0, MaxSpeed: 60, Enabled: true, GracePct: 5}}
	w := newDriverWorker(t, store)
	start := time.Date(2026, 9, 24, 11, 0, 0, 0, time.UTC)

	// Frame 1: above the limit → the episode opens, nothing is written yet.
	w.detectDriver(context.Background(), models.TelemetryMessage{
		IMEI: "1", CompanyCode: "ACME", VehicleID: 3, Speed: 80, Timestamp: start.Unix(),
	}, start)
	if len(store.driverEvents) != 0 {
		t.Fatal("an open episode must not be written before it closes")
	}

	// Frame 2: still above the limit → still one open episode, nothing written.
	w.detectDriver(context.Background(), models.TelemetryMessage{
		IMEI: "1", CompanyCode: "ACME", VehicleID: 3, Speed: 90,
		Timestamp: start.Add(20 * time.Second).Unix(),
	}, start.Add(20*time.Second))
	if len(store.driverEvents) != 0 {
		t.Fatal("a continuing episode must not be written twice")
	}

	// Frame 3: back below the limit → the episode closes with a measured duration.
	w.detectDriver(context.Background(), models.TelemetryMessage{
		IMEI: "1", CompanyCode: "ACME", VehicleID: 3, Speed: 40,
		Timestamp: start.Add(45 * time.Second).Unix(),
	}, start.Add(45*time.Second))
	if len(store.driverEvents) != 1 {
		t.Fatalf("closed episodes = %d, want 1", len(store.driverEvents))
	}
	ev := store.driverEvents[0]
	if ev.EventType != models.DriverSpeeding || ev.Source != "derived" {
		t.Fatalf("event = %s/%s, want speeding/derived", ev.EventType, ev.Source)
	}
	if ev.DurationSeconds != 45 {
		t.Fatalf("speeding duration = %d s, want 45 (device time)", ev.DurationSeconds)
	}
	if ev.SpeedLimitKMH != 60 {
		t.Fatalf("speed limit = %v, want 60 (without the grace margin)", ev.SpeedLimitKMH)
	}
}

func TestSpeedingEpisodeShorterThanThresholdIsDropped(t *testing.T) {
	store := newFakeAlertStore()
	store.speeds = []models.SpeedConfig{{ID: 1, MaxSpeed: 60, Enabled: true}}
	w := newDriverWorker(t, store)
	start := time.Date(2026, 9, 24, 11, 0, 0, 0, time.UTC)

	w.detectDriver(context.Background(), models.TelemetryMessage{
		IMEI: "1", CompanyCode: "ACME", VehicleID: 3, Speed: 80, Timestamp: start.Unix(),
	}, start)
	w.detectDriver(context.Background(), models.TelemetryMessage{
		IMEI: "1", CompanyCode: "ACME", VehicleID: 3, Speed: 40,
		Timestamp: start.Add(3 * time.Second).Unix(),
	}, start.Add(3*time.Second))

	if len(store.driverEvents) != 0 {
		t.Fatalf("a 3 s blip produced %d event(s), want 0", len(store.driverEvents))
	}
}

func TestMaintenanceDueThresholds(t *testing.T) {
	km := 10000.0
	hours := 250.0
	days := 180
	base := 9000.0
	baseHours := 200.0
	lastService := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	sc := &models.MaintenanceSchedule{
		ID: 1, VehicleID: 5, Name: "Oil change", MaintenanceType: "oil_change",
		IntervalKM: &km, IntervalEngineHours: &hours, IntervalDays: &days,
		LastServiceAt: &lastService, LastServiceOdometerKM: &base, LastServiceEngineHrs: &baseHours,
		ReminderKMBefore: 500, ReminderDaysBefore: 7,
	}
	day := func(y int, m time.Month, d int) time.Time {
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	}

	// Below every threshold → no reminder.
	if reason, due := maintenanceDue(sc,
		models.VehicleUsage{VehicleID: 5, OdometerKM: 9000, EngineHours: 200}, day(2026, 2, 1)); due {
		t.Fatalf("premature reminder: %s", reason)
	}

	// Odometer inside the pre-emptive margin: due at 9 000 + 10 000 = 19 000 km,
	// so the reminder fires from 18 500 km (margin 500).
	if _, due := maintenanceDue(sc,
		models.VehicleUsage{VehicleID: 5, OdometerKM: 18500, EngineHours: 200}, day(2026, 2, 1)); !due {
		t.Fatal("odometer threshold did not fire at the reminder margin")
	}

	// Engine hours: due at 200 + 250 = 450 h, margin min(500, 25) = 25 h → 425 h.
	if _, due := maintenanceDue(sc,
		models.VehicleUsage{VehicleID: 5, OdometerKM: 9000, EngineHours: 425}, day(2026, 2, 1)); !due {
		t.Fatal("engine-hours threshold did not fire")
	}

	// Calendar threshold (180 days − 7 day margin → 2026-06-23).
	if _, due := maintenanceDue(sc,
		models.VehicleUsage{VehicleID: 5, OdometerKM: 9000, EngineHours: 200}, day(2026, 7, 1)); !due {
		t.Fatal("calendar threshold did not fire")
	}
}

func TestSweepMaintenanceRaisesReminderAndStampsCooldown(t *testing.T) {
	km := 10000.0
	base := 9000.0
	store := newFakeAlertStore()
	store.schedules = []models.MaintenanceSchedule{{
		ID: 42, VehicleID: 5, Name: "Oil change", MaintenanceType: "oil_change",
		IntervalKM: &km, LastServiceOdometerKM: &base, ReminderKMBefore: 500,
	}}
	store.usage = map[int64]models.VehicleUsage{
		5: {VehicleID: 5, IMEI: "123456789012345", OdometerKM: 19000, EngineHours: 200},
	}
	w := newDriverWorker(t, store)
	w.cfg.Driver.MaintenanceCooldown = 24 * time.Hour

	now := time.Date(2026, 9, 24, 8, 0, 0, 0, time.UTC)
	if err := w.sweepMaintenance(context.Background(), "ACME", now); err != nil {
		t.Fatalf("sweepMaintenance: %v", err)
	}
	if len(store.reminders) != 1 || store.reminders[0] != 42 {
		t.Fatalf("reminders = %v, want [42]", store.reminders)
	}

	// Inside the cooldown the schedule stays quiet (no reminder storm).
	store.schedules[0].LastReminderAt = &now
	if err := w.sweepMaintenance(context.Background(), "ACME", now.Add(time.Hour)); err != nil {
		t.Fatalf("sweepMaintenance (cooldown): %v", err)
	}
	if len(store.reminders) != 1 {
		t.Fatalf("cooldown ignored: reminders = %v", store.reminders)
	}
}
