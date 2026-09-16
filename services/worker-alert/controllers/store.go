package controllers

import (
	"context"
	"time"

	"ajb_gps/worker-alert/models"
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
}
