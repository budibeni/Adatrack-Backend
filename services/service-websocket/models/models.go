// Package models holds service-websocket data types (DTOs + WS events).
package models

import (
	"encoding/json"
	"time"
)

// OkResponse is the standard success envelope { status, data, pagination? }.
type OkResponse struct {
	Status       string          `json:"status"`
	Data         interface{}     `json:"data"`
	Pagination   *PaginationInfo `json:"pagination,omitempty"`
	TotalRecords int64           `json:"total_records,omitempty"` // history: jumlah record keseluruhan
}

// PaginationInfo describes page/limit/total for list endpoints.
type PaginationInfo struct {
	Page  int   `json:"page"`
	Limit int   `json:"limit"`
	Total int64 `json:"total"`
}

// ApiErrorResponse is the standard error envelope.
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
	Role                string     // global role: Admin / Manager / Operator / Driver
	Status              string     // active / inactive / suspended
}

// AuthUser is the authenticated caller stored in the gin context. The effective
// role is resolved from user_company_access.role_override (bila ada) else the
// global master role (PRD §6.1 / FR-5.1).
type AuthUser struct {
	ID            uint64 // master.users.id
	CompanyCode   string
	CompanyID     int64
	Email         string
	Role          string // effective role (mis. "Admin", "Manager", "Operator", "Driver")
	CompanyUserID uint64 // user_company_access.id (untuk created_by / acknowledged_by)
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
	CompanyCode string `json:"company_code"`
	Email       string `json:"email"`
	Role        string `json:"role"`
}

// VehicleListItem is the vehicle summary returned to authenticated users.
type VehicleListItem struct {
	ID           uint64   `json:"id"`
	IMEI         string   `json:"imei"`
	PlateNumber  string   `json:"plate_number"`
	DeviceModel  string   `json:"device_model,omitempty"`
	Status       string   `json:"status"`
	LastPosition *LastPos `json:"last_position,omitempty"`
}

// LastPos is the last known position (from live Redis state, fallback MySQL).
// FuelLevel & Acc are surfaced from the live state (worker-live) when present.
type LastPos struct {
	Lat       float64    `json:"lat"`
	Lon       float64    `json:"lon"`
	Speed     float64    `json:"speed"`
	FuelLevel *float64   `json:"fuel_level,omitempty"`
	Acc       *bool      `json:"acc,omitempty"`
	Timestamp *time.Time `json:"timestamp,omitempty"`
}

// HistoryPoint is one telemetry sample.
type HistoryPoint struct {
	Lat       float64 `json:"lat"`
	Lon       float64 `json:"lon"`
	Speed     float64 `json:"speed"`
	Heading   float64 `json:"heading"`
	Timestamp string  `json:"timestamp"`
}

// ---------------------------------------------------------------------------
// Geofence (company migration 005 — area_type circle|polygon, coordinates GeoJSON)
// ---------------------------------------------------------------------------

// GeofenceItem mirrors the geofences table.
type GeofenceItem struct {
	ID             uint64          `json:"id"`
	Name           string          `json:"name"`
	AreaType       string          `json:"area_type"` // circle | polygon
	Coordinates    json.RawMessage `json:"coordinates"`
	RadiusMeters   *float64        `json:"radius_meters,omitempty"` // circle only
	BoundaryPoints json.RawMessage `json:"boundary_points,omitempty"`
	CreatedBy      uint64          `json:"created_by"`
	IsActive       bool            `json:"is_active"`
	Vehicles       []uint64        `json:"vehicles,omitempty"` // geofence_vehicles (assigned)
	CreatedAt      time.Time       `json:"created_at"`
}

// GeofenceCreateRequest is the POST /api/v1/geofences body.
type GeofenceCreateRequest struct {
	Name           string          `json:"name"`
	AreaType       string          `json:"area_type"` // "circle" | "polygon"
	Coordinates    json.RawMessage `json:"coordinates"`
	RadiusMeters   *float64        `json:"radius_meters,omitempty"`
	BoundaryPoints json.RawMessage `json:"boundary_points,omitempty"`
	VehicleIDs     []uint64        `json:"vehicle_ids,omitempty"` // linked vehicles
}

// ---------------------------------------------------------------------------
// Alerts (company migration 008)
// ---------------------------------------------------------------------------

