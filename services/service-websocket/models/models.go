// Package models holds the request/response and WebSocket payload shapes of
// service-websocket (PRD §8.1 response format, §8.2 REST endpoints, §8.3 WS
// contract, FR-5.2 VehicleUpdateData).
package models

import "encoding/json"

// Role names (master `tm_users.global_role`, PRD §3.1).
const (
	RoleSuperAdmin = "SuperAdmin"
	RoleAdmin      = "Admin"
	RoleManager    = "Manager"
	RoleOperator   = "Operator"
	RoleDriver     = "Driver"
)

// PlatformCompanyCode is the reserved platform tenant (PRD §3.1 "Platform Tier").
const PlatformCompanyCode = "DEFAULT"

// User is the authenticated identity resolved from master `tm_users` plus the
// per-tenant role from `tm_user_company_access` (role_override wins).
type User struct {
	ID                 int64  `json:"id"`
	Email              string `json:"email"`
	FullName           string `json:"full_name"`
	GlobalRole         string `json:"global_role"`
	Role               string `json:"role"` // effective role (tenant override or global)
	CompanyCode        string `json:"company_code"`
	MustChangePassword bool   `json:"must_change_password"`
	IsActive           bool   `json:"is_active"`
}

// IsPlatform reports whether this identity is the platform (governance) tier:
// context `default` + role SuperAdmin (PRD §3.1).
func (u User) IsPlatform() bool {
	return u.GlobalRole == RoleSuperAdmin && u.CompanyCode == PlatformCompanyCode
}

// LoginRequest is the `POST /api/v1/auth/login` body (PRD §9.1).
type LoginRequest struct {
	Email    string `json:"email" binding:"required,email,max=255"`
	Password string `json:"password" binding:"required,min=8,max=128"`
}

// RefreshRequest is the `POST /api/v1/auth/refresh` body (FR-5.7).
type RefreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required,min=32,max=256"`
}

// LogoutRequest is the `POST /api/v1/auth/logout` body (FR-5.7). `refresh_token`
// is optional — when present it is revoked together with the access token.
type LogoutRequest struct {
	RefreshToken string `json:"refresh_token,omitempty" binding:"omitempty,min=32,max=256"`
}

// TokenPair is returned by login and refresh.
type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"` // access-token lifetime in seconds
}

// LoginResponse additionally carries the resolved identity so the dashboard can
// render the shell without a second call.
type LoginResponse struct {
	TokenPair
	User User `json:"user"`
}

// CreateCompanyRequest is the `POST /api/v1/companies` body (FR-5.5).
type CreateCompanyRequest struct {
	Code         string `json:"code" binding:"required,min=2,max=20"`
	Name         string `json:"name" binding:"required,min=2,max=100"`
	CountryCode  string `json:"country_code,omitempty" binding:"omitempty,len=2,alpha"`
	Timezone     string `json:"timezone,omitempty" binding:"omitempty,max=50"`
	BusinessType string `json:"business_type,omitempty" binding:"omitempty,oneof=b2b b2c"`
	// AdminEmail overrides the default `admin@{code}.local` tenant admin (FR-5.5).
	AdminEmail string `json:"admin_email,omitempty" binding:"omitempty,email,max=255"`
}

// AdminUser is the auto-created tenant admin reported by FR-5.5.
type AdminUser struct {
	Email              string `json:"email"`
	MustChangePassword bool   `json:"must_change_password"`
}

// CreateCompanyResponse is the FR-5.5 `201 Created` payload.
type CreateCompanyResponse struct {
	Code              string    `json:"code"`
	Name              string    `json:"name"`
	CountryCode       string    `json:"country_code"`
	Timezone          string    `json:"timezone"`
	BusinessType      string    `json:"business_type"`
	DatabaseName      string    `json:"database_name"`
	MigrationsApplied int       `json:"migrations_applied"`
	AdminUser         AdminUser `json:"admin_user"`
}

// CreateUserRequest is the `POST /api/v1/users` body (FR-5.6).
type CreateUserRequest struct {
	Email       string  `json:"email" binding:"required,email,max=255"`
	FullName    string  `json:"full_name" binding:"required,min=2,max=100"`
	Role        string  `json:"role" binding:"required,oneof=Admin Manager Operator Driver SuperAdmin"`
	CompanyCode string  `json:"company_code" binding:"required,min=2,max=20"`
	Password    string  `json:"password,omitempty" binding:"omitempty,min=8,max=128"`
	PhoneNumber string  `json:"phone_number,omitempty" binding:"omitempty,max=20"`
	VehicleIDs  []int64 `json:"vehicle_ids,omitempty" binding:"omitempty,max=1000,dive,gt=0"`
}

