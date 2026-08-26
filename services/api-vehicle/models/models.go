// Package models holds api-vehicle data types (DTOs).
package models

import (
	"encoding/json"
	"time"
)

// OkResponse is the standard success envelope: { status, data, pagination? }.
type OkResponse struct {
	Status       string          `json:"status"`
	Data         interface{}     `json:"data"`
	Pagination   *PaginationInfo `json:"pagination,omitempty"`
	TotalRecords *int64          `json:"total_records,omitempty"` // GAP #1: riwayat berformat array titik
}

// PaginationInfo describes page/limit/total for list endpoints.
type PaginationInfo struct {
	Page  int   `json:"page"`
	Limit int   `json:"limit"`
	Total int64 `json:"total"`
}

// ApiErrorResponse is the standard error envelope (GAP #3).
type ApiErrorResponse struct {
	Status    string `json:"status"`
	ErrorCode string `json:"error_code"`
	Message   string `json:"message"`
	Timestamp string `json:"timestamp"`
}

// MasterUser mirrors the master.users row (GLOBAL auth authority, PRD §6.1).
// Enterprise-standard identity fields (PRD §6.1, migration 011) are included
// so that auth, RBAC, and user-management flows can enforce email verification,
// MFA, account lockout, soft-delete, and localization.
type MasterUser struct {
	ID           uint64
	CompanyID    int64
	CompanyCode  string
	Email        string
	PasswordHash string
	FullName     string
	// --- Enterprise-standard identity fields (migration 011) ---
	Username            string     // nullable; COALESCE('') in query
	FirstName           string     // nullable; COALESCE('') in query
	LastName            string     // nullable; COALESCE('') in query
	PhoneNumber         string     // nullable; E.164; COALESCE('') in query
	EmailVerified       bool       // NOT NULL DEFAULT FALSE
	PhoneVerified       bool       // NOT NULL DEFAULT FALSE
	MFAEnabled          bool       // NOT NULL DEFAULT FALSE
	Locale              string     // NOT NULL DEFAULT 'id'
	AvatarURL           string     // nullable; COALESCE('') in query
	FailedLoginAttempts int        // NOT NULL DEFAULT 0
	PasswordChangedAt   *time.Time // nullable
	LockedUntil         *time.Time // nullable — account lockout expiry
	DeletedAt           *time.Time // nullable — soft delete (NULL = active)
	LastLogin           *time.Time // nullable
	CreatedBy           *uint64    // nullable FK → master.users.id (audit)
	UpdatedBy           *uint64    // nullable FK → master.users.id (audit)
	Role                string     // global role: Admin | Manager | Operator | Driver
	Status              string     // active | inactive | suspended
}

// AuthUser is the authenticated caller stored in the gin context.
type AuthUser struct {
	ID            uint64
	CompanyCode   string
	CompanyID     int64
	Email         string
	Role          string
	CompanyUserID uint64 // user_company_access.user_id (FK created_by/driver)
}

// LoginResponse is returned by POST /api/v1/auth/login (dan /auth/refresh).
type LoginResponse struct {
	Token            string          `json:"token"`
	TokenType        string          `json:"token_type"`
	ExpiresIn        int64           `json:"expires_in"`
	RefreshToken     string          `json:"refresh_token,omitempty"`      // B4 hardening
	RefreshExpiresIn int64           `json:"refresh_expires_in,omitempty"` // detik; B4 hardening
	User             AuthUserPayload `json:"user"`
}

// AuthUserPayload mirrors the authenticated user in JSON responses.
type AuthUserPayload struct {
	ID          uint64 `json:"id"`
	Username    string `json:"username,omitempty"`
	CompanyCode string `json:"company_code,omitempty"`
	Email       string `json:"email"`
	Role        string `json:"role"`
}

// ---------------------------------------------------------------------------
// Vehicles (company migration 002 — enterprise-standard schema).
// ---------------------------------------------------------------------------

// CreateVehicleRequest is the POST /vehicles body.
type CreateVehicleRequest struct {
	IMEI          string  `json:"imei" binding:"required"`
	PlateNumber   string  `json:"plate_number" binding:"required"`
	Make          *string `json:"make,omitempty"`
	Model         *string `json:"model,omitempty"`
	Variant       *string `json:"variant,omitempty"`
	Year          *int    `json:"year_of_manufacture,omitempty"`
	Color         *string `json:"color,omitempty"`
	FuelType      *string `json:"fuel_type,omitempty"`
	VehicleTypeCd *string `json:"vehicle_type_code,omitempty"`
	CategoryCd    *string `json:"vehicle_category_code,omitempty"`
	DriverUserID  *uint64 `json:"driver_user_id,omitempty"`
	DeviceModel   *string `json:"device_model,omitempty"`
	FirmwareVer   *string `json:"firmware_version,omitempty"`
	RegNumber     *string `json:"registration_number,omitempty"`
	Notes         *string `json:"notes,omitempty"`
}

