// Package models holds worker-alert data types (Phase B3, multi-tenant).
package models

import "time"

// TelemetryMessage mirrors the payload published by ingestion-tcp.
// Multi-tenant (PRD §6): ingestion enriches every message with company_code +
// vehicle_id (FR-1.4) so consumers can route to adatrack_gps_{company_code}.
type TelemetryMessage struct {
	IMEI        string  `json:"imei"`
	CompanyCode string  `json:"company_code,omitempty"`
	VehicleID   int64   `json:"vehicle_id,omitempty"`
	Lat         float64 `json:"lat"`
	Lon         float64 `json:"lon"`
	Speed       float64 `json:"speed"`
	Heading     int16   `json:"heading,omitempty"`
	Satellites  uint8   `json:"satellites,omitempty"`
	HDOP        float64 `json:"hdop,omitempty"`
	Battery     uint8   `json:"battery_level,omitempty"`
	Acc         *bool   `json:"acc,omitempty"`
	Timestamp   int64   `json:"timestamp"`
	Raw         string  `json:"raw,omitempty"`
}

// Alert types as stored in alerts.alert_type (company migration 008).
const (
	AlertTypeGeofence  = "GEOFENCE_BREACH"
	AlertTypeSpeed     = "OVERSPEEDING"
	AlertTypeOffline   = "OFFLINE"
	AlertTypeBattery   = "BATTERY_LOW"
	AlertTypeSOS       = "SOS"
	AlertTypeRouteDev  = "ROUTE_DEVIATION"
)

// Alert lifecycle status values (alerts.status enum, lowercase).
const (
	AlertStatusOpen         = "open"
	AlertStatusAcknowledged = "acknowledged"
	AlertStatusResolved     = "resolved"
)

// AlertRecord is one row of the company DB alerts table (migration 008).
type AlertRecord struct {
	ID          uint64
	VehicleID   uint64
	AlertType   string  // GEOFENCE_BREACH | OVERSPEEDING | OFFLINE | BATTERY_LOW | SOS | ROUTE_DEVIATION
	Severity    string  // low | medium | high | critical
	Description string
	Status      string
	VehicleLat  *float64
	VehicleLon  *float64
	CreatedAt   time.Time
}

// GeofenceDef is one active geofence loaded from the company DB (migration 005).
type GeofenceDef struct {
	ID            uint64
	Name          string
	AreaType      string // "circle" | "polygon"
	CenterLat     float64 // circle only (from GeoJSON Point)
	CenterLon     float64
	RadiusMeters  float64 // circle only
	Boundary      [][2]float64 // polygon only: [{lat,lon},...] from boundary_points or GeoJSON ring
}

// GeofenceState tracks per-zone inside/outside state for a vehicle (Redis
// hash {prefix}{company}:geofence_state:{imei}, field = geofence id).
type GeofenceState struct {
	Inside    map[uint64]bool
	LastCheck time.Time
}

// SpeedConfig is one row of speed_configs (company migration 007).
type SpeedConfig struct {
	VehicleID     *int64  // nil = global default
	SpeedLimitKMH float64
	GraceMargin   float64
}

// RouteAssignment is one active route_assignment joined with routes + vehicles
// (company migrations 011 + 012).
type RouteAssignment struct {
	AssignmentID  uint64
	RouteID       uint64
	RouteName     string
	VehicleID     uint64
	IMEI          string
	DriverUserID  uint64
	Status        string // not_started | in_progress | completed | delayed
	StartedAt     *time.Time
	Waypoints     []Waypoint
	EstimatedSec  *int
}

// Waypoint is one point of a planned route.
type Waypoint struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// NotifPreference is one enabled notification_preferences row (migration 009).
type NotifPreference struct {
	UserID       uint64
	AlertType    string // 'geofence','speed','sos','offline','battery','route_deviation','all'
	Channel      string // websocket | email | sms | push
	MinSeverity  string // info | warning | critical
	DeliveryConf []byte // JSON: {"email": "...", "phone_number": "+62..."}
}

// OpenSOSAlert is an unacknowledged SOS alert due for automatic escalation.
type OpenSOSAlert struct {
	ID            uint64
	VehicleID     uint64
	CreatedAt     time.Time
	AcknowledgedAt *time.Time
}

