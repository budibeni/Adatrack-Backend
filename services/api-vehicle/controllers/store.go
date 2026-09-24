package controllers

import (
	"context"
	"time"

	"adatrack_gps/api-vehicle/models"
)

// UserRecord is the master `tm_users` row the auth layer needs.
type UserRecord struct {
	ID                 int64
	CompanyCode        string
	Email              string
	GlobalRole         string
	IsActive           bool
	MustChangePassword bool
}

// VehicleQuery is the validated vehicle list filter.
type VehicleQuery struct {
	CompanyCode string
	// AllVehicles grants tenant-wide read (Admin/Manager); otherwise only
	// AssignedIDs are visible (row-level RBAC, PRD §3.1/§9.2).
	AssignedIDs []int64
	AllVehicles bool
	Status      string
	Search      string
	IncludeDel  bool
	Page        int
	Limit       int
}

// GeofenceQuery is the validated geofence list filter.
type GeofenceQuery struct {
	CompanyCode string
	Page        int
	Limit       int
	IncludeDel  bool
}

// RouteQuery is the validated route list filter.
type RouteQuery struct {
	CompanyCode string
	Page        int
	Limit       int
	IncludeDel  bool
}

// AlertQuery is the validated alert list filter (PRD §8.1 pagination + row-level).
type AlertQuery struct {
	CompanyCode string
	Type        string
	Severity    string
	Status      string
	AssignedIDs []int64
	AllVehicles bool
	From, To    time.Time
	Page        int
	Limit       int
}

// CommandQuery is the validated downlink-command list filter.
type CommandQuery struct {
	CompanyCode string
	VehicleID   int64
	Status      string
	AssignedIDs []int64
	AllVehicles bool
	Page        int
	Limit       int
}

