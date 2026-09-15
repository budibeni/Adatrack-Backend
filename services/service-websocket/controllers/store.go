package controllers

import (
	"context"
	"time"

	"ajb_gps/internal/tenant"
	"ajb_gps/service-websocket/models"
)

// UserRecord is the master `tm_users` row the auth layer needs (never leaves the
// service: the password hash is not part of any response DTO).
type UserRecord struct {
	ID                 int64
	CompanyCode        string
	Email              string
	FullName           string
	PasswordHash       string
	GlobalRole         string
	IsActive           bool
	MustChangePassword bool
	FailedAttempts     int
	LockedUntil        *time.Time
}

// VehicleQuery is the validated `GET /api/v1/vehicles` filter (PRD §8.5).
type VehicleQuery struct {
	CompanyCode string
	// AllVehicles grants tenant-wide read (Admin/Manager); otherwise only
	// `AssignedIDs` is returned (row-level RBAC, PRD §3.1/§9.2).
	AllVehicles bool
	AssignedIDs []int64
	Status      string
	Search      string
	IncludeDel  bool
	Page        int
	Limit       int
}

// HistoryQuery is the validated `GET /api/v1/vehicles/{id}/history` filter.
type HistoryQuery struct {
	CompanyCode string
	VehicleID   int64
	IMEI        string
	From        time.Time
	To          time.Time
	Page        int
	Limit       int
}

// Store is the persistence surface of service-websocket. Every statement is
// parameterized (PRD §9.6) and always filters by tenant schema + `deleted_at`.
type Store interface {
	// --- master: auth + governance (PRD §6.1) ------------------------------
	UserByEmail(ctx context.Context, email string) (*UserRecord, error)
	UserByID(ctx context.Context, id int64) (*UserRecord, error)
	UserEmailExists(ctx context.Context, email string) (bool, error)
	CreateUser(ctx context.Context, u UserRecord) (int64, error)
	RecordLoginSuccess(ctx context.Context, userID int64) error
	RecordLoginFailure(ctx context.Context, userID int64, attempts int, lockedUntil *time.Time) error
	CompanyExists(ctx context.Context, code string) (bool, error)
	WriteAudit(ctx context.Context, rows []AuditRow) error

	// --- company schema: RBAC (PRD §3.1, §9.2) -----------------------------
	TenantAccess(ctx context.Context, companyCode string, userID int64) (roleOverride string, isActive bool, found bool, err error)
	UpsertTenantAccess(ctx context.Context, companyCode string, userID int64, role string) error
	AssignedVehicleIDs(ctx context.Context, companyCode string, userID int64) ([]int64, error)
	AssignVehicles(ctx context.Context, companyCode string, userID int64, vehicleIDs []int64) error
	ExistingVehicleIDs(ctx context.Context, companyCode string, ids []int64) ([]int64, error)

	// --- company schema: read path (FR-5.1/FR-5.2) -------------------------
	ListVehicles(ctx context.Context, q VehicleQuery) ([]models.Vehicle, int64, error)
	VehicleByID(ctx context.Context, companyCode string, id int64, includeDeleted bool) (*models.Vehicle, error)
	VehicleHistory(ctx context.Context, q HistoryQuery) ([]models.Position, int64, error)
	VehicleMeta(ctx context.Context, companyCode string, id int64) (models.Vehicle, error)

	// --- tenant routing (PRD §6.2) -----------------------------------------
	ProvisionTenant(ctx context.Context, opts tenant.ProvisionOptions) (*tenant.ProvisionResult, error)
	PingTenant(ctx context.Context, companyCode string) error
}