// UpdateVehicleRequest is the PATCH /vehicles/:id body (partial update).
type UpdateVehicleRequest struct {
	PlateNumber  *string `json:"plate_number,omitempty"`
	Make         *string `json:"make,omitempty"`
	Model        *string `json:"model,omitempty"`
	Color        *string `json:"color,omitempty"`
	FuelType     *string `json:"fuel_type,omitempty"`
	DeviceModel  *string `json:"device_model,omitempty"`
	FirmwareVer  *string `json:"firmware_version,omitempty"`
	RegNumber    *string `json:"registration_number,omitempty"`
	Notes        *string `json:"notes,omitempty"`
	Status       *string `json:"status,omitempty"` // active|inactive|maintenance
	DriverUserID *uint64 `json:"driver_user_id,omitempty"`
}

// VehicleItem is the API response shape for a vehicle.
type VehicleItem struct {
	ID            uint64          `json:"id"`
	IMEI          string          `json:"imei"`
	PlateNumber   string          `json:"plate_number"`
	Make          *string         `json:"make,omitempty"`
	Model         *string         `json:"model,omitempty"`
	FuelType      *string         `json:"fuel_type,omitempty"`
	VehicleTypeCd *string         `json:"vehicle_type_code,omitempty"`
	DriverUserID  *uint64         `json:"driver_user_id,omitempty"`
	DeviceModel   *string         `json:"device_model,omitempty"`
	Status        string          `json:"status"`
	CreatedAt     time.Time       `json:"created_at"`
	LastPosition  json.RawMessage `json:"last_position,omitempty"`
}

// AssignUserRequest is the POST /vehicles/:id/users body.
type AssignUserRequest struct {
	UserID uint64 `json:"user_id" binding:"required"`
}

// ---------------------------------------------------------------------------
// Speed configs (company migration 007).
// ---------------------------------------------------------------------------

// SpeedConfigItem is the response for a speed_configs row.
type SpeedConfigItem struct {
	ID             uint64    `json:"id"`
	VehicleID      *uint64   `json:"vehicle_id,omitempty"` // nil = global default
	SpeedLimitKMH  float64   `json:"speed_limit_kmh"`
	GraceMarginKMH float64   `json:"grace_margin_kmh"`
	IsActive       bool      `json:"is_active"`
	CreatedAt      time.Time `json:"created_at"`
}

// CreateSpeedConfigRequest is the POST /speed-configs body.
type CreateSpeedConfigRequest struct {
	VehicleID      *uint64 `json:"vehicle_id,omitempty"` // omit = global default
	SpeedLimitKMH  float64 `json:"speed_limit_kmh" binding:"required,gt=0"`
	GraceMarginKMH float64 `json:"grace_margin_kmh"`
}

// UpdateSpeedConfigRequest is the PATCH /speed-configs/:id body.
type UpdateSpeedConfigRequest struct {
	SpeedLimitKMH  *float64 `json:"speed_limit_kmh,omitempty" binding:"omitempty,gt=0"`
	GraceMarginKMH *float64 `json:"grace_margin_kmh,omitempty"`
	IsActive       *bool    `json:"is_active,omitempty"`
}

// ---------------------------------------------------------------------------
// Routes + assignments (company migrations 011/012) — B3 route management.
// ---------------------------------------------------------------------------

// Waypoint is one point of a planned route.
type Waypoint struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// RouteItem is the API representation of a routes row (migration 011).
type RouteItem struct {
	ID                   uint64           `json:"id"`
	Name                 string           `json:"name"`
	Waypoints            []Waypoint       `json:"waypoints"`
	EstimatedDurationSec *int             `json:"estimated_duration_sec,omitempty"`
	CreatedBy            uint64           `json:"created_by"` // user_company_access.user_id
	IsActive             bool             `json:"is_active"`
	CreatedAt            time.Time        `json:"created_at"`
	Assignments          []AssignmentItem `json:"assignments,omitempty"`
}

// AssignmentItem is one route_assignments row (migration 012).
type AssignmentItem struct {
	ID              uint64     `json:"id"`
	RouteID         uint64     `json:"route_id"`
	VehicleID       uint64     `json:"vehicle_id"`
	DriverUserID    uint64     `json:"driver_user_id"`
	Status          string     `json:"status"` // not_started|in_progress|completed|delayed
	StartedAt       *time.Time `json:"started_at,omitempty"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	DeviationMeters *float64   `json:"deviation_meters,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

// CreateRouteRequest is the POST /routes body.
type CreateRouteRequest struct {
	Name                 string     `json:"name" binding:"required"`
	Waypoints            []Waypoint `json:"waypoints" binding:"required,min=2,dive"`
	EstimatedDurationSec *int       `json:"estimated_duration_sec,omitempty" binding:"omitempty,gt=0"`
}

// UpdateRouteRequest is the PATCH /routes/:id body.
type UpdateRouteRequest struct {
	Name                 *string     `json:"name,omitempty"`
	Waypoints            *[]Waypoint `json:"waypoints,omitempty"`
	EstimatedDurationSec *int        `json:"estimated_duration_sec,omitempty" binding:"omitempty,gt=0"`
	IsActive             *bool       `json:"is_active,omitempty"`
}

// CreateAssignmentRequest is the POST /routes/:id/assignments body.
type CreateAssignmentRequest struct {
	VehicleID    uint64 `json:"vehicle_id" binding:"required"`
	DriverUserID uint64 `json:"driver_user_id" binding:"required"`
}

// UpdateAssignmentStatusRequest is the PATCH assignment status body.
type UpdateAssignmentStatusRequest struct {
	Status string `json:"status" binding:"required,oneof=not_started in_progress completed delayed"`
}