// AlertItem mirrors relevant columns of the alerts table.
type AlertItem struct {
	ID             uint64     `json:"id"`
	VehicleID      uint64     `json:"vehicle_id"`
	IMEI           string     `json:"imei"`
	AlertType      string     `json:"alert_type"`
	Severity       string     `json:"severity"`
	Description    *string    `json:"description,omitempty"`
	Status         string     `json:"status"`
	AcknowledgedBy *uint64    `json:"acknowledged_by,omitempty"`
	AcknowledgedAt *time.Time `json:"acknowledged_at,omitempty"`
	ResolvedAt     *time.Time `json:"resolved_at,omitempty"`
	VehicleLat     *float64   `json:"vehicle_lat,omitempty"`
	VehicleLon     *float64   `json:"vehicle_lon,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

// ---------------------------------------------------------------------------
// Routes (company migrations 011 + 012)
// ---------------------------------------------------------------------------

// RouteWaypoint is one point of a planned route.
type RouteWaypoint struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// RouteItem mirrors the routes table (011).
type RouteItem struct {
	ID                   uint64                `json:"id"`
	Name                 string                `json:"name"`
	Waypoints            []RouteWaypoint       `json:"waypoints"`
	EstimatedDurationSec *int                  `json:"estimated_duration_sec,omitempty"`
	CreatedBy            uint64                `json:"created_by"`
	IsActive             bool                  `json:"is_active"`
	Assignments          []RouteAssignmentItem `json:"assignments,omitempty"`
	CreatedAt            time.Time             `json:"created_at"`
	UpdatedAt            time.Time             `json:"updated_at"`
}

// RouteAssignmentItem mirrors the route_assignments table (012).
type RouteAssignmentItem struct {
	ID              uint64     `json:"id"`
	RouteID         uint64     `json:"route_id"`
	VehicleID       uint64     `json:"vehicle_id"`
	DriverUserID    *uint64    `json:"driver_user_id,omitempty"`
	Status          string     `json:"status"` // not_started|in_progress|completed|delayed
	StartedAt       *time.Time `json:"started_at,omitempty"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	DeviationMeters float64    `json:"deviation_meters"`
	IMEI            string     `json:"imei,omitempty"`
}

// RouteCreateRequest is the POST /api/v1/routes body.
type RouteCreateRequest struct {
	Name                 string          `json:"name"`
	Waypoints            []RouteWaypoint `json:"waypoints"`
	EstimatedDurationSec *int            `json:"estimated_duration_sec,omitempty"`
	VehicleIDs           []uint64        `json:"vehicle_ids,omitempty"` // create assignment per vehicle
	DriverUserID         *uint64         `json:"driver_user_id,omitempty"`
}

// ---------------------------------------------------------------------------
// NATS / telemetry messages
// ---------------------------------------------------------------------------

// TelemetryMessage mirrors the payload published by ingestion-tcp
// (subject telemetry.raw.<IMEI>) 1:1 — including ACC (ignition), fuel sensor
// (B5a), GSM signal, mileage, alarm code and fix flag. CompanyCode & VehicleID
// are resolved from master.vehicle_imei_map by the ingestion layer (tenant
// resolution). Field set must stay aligned with
// backend/services/ingestion-tcp/models/models.go.
type TelemetryMessage struct {
	IMEI        string  `json:"imei"`
	CompanyCode string  `json:"company_code,omitempty"`
	VehicleID   int64   `json:"vehicle_id,omitempty"`
	Lat         float64 `json:"lat"`
	Lon         float64 `json:"lon"`
	Speed       float64 `json:"speed"`
	Heading     int16   `json:"heading"`
	Satellites  uint8   `json:"satellites"`
	HDOP        float64 `json:"hdop,omitempty"`
	Battery     uint8   `json:"battery_level,omitempty"`
	GsmSignal   uint8   `json:"gsm_signal,omitempty"`
	ACC         bool    `json:"acc,omitempty"`
	Mileage     uint32  `json:"mileage,omitempty"`
	AlarmCode   uint8   `json:"alarm_code,omitempty"`
	Fix         bool    `json:"fix,omitempty"`
	Timestamp   int64   `json:"timestamp"`

	// --- B5a: Fuel sensor (PRD v1.3.0 Module 7) ---
	// Pointer + omitempty: field yang tidak hadir (absen ≠ nol) tidak muncul di JSON.
	FuelLevel  *float64 `json:"fuel_level,omitempty"`
	FuelVolume *float64 `json:"fuel_volume,omitempty"`
	FuelTempC  *float64 `json:"fuel_temp_c,omitempty"`
}

// VehicleUpdateEvent is the FR-5.2 VEHICLE_UPDATE payload pushed to clients.
type VehicleUpdateEvent struct {
	Event string            `json:"event"`
	Data  VehicleUpdateData `json:"data"`
}