// CreateUserResponse is the FR-5.6 `201 Created` payload.
type CreateUserResponse struct {
	ID                 int64   `json:"id"`
	Email              string  `json:"email"`
	FullName           string  `json:"full_name"`
	Role               string  `json:"role"`
	CompanyCode        string  `json:"company_code"`
	MustChangePassword bool    `json:"must_change_password"`
	VehicleIDs         []int64 `json:"vehicle_ids,omitempty"`
}

// LiveState mirrors the Redis value written by worker-live at
// `adatrack_gps:{company}:vehicle:state:{IMEI}` (FR-2.1) — including the B5a fuel
// fields and the B6 satellites/altitude/gsm_signal enrichment.
type LiveState struct {
	IMEI        string   `json:"imei"`
	CompanyCode string   `json:"company_code"`
	VehicleID   int64    `json:"vehicle_id"`
	Lat         float64  `json:"lat"`
	Lon         float64  `json:"lon"`
	Speed       float64  `json:"speed"`
	Heading     int16    `json:"heading"`
	Satellites  uint8    `json:"satellites"`
	Altitude    int16    `json:"altitude"`
	Battery     uint8    `json:"battery_level"`
	GsmSignal   uint8    `json:"gsm_signal"`
	ACC         *bool    `json:"acc,omitempty"`
	Mileage     uint32   `json:"mileage,omitempty"`
	Fix         bool     `json:"fix"`
	Status      string   `json:"status"`
	LastSeen    int64    `json:"last_seen"`
	Timestamp   int64    `json:"timestamp"`
	FuelLevel   *float64 `json:"fuel_level,omitempty"`
	FuelVolume  *float64 `json:"fuel_volume,omitempty"`
	FuelTempC   *float64 `json:"fuel_temp_c,omitempty"`
}

// Vehicle is the REST representation of `tm_vehicles` enriched with the live
// state (FR-5.1/FR-5.2 read path).
type Vehicle struct {
	ID           int64      `json:"id"`
	IMEI         string     `json:"imei"`
	PlateNumber  string     `json:"plate_number"`
	Make         string     `json:"make,omitempty"`
	Model        string     `json:"model,omitempty"`
	Variant      string     `json:"variant,omitempty"`
	Color        string     `json:"color,omitempty"`
	FuelType     string     `json:"fuel_type,omitempty"`
	CategoryCode string     `json:"vehicle_category_code,omitempty"`
	TypeCode     string     `json:"vehicle_type_code,omitempty"`
	DriverUserID *int64     `json:"driver_user_id,omitempty"`
	DriverName   string     `json:"driver_name,omitempty"`
	DeviceModel  string     `json:"device_model,omitempty"`
	Status       string     `json:"status"`
	DeletedAt    *string    `json:"deleted_at,omitempty"`
	Live         *LiveState `json:"live,omitempty"`
}

// Position is one `th_telemetry_logs` row of the history endpoint.
type Position struct {
	Timestamp  string  `json:"timestamp"`
	Lat        float64 `json:"lat"`
	Lon        float64 `json:"lon"`
	Speed      float64 `json:"speed"`
	Heading    int16   `json:"heading,omitempty"`
	Satellites uint8   `json:"satellites,omitempty"`
	Altitude   int16   `json:"altitude,omitempty"`
	Battery    uint8   `json:"battery_level,omitempty"`
	GsmSignal  uint8   `json:"gsm_signal,omitempty"`
	ACC        *bool   `json:"acc,omitempty"`
	Fix        bool    `json:"fix"`
}

// Pagination is the optional PRD §8.1 pagination block.
type Pagination struct {
	Page  int   `json:"page"`
	Limit int   `json:"limit"`
	Total int64 `json:"total"`
}

