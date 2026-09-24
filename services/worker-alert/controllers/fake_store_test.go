package controllers

// fake_store_test.go — configurable Store fake backing the hermetic suites.
// Every method is backed by plain fields (inputs → canned outputs) and records
// the drafts it received so the detectors can be asserted end-to-end without
// PostgreSQL or NATS (publish only runs when InsertAlert reports success — the
// fakes keep inserted=false to stay NATS-free; the live path is covered by the
// ADATRACK_IT suite).

import (
	"context"
	"time"

	"adatrack_gps/worker-alert/models"
)

type fakeAlertStore struct {
	readinessErr error

	codes    []string
	codesErr error

	users    []models.Recipient
	usersErr error

	admins    []int64
	adminsErr error

	grants    []int64
	grantsErr error

	prefs    []models.PrefRow
	prefsErr error

	// inserted=false simulates the DB open-alert dedup guard.
	inserted  bool
	insertErr error
	alerts    []*models.Alert

	updateDeviationErr error
	deviations         []float64

	resolvedN    int64
	resolveErr   error
	resolveCalls []string

	openSOS    []models.Alert
	openSOSErr error

	escalateErr error
	escalations []int64

	notifErr  error
	notifRows []models.NotificationRow

	geofences   []models.Geofence
	geofenceErr error

	speeds    []models.SpeedConfig
	speedsErr error

	assignments    []models.Assignment
	assignmentsErr error

	vehicles    []VehicleRef
	vehiclesErr error

	fuelCfgs  []models.FuelConfig
	fuelErr   error
	upserts   []*models.FuelConfig
	upsertErr error

	// --- B8 driver behaviour + maintenance -----------------------------------
	driverEvents   []*models.DriverEvent
	driverEventErr error
	// counts is the canned daily aggregate returned by DailyDriverEventCounts
	// (defaults to counting the events collected in this fake).
	counts    models.DriverEventCounts
	countsErr error
	scores    []*models.DriverScore
	scoreErr  error

	schedules   []models.MaintenanceSchedule
	scheduleErr error
	usage       map[int64]models.VehicleUsage
	usageErr    error
	reminders   []int64
	reminderErr error
	touchedAt   []time.Time
}

func newFakeAlertStore() *fakeAlertStore { return &fakeAlertStore{} }

func (f *fakeAlertStore) Readiness(context.Context) error { return f.readinessErr }

func (f *fakeAlertStore) CompanyCodes(context.Context) ([]string, error) {
	return f.codes, f.codesErr
}

func (f *fakeAlertStore) Users(context.Context, []int64) ([]models.Recipient, error) {
	return f.users, f.usersErr
}

func (f *fakeAlertStore) TenantAdminUserIDs(context.Context, string) ([]int64, error) {
	return f.admins, f.adminsErr
}

func (f *fakeAlertStore) VehicleGrants(context.Context, string, int64) ([]int64, error) {
	return f.grants, f.grantsErr
}

func (f *fakeAlertStore) Preferences(context.Context, string, []int64) ([]models.PrefRow, error) {
	return f.prefs, f.prefsErr
}

func (f *fakeAlertStore) InsertAlert(_ context.Context, _ string, a *models.Alert) (bool, error) {
	f.alerts = append(f.alerts, a)
	if f.insertErr != nil {
		return false, f.insertErr
	}
	return f.inserted, nil
}

func (f *fakeAlertStore) UpdateRouteDeviation(_ context.Context, _, _ string, meters float64) error {
	f.deviations = append(f.deviations, meters)
	return f.updateDeviationErr
}

func (f *fakeAlertStore) ResolveOpenAlerts(_ context.Context, _, dedupKey string) (int64, error) {
	f.resolveCalls = append(f.resolveCalls, dedupKey)
	return f.resolvedN, f.resolveErr
}

func (f *fakeAlertStore) OpenSOAlerts(context.Context, string, time.Time, int) ([]models.Alert, error) {
	return f.openSOS, f.openSOSErr
}