// Store is the persistence surface of api-vehicle. Every statement is
// parameterized (PRD §9.6) and always scoped by tenant schema + `deleted_at`.
type Store interface {
	// --- master: auth -------------------------------------------------------
	UserByID(ctx context.Context, id int64) (*UserRecord, error)

	// --- company schema: RBAC ------------------------------------------------
	TenantAccess(ctx context.Context, companyCode string, userID int64) (roleOverride string, isActive bool, found bool, err error)
	AssignedVehicleIDs(ctx context.Context, companyCode string, userID int64) ([]int64, error)

	// --- vehicles (CRUD + soft delete/restore + IMEI map sync) ---------------
	ListVehicles(ctx context.Context, q VehicleQuery) ([]models.Vehicle, int64, error)
	VehicleByID(ctx context.Context, company string, id int64, includeDeleted bool) (*models.Vehicle, error)
	IMEIExists(ctx context.Context, company, imei string, excludeID int64) (bool, error)
	CreateVehicle(ctx context.Context, company string, v *models.Vehicle, createdBy int64) (int64, error)
	UpdateVehicle(ctx context.Context, company string, v *models.Vehicle, updatedBy int64) error
	SoftDeleteVehicle(ctx context.Context, company string, id, by int64, reason string) error
	RestoreVehicle(ctx context.Context, company string, id int64) error
	SyncIMEIMap(ctx context.Context, imei, company string, vehicleID int64) error
	SoftDeleteIMEIMap(ctx context.Context, imei, company string) error

	// --- geofences -----------------------------------------------------------
	ListGeofences(ctx context.Context, q GeofenceQuery) ([]models.Geofence, int64, error)
	GeofenceByID(ctx context.Context, company string, id int64, includeDeleted bool) (*models.Geofence, error)
	GeofenceNameExists(ctx context.Context, company, name string, excludeID int64) (bool, error)
	CreateGeofence(ctx context.Context, company string, g *models.Geofence, createdBy int64) (int64, error)
	UpdateGeofence(ctx context.Context, company string, g *models.Geofence, updatedBy int64) error
	SoftDeleteGeofence(ctx context.Context, company string, id, by int64, reason string) error
	RestoreGeofence(ctx context.Context, company string, id int64) error
	ReplaceGeofenceVehicles(ctx context.Context, company string, geofenceID int64, vehicleIDs []int64, by int64) error

	// --- routes + assignments -------------------------------------------------
	ListRoutes(ctx context.Context, q RouteQuery) ([]models.Route, int64, error)
	RouteByID(ctx context.Context, company string, id int64, includeDeleted bool) (*models.Route, error)
	RouteNameExists(ctx context.Context, company, name string, excludeID int64) (bool, error)
	CreateRoute(ctx context.Context, company string, r *models.Route, createdBy int64) (int64, error)
	UpdateRoute(ctx context.Context, company string, r *models.Route, updatedBy int64) error
	SoftDeleteRoute(ctx context.Context, company string, id, by int64, reason string) error
	RestoreRoute(ctx context.Context, company string, id int64) error
	ListAssignments(ctx context.Context, company string, routeID int64) ([]models.RouteAssignment, error)
	AssignmentByID(ctx context.Context, company string, routeID, id int64) (*models.RouteAssignment, error)
	CreateAssignment(ctx context.Context, company string, a *models.RouteAssignment, by int64) (int64, error)
	UpdateAssignmentStatus(ctx context.Context, company string, a *models.RouteAssignment) error
	SoftDeleteAssignment(ctx context.Context, company string, routeID, id, by int64, reason string) error

	// --- speed configs ---------------------------------------------------------
	ListSpeedConfigs(ctx context.Context, company string, includeDeleted bool) ([]models.SpeedConfig, error)
	SpeedConfigByID(ctx context.Context, company string, id int64, includeDeleted bool) (*models.SpeedConfig, error)
	CreateSpeedConfig(ctx context.Context, company string, sc *models.SpeedConfig, createdBy int64) (int64, error)
	UpdateSpeedConfig(ctx context.Context, company string, sc *models.SpeedConfig, updatedBy int64) error
	SoftDeleteSpeedConfig(ctx context.Context, company string, id, by int64, reason string) error
	RestoreSpeedConfig(ctx context.Context, company string, id int64) error

	// --- fuel configs + history (B5a, PRD Module 7 / FR-7.6/FR-7.7) ------------
	ListFuelConfigs(ctx context.Context, company string, includeDeleted bool) ([]models.FuelConfig, error)
	FuelConfigByID(ctx context.Context, company string, id int64, includeDeleted bool) (*models.FuelConfig, error)
	CreateFuelConfig(ctx context.Context, company string, fc *models.FuelConfig, createdBy int64) (int64, error)
	UpdateFuelConfig(ctx context.Context, company string, fc *models.FuelConfig, updatedBy int64) error
	SoftDeleteFuelConfig(ctx context.Context, company string, id, by int64, reason string) error
	RestoreFuelConfig(ctx context.Context, company string, id int64) error
	ListFuelHistory(ctx context.Context, company string, vehicleID int64, from, to time.Time, page, limit int) ([]models.FuelLog, int64, error)

	// --- alerts (read + life-cycle) --------------------------------------------
	ListAlerts(ctx context.Context, q AlertQuery) ([]models.Alert, int64, error)
	AlertByID(ctx context.Context, company string, id int64) (*models.Alert, error)
	AcknowledgeAlert(ctx context.Context, company string, id, by int64) (int64, error)
	ResolveAlert(ctx context.Context, company string, id, by int64) (int64, error)

	// --- B8 downlink commands -------------------------------------------------
	// CreateDeviceCommand inserts a `pending` td_device_commands row (the audit
	// trail of the request; ingestion-tcp then drives it to sent/acked/...).
	CreateDeviceCommand(ctx context.Context, company string, cmd *models.DeviceCommand) (int64, error)
	// DeviceCommandByRequestID reads the row back (the POST response body).
	DeviceCommandByRequestID(ctx context.Context, company, requestID string) (*models.DeviceCommand, error)
	// ListDeviceCommands returns the command history with row-level filtering.
	ListDeviceCommands(ctx context.Context, q CommandQuery) ([]models.DeviceCommand, int64, error)
}
