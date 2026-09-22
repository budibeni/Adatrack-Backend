package controllers

import (
	"context"
	"time"

	"adatrack_gps/internal"
	"adatrack_gps/service-media/models"
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

// MediaConfig is the per-company media configuration
// (master `tm_company_media_config`, FR-8.1/8.2/8.7). Zero values mean "not
// configured" and fall back to the service defaults.
type MediaConfig struct {
	CompanyCode   string
	Bucket        string
	RetentionDays int
	MaxFileMB     int
	HMACSecret    string
}

// EffectiveRetention resolves the retention horizon for one company.
func (c MediaConfig) EffectiveRetention(def int) int {
	if c.RetentionDays > 0 {
		return c.RetentionDays
	}
	return def
}

// EffectiveMaxFileMB resolves the upload ceiling for one company.
func (c MediaConfig) EffectiveMaxFileMB(def int) int {
	if c.MaxFileMB > 0 {
		return c.MaxFileMB
	}
	return def
}

// VehicleRef is the IMEI → (tenant, vehicle) resolution from master
// `tm_vehicle_imei_map` (anti-spoofing: only registered devices may ingest).
type VehicleRef struct {
	CompanyCode string
	VehicleID   int64
	IsActive    bool
}

// Vehicle is the tenant-side vehicle row media ingestion must validate against.
type Vehicle struct {
	ID          int64
	PlateNumber string
	IMEI        string
	Deleted     bool
}

// MediaQuery is the validated media-catalog list filter (PRD §8.1 pagination +
// row-level RBAC §3.1).
type MediaQuery struct {
	CompanyCode string
	VehicleID   int64
	EventType   string
	Status      string
	IMEI        string
	From, To    time.Time
	// AssignedIDs/AllVehicles implement row-level filtering.
	AssignedIDs []int64
	AllVehicles bool
	IncludeDel  bool
	Page        int
	Limit       int
}

// Store is the persistence surface of service-media. Every statement is
// parameterized (PRD §9.6) and tenant-scoped.
type Store interface {
	// --- master: auth + tenant routing ---------------------------------------
	UserByID(ctx context.Context, id int64) (*UserRecord, error)
	TenantAccess(ctx context.Context, companyCode string, userID int64) (roleOverride string, isActive bool, found bool, err error)
	AssignedVehicleIDs(ctx context.Context, companyCode string, userID int64) ([]int64, error)
	MediaCompanies(ctx context.Context) ([]MediaConfig, error)
	ResolveVehicleByIMEI(ctx context.Context, imei string) (*VehicleRef, error)
	WriteAudit(ctx context.Context, rows []AuditRow) error

	// --- company schema: catalog --------------------------------------------
	VehicleByID(ctx context.Context, company string, id int64, includeDeleted bool) (*Vehicle, error)
	CreateMediaEvent(ctx context.Context, company string, m *models.MediaEvent) (int64, error)
	MediaEventByID(ctx context.Context, company string, id int64, includeDeleted bool) (*models.MediaEvent, error)
	MediaEventByObjectKey(ctx context.Context, company, key string) (*models.MediaEvent, error)
	ListMediaEvents(ctx context.Context, q MediaQuery) ([]models.MediaEvent, int64, error)
	CompleteMediaEvent(ctx context.Context, company string, m *models.MediaEvent) error
	MarkMediaExpired(ctx context.Context, company string, ids []int64) error
	SoftDeleteMediaEvent(ctx context.Context, company string, id, by int64, reason string) error
	RestoreMediaEvent(ctx context.Context, company string, id int64) error
	// ExpiredMediaEvents lists rows the retention sweep must expire: uploaded
	// media past `expires_at` (or captured before the retention horizon) plus
	// uploads that never completed within the pending TTL (FR-8.7).
	ExpiredMediaEvents(ctx context.Context, company string, now, pendingBefore time.Time, limit int) ([]models.MediaEvent, error)
	CountStoredObjects(ctx context.Context, company string) (int64, error)

	// --- readiness ------------------------------------------------------------
	Master() *internal.DBPool
	TenantHealth(ctx context.Context) error
}