// VehicleUpdateData is the FR-5.2 WebSocket `VEHICLE_UPDATE` payload.
type VehicleUpdateData struct {
	IMEI        string   `json:"imei"`
	CompanyCode string   `json:"company_code"`
	VehicleID   int64    `json:"vehicle_id"`
	PlateNumber string   `json:"plate_number,omitempty"`
	Lat         float64  `json:"lat"`
	Lon         float64  `json:"lon"`
	Speed       float64  `json:"speed"`
	Heading     int16    `json:"heading"`
	ACC         *bool    `json:"acc,omitempty"`
	Status      string   `json:"status"`
	Battery     uint8    `json:"battery"`
	FuelLevel   *float64 `json:"fuel_level,omitempty"`
	FuelVolume  *float64 `json:"fuel_volume,omitempty"`
	FuelTempC   *float64 `json:"fuel_temp_c,omitempty"`
	Satellites  uint8    `json:"satellites"`
	Altitude    int16    `json:"altitude"`
	GsmSignal   uint8    `json:"gsm_signal"`
	Timestamp   string   `json:"timestamp"` // RFC3339 (PRD FR-5.2)
	LastSeen    int64    `json:"last_seen"`
}

// Envelope is the generic PRD §8.1 success response.
type Envelope struct {
	Status     string      `json:"status"`
	Data       any         `json:"data,omitempty"`
	Pagination *Pagination `json:"pagination,omitempty"`
}

// ErrorEnvelope is the generic PRD §8.1 error response.
type ErrorEnvelope struct {
	Status    string `json:"status"`
	ErrorCode string `json:"error_code"`
	Message   string `json:"message"`
	Timestamp string `json:"timestamp"`
	// Errors carries the per-field validation failures (PRD §8.5 rule 1).
	Errors map[string]string `json:"errors,omitempty"`
}

// MediaEventData is the `MEDIA_EVENT` payload (PRD Module 8 / FR-8.5) published
// by service-media on `media.event.<company_code>`.
type MediaEventData struct {
	ID          int64  `json:"id"`
	CompanyCode string `json:"company_code"`
	VehicleID   int64  `json:"vehicle_id"`
	IMEI        string `json:"imei"`
	EventType   string `json:"event_type"`
	ObjectKey   string `json:"object_key"`
	MimeType    string `json:"mime_type"`
	FileSize    int64  `json:"file_size"`
	Status      string `json:"status"`
	URL         string `json:"url,omitempty"`
	CapturedAt  string `json:"captured_at"`
}

// AlertNotify is the `notify.alert.<vehicle_id>` payload published by
// worker-alert (PRD §5.9.8) and fanned out to entitled clients.
type AlertNotify struct {
	AlertID    int64           `json:"alert_id"`
	Type       string          `json:"type"`
	Severity   string          `json:"severity"`
	Company    string          `json:"company"`
	VehicleID  int64           `json:"vehicle_id"`
	IMEI       string          `json:"imei"`
	Lat        *float64        `json:"lat,omitempty"`
	Lon        *float64        `json:"lon,omitempty"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
	DetectedAt string          `json:"detected_at"`
}

// WebSocket event names (PRD §8.3 + FR-5.2).
const (
	EventVehicleUpdate = "VEHICLE_UPDATE"
	EventMediaEvent    = "MEDIA_EVENT"
	EventError         = "ERROR"
	EventSubscribed    = "SUBSCRIBED"
	EventUnsubscribed  = "UNSUBSCRIBED"
	EventHeartbeat     = "HEARTBEAT"
	// EventNotifyAlertPrefix mirrors the documented `notify.alert.<vehicle_id>`
	// event name (PRD §8.3): the client sees exactly the subject it would
	// subscribe to on the bus.
	EventNotifyAlertPrefix = "notify.alert."
)

// WSEnvelope is every server→client WebSocket frame (PRD §8.3).
type WSEnvelope struct {
	Event     string `json:"event"`
	Data      any    `json:"data,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
	Message   string `json:"message,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
}

// WSRequest is a client→server frame (subscription model, PRD §8.3):
//
//	{"action":"subscribe","vehicle_ids":[1,2]}
//	{"action":"subscribe","topic":"vehicle.update.1"}
//	{"action":"unsubscribe","vehicle_ids":[1]}
//	{"action":"ping"}
type WSRequest struct {
	Action     string  `json:"action"`
	VehicleIDs []int64 `json:"vehicle_ids,omitempty"`
	Topic      string  `json:"topic,omitempty"`
}

// Connection status values reported by worker-live (FR-2.2).
const (
	StatusOnline  = "ONLINE"
	StatusIdle    = "IDLE"
	StatusOffline = "OFFLINE"
)
