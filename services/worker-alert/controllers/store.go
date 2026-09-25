package controllers

import (
	"context"
	"time"

	"adatrack_gps/worker-alert/models"
)

// Store is the persistence surface of worker-alert. Every statement is
// parameterized and company-scoped (schema-per-tenant, PRD §6.2). A fake
// implementation backs the unit tests; PostgresStore implements it on top of
// the shared tenant manager.
type Store interface {
	// Readiness verifies the master pool (healthz).
	Readiness(ctx context.Context) error

	// CompanyCodes lists active tenant codes (master) so the sweepers can cover
	// companies that have not sent telemetry since the last boot.
	CompanyCodes(ctx context.Context) ([]string, error)

	// Users loads master tm_users rows for the given ids (notification targets).
	Users(ctx context.Context, ids []int64) ([]models.Recipient, error)

	// TenantAdminUserIDs returns company users whose effective role is Admin or
	// Manager (tm_user_company_access in the company schema).
	TenantAdminUserIDs(ctx context.Context, company string) ([]int64, error)

	// VehicleGrants returns users with a row-level grant on the vehicle
	// (tm_user_vehicles in the company schema).
	VehicleGrants(ctx context.Context, company string, vehicleID int64) ([]int64, error)

	// Preferences loads tm_notification_preferences rows for the given users.
	Preferences(ctx context.Context, company string, userIDs []int64) ([]models.PrefRow, error)

	// InsertAlert persists one alert with ON CONFLICT (dedup_key) WHERE
	// status='open' DO NOTHING. Returns false when the dedup guard suppressed it.
	InsertAlert(ctx context.Context, company string, a *models.Alert) (bool, error)

	// UpdateRouteDeviation refreshes the max deviation on the OPEN alert
	// identified by the dedup key (PRD §5.9.2: deviation_meters ter-update).
	UpdateRouteDeviation(ctx context.Context, company, dedupKey string, meters float64) error

	// ResolveOpenAlerts resolves open alerts matching the dedup key prefix
	// (offline: status OK → resolve). Returns the number of resolved rows.
	ResolveOpenAlerts(ctx context.Context, company, dedupKey string) (int64, error)

	// OpenSOAlerts lists open SOS alerts older than `olderThan` with an
	// escalation counter below the cap (escalation sweeper, PRD §5.9.5).
	OpenSOAlerts(ctx context.Context, company string, olderThan time.Time, maxCount int) ([]models.Alert, error)

	// EscalateAlert bumps the escalation counter of one open alert.
	EscalateAlert(ctx context.Context, company string, id int64, count int) error

	// InsertNotifications appends td_notifications rows (delivery audit).
	InsertNotifications(ctx context.Context, company string, rows []models.NotificationRow) error

	// LoadGeofences loads active geofences with their enabled vehicle mapping.
	LoadGeofences(ctx context.Context, company string) ([]models.Geofence, error)

	// LoadSpeedConfigs loads enabled speed configs (vehicle-specific + global).
	LoadSpeedConfigs(ctx context.Context, company string) ([]models.SpeedConfig, error)

	// LoadAssignments loads in-progress route assignments with waypoints.
	LoadAssignments(ctx context.Context, company string) ([]models.Assignment, error)

	// ActiveVehicles lists active vehicles (id + IMEI) of one company — the
	// input of the OFFLINE sweeper.
	ActiveVehicles(ctx context.Context, company string) ([]VehicleRef, error)

	// --- B5a fuel sensor (PRD Module 7, FR-7.2/FR-7.6) -------------------------
	// FuelConfigs loads per-vehicle + tenant-wide fuel thresholds. A vehicle row
	// wins over the global row; an empty set falls back to the global config (or
	// the global defaults in Config.Fuel / FR-7.6 when even the global row is
	// absent). Missing rows are never an alert failure.
	FuelConfigs(ctx context.Context, company string) ([]models.FuelConfig, error)

	// UpsertFuelConfig inserts/replaces one fuel config row (the vehicle_id
	// column is nullable; null = tenant-wide default).
	UpsertFuelConfig(ctx context.Context, company string, cfg *models.FuelConfig, by int64) error

	// --- B8 driver behaviour (migration 022) ---------------------------------
	// InsertDriverEvent appends one td_driver_events row.
	InsertDriverEvent(ctx context.Context, company string, ev *models.DriverEvent) error

	// DailyDriverEventCounts aggregates the events of one vehicle for one day
	// (the input of the score formula).
	DailyDriverEventCounts(ctx context.Context, company string, vehicleID int64, day time.Time) (models.DriverEventCounts, error)

	// DailyDriverDistanceKM sums the B7.2 trip distance of one vehicle for one day
	// (migration 025): the normaliser of the driver score. Trips are matched by
	// start_time so a trip never counts on two days.
	DailyDriverDistanceKM(ctx context.Context, company string, vehicleID int64, day time.Time) (float64, error)

	// UpsertDriverScore writes/refreshes the daily th_driver_scores row.
	UpsertDriverScore(ctx context.Context, company string, sc *models.DriverScore) error

	// --- B8 maintenance reminders (migration 023) ----------------------------
	// MaintenanceSchedules lists active, non-deleted schedules of a company.
	MaintenanceSchedules(ctx context.Context, company string) ([]models.MaintenanceSchedule, error)

	// VehicleUsage returns the odometer/engine-hours snapshot per vehicle
	// (tm_vehicles accumulators maintained by worker-live, B7.1).
	VehicleUsage(ctx context.Context, company string) (map[int64]models.VehicleUsage, error)

	// TouchMaintenanceReminder stamps last_reminder_at after a reminder fired.
	TouchMaintenanceReminder(ctx context.Context, company string, scheduleID int64, at time.Time) error
}