// VehicleUpdateData is the payload body of a VEHICLE_UPDATE event.
// Acc reflects the REAL ignition state sent by the tracker (GT06 status byte /
// Teltonika IO 239/240) — NOT inferred from Speed (hotfix ACC status WebSocket).
// FuelLevel/FuelVolume/FuelTempC (B5a) and Satellites/GsmSignal enrich the
// real-time contract so the dashboard can monitor fuel & signal quality live.
type VehicleUpdateData struct {
	VehicleID   uint64   `json:"vehicle_id"`
	IMEI        string   `json:"imei"`
	CompanyCode string   `json:"company_code"`
	PlateNumber string   `json:"plate_number"`
	DeviceModel string   `json:"device_model,omitempty"`
	Lat         float64  `json:"lat"`
	Lon         float64  `json:"lon"`
	Speed       float64  `json:"speed"`
	Heading     int16    `json:"heading"`
	Acc         bool     `json:"acc"`
	Status      string   `json:"status"`
	Battery     uint8    `json:"battery,omitempty"`
	Satellites  uint8    `json:"satellites,omitempty"`
	GsmSignal   uint8    `json:"gsm_signal,omitempty"`
	FuelLevel   *float64 `json:"fuel_level,omitempty"`
	FuelVolume  *float64 `json:"fuel_volume,omitempty"`
	FuelTempC   *float64 `json:"fuel_temp_c,omitempty"`
	Timestamp   string   `json:"timestamp"`
}

// RedisState mirrors the JSON stored under Redis key
// adatrack_gps:{company_code}:vehicle:state:<IMEI> by worker-live.
// FuelLevel/FuelTempC/Acc (B5a + hotfix ACC) are persisted by worker-live and
// surfaced by the REST vehicle list (enrichVehicles).
type RedisState struct {
	IMEI        string  `json:"imei"`
	CompanyCode string  `json:"company_code"`
	Lat         float64 `json:"lat"`
	Lon         float64 `json:"lon"`
	Speed       float64 `json:"speed"`
	Heading     int16   `json:"heading"`
	Status      string  `json:"status"` // ONLINE / IDLE / OFFLINE
	LastSeen    int64   `json:"last_seen"`

	// --- B5a + hotfix ACC ---
	FuelLevel *float64 `json:"fuel_level,omitempty"`
	FuelTempC *float64 `json:"fuel_temp_c,omitempty"`
	Acc       *bool    `json:"acc,omitempty"`
}

// ---------------------------------------------------------------------------
// WebSocket control messages
// ---------------------------------------------------------------------------

// SubscriptionMessage is an optional client control message.
type SubscriptionMessage struct {
	Event string `json:"event"`
	Data  struct {
		VehicleID uint64 `json:"vehicle_id"`
	} `json:"data"`
}

// WsErrorEvent is an error event sent to WS clients.
type WsErrorEvent struct {
	Event     string `json:"event"`
	ErrorCode string `json:"error_code"`
	Message   string `json:"message"`
}

// ConnectionEvent conveys connection status to a client.
type ConnectionEvent struct {
	Event string      `json:"event"`
	Data  interface{} `json:"data"`
}

// ---------------------------------------------------------------------------
// Alert notification events (B3 notifikasi → WS)
// ---------------------------------------------------------------------------

// AlertNotification mirrors the worker-alert notify payload. CompanyCode is
// required for cross-tenant-safe fan-out by the hub.
type AlertNotification struct {
	AlertID       string  `json:"alert_id"`
	VehicleID     uint64  `json:"vehicle_id"`
	IMEI          string  `json:"imei"`
	CompanyCode   string  `json:"company_code"`
	AlertType     string  `json:"alert_type"`
	Severity      string  `json:"severity"`
	Status        string  `json:"status"` // OPEN / ACKNOWLEDGED / RESOLVED
	Message       string  `json:"message,omitempty"`
	Lat           float64 `json:"lat"`
	Lon           float64 `json:"lon"`
	Speed         float64 `json:"speed,omitempty"`
	TriggeredAt   int64   `json:"triggered_at"`
	GeofenceID    uint64  `json:"geofence_id,omitempty"`
	GeofenceName  string  `json:"geofence_name,omitempty"`
	SpeedLimit    float64 `json:"speed_limit,omitempty"`
	SpeedObserved float64 `json:"speed_observed,omitempty"`
}

// AlertNotificationEvent is the GAP #1 WS event broadcast to clients.
type AlertNotificationEvent struct {
	Event string            `json:"event"` // "ALERT_NOTIFICATION"
	Data  AlertNotification `json:"data"`
}

// ---------------------------------------------------------------------------
// Media event notifications (B5b, Module 8 → WS MEDIA_EVENT, FR-8.5)
// ---------------------------------------------------------------------------

// MediaEventWS is the payload of a MEDIA_EVENT WS event; it mirrors the NATS
// media.event.<company> message published by service-media.
type MediaEventWS struct {
	Event string         `json:"event"` // "MEDIA_EVENT"
	Data  MediaEventData `json:"data"`
}

// MediaEventData carries the media catalog fields fanned out via the hub.
type MediaEventData struct {
	MediaID     uint64 `json:"media_id"`
	VehicleID   uint64 `json:"vehicle_id"`
	IMEI        string `json:"imei"`
	CompanyCode string `json:"company_code"`
	MediaType   string `json:"media_type"`
	TriggerType string `json:"trigger_type"`
	Status      string `json:"status"`
	SizeBytes   int64  `json:"size_bytes"`
	TakenAt     int64  `json:"taken_at"`
	PublishedAt int64  `json:"published_at"`
}