func (f *fakeAlertStore) EscalateAlert(_ context.Context, _ string, id int64, _ int) error {
	f.escalations = append(f.escalations, id)
	return f.escalateErr
}

func (f *fakeAlertStore) InsertNotifications(_ context.Context, _ string, rows []models.NotificationRow) error {
	if f.notifErr != nil {
		return f.notifErr
	}
	f.notifRows = append(f.notifRows, rows...)
	return nil
}

func (f *fakeAlertStore) LoadGeofences(context.Context, string) ([]models.Geofence, error) {
	return f.geofences, f.geofenceErr
}

func (f *fakeAlertStore) LoadSpeedConfigs(context.Context, string) ([]models.SpeedConfig, error) {
	return f.speeds, f.speedsErr
}

func (f *fakeAlertStore) LoadAssignments(context.Context, string) ([]models.Assignment, error) {
	return f.assignments, f.assignmentsErr
}

func (f *fakeAlertStore) ActiveVehicles(context.Context, string) ([]VehicleRef, error) {
	return f.vehicles, f.vehiclesErr
}

func (f *fakeAlertStore) FuelConfigs(context.Context, string) ([]models.FuelConfig, error) {
	return f.fuelCfgs, f.fuelErr
}

func (f *fakeAlertStore) UpsertFuelConfig(_ context.Context, _ string, cfg *models.FuelConfig, _ int64) error {
	if f.upsertErr != nil {
		return f.upsertErr
	}
	f.upserts = append(f.upserts, cfg)
	return nil
}

// --- B8 driver behaviour + maintenance ---------------------------------------

func (f *fakeAlertStore) InsertDriverEvent(_ context.Context, _ string, ev *models.DriverEvent) error {
	if f.driverEventErr != nil {
		return f.driverEventErr
	}
	f.driverEvents = append(f.driverEvents, ev)
	return nil
}

// DailyDriverEventCounts returns the canned aggregate; when it was not set the
// counts are derived from the events collected by this fake, so a test can assert
// the score end-to-end without duplicating the aggregation.
func (f *fakeAlertStore) DailyDriverEventCounts(_ context.Context, _ string, vehicleID int64, _ time.Time) (models.DriverEventCounts, error) {
	if f.countsErr != nil {
		return models.DriverEventCounts{}, f.countsErr
	}
	if f.counts.Total() > 0 || f.counts.SpeedingSeconds > 0 {
		return f.counts, nil
	}
	var c models.DriverEventCounts
	for _, ev := range f.driverEvents {
		if ev.VehicleID != vehicleID {
			continue
		}
		switch ev.EventType {
		case models.DriverHarshAcceleration:
			c.HarshAcceleration++
		case models.DriverHarshBraking:
			c.HarshBraking++
		case models.DriverHarshCornering:
			c.HarshCornering++
		case models.DriverSpeeding:
			c.Speeding++
			c.SpeedingSeconds += ev.DurationSeconds
		}
	}
	return c, nil
}

func (f *fakeAlertStore) UpsertDriverScore(_ context.Context, _ string, sc *models.DriverScore) error {
	if f.scoreErr != nil {
		return f.scoreErr
	}
	f.scores = append(f.scores, sc)
	return nil
}

func (f *fakeAlertStore) MaintenanceSchedules(context.Context, string) ([]models.MaintenanceSchedule, error) {
	return f.schedules, f.scheduleErr
}

func (f *fakeAlertStore) VehicleUsage(context.Context, string) (map[int64]models.VehicleUsage, error) {
	return f.usage, f.usageErr
}

func (f *fakeAlertStore) TouchMaintenanceReminder(_ context.Context, _ string, scheduleID int64, at time.Time) error {
	if f.reminderErr != nil {
		return f.reminderErr
	}
	f.reminders = append(f.reminders, scheduleID)
	f.touchedAt = append(f.touchedAt, at)
	return nil
}
