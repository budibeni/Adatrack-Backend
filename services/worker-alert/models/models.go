// Package models holds the shapes of the worker-alert engine (PRD Module 6,
// phase B3): the telemetry payload, alert rows, recipients and configuration
// rows consumed from the tenant schemas.
package models

import "time"

// Alert types (th_alerts.type, PRD §6.3).
const (
	AlertGeofenceBreach = "geofence_breach"
	AlertOverspeeding   = "overspeeding"
	AlertBatteryLow     = "battery_low"
	AlertOffline        = "offline"
	AlertSOS            = "sos"
	AlertRouteDeviation = "route_deviation"
	AlertFuelDrop       = "fuel_drop" // B5a
	AlertRefuel         = "refuel"    // B5a
)

// Severities (PRD §5.9). rank orders them for min_severity filtering.
const (
	SeverityLow      = "low"
	SeverityMedium   = "medium"
	SeverityHigh     = "high"
	SeverityCritical = "critical"
)

// SeverityRank maps a severity to its order (low < medium < high < critical).
var SeverityRank = map[string]int{
	SeverityLow:      0,
	SeverityMedium:   1,
	SeverityHigh:     2,
	SeverityCritical: 3,
}

// AtLeastSeverity reports whether `have` meets or exceeds `min`.
func AtLeastSeverity(have, min string) bool {
	return SeverityRank[have] >= SeverityRank[min]
}

// Alert life-cycle statuses.
const (
	StatusOpen     = "open"
	StatusAcked    = "acknowledged"
	StatusResolved = "resolved"
)

// Notification channels (td_notifications.channel).
const (
	ChannelWebsocket = "websocket"
	ChannelEmail     = "email"
	ChannelSMS       = "sms"
	ChannelPush      = "push"
)

// Notification delivery statuses (td_notifications.status).
const (
	NotifyPending   = "pending"
	NotifySent      = "sent"
	NotifyDelivered = "delivered"
	NotifyFailed    = "failed"
	NotifySkipped   = "skipped"
)

// TelemetryMessage mirrors the ingestion payload published on
// `telemetry.raw.<IMEI>` (same JSON contract as worker-live, plus the alarm
// markers the SOS detector consumes).
type TelemetryMessage struct {
	IMEI        string  `json:"imei"`
	CompanyCode string  `json:"company_code"`
	VehicleID   int64   `json:"vehicle_id"`
	Lat         float64 `json:"lat"`
	Lon         float64 `json:"lon"`
	Speed       float64 `json:"speed"`
	Heading     int16   `json:"heading"`
	Satellites  uint8   `json:"satellites"`
	Altitude    int16   `json:"altitude"`
	Battery     uint8   `json:"battery_level"`
	GsmSignal   uint8   `json:"gsm_signal"`
	ACC         bool    `json:"acc"`
	Mileage     uint32  `json:"mileage"`
	AlarmCode   uint8   `json:"alarm_code,omitempty"`
	AlarmLBS    bool    `json:"alarm_lbs,omitempty"`
	Fix         bool    `json:"fix"`
	Timestamp   int64   `json:"timestamp"`

	FuelLevel  *float64 `json:"fuel_level,omitempty"`
	FuelVolume *float64 `json:"fuel_volume,omitempty"`
	FuelTempC  *float64 `json:"fuel_temp_c,omitempty"`
}

// Alert is one th_alerts row flowing through the engine.
type Alert struct {
	ID              int64          `json:"id"`
	Type            string         `json:"type"`
	Severity        string         `json:"severity"`
	VehicleID       int64          `json:"vehicle_id"`
	IMEI            string         `json:"imei"`
	CompanyCode     string         `json:"company_code"`
	Lat             float64        `json:"lat,omitempty"`
	Lon             float64        `json:"lon,omitempty"`
	Speed           float64        `json:"speed,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	Status          string         `json:"status"`
	DedupKey        string         `json:"-"`
	DetectedAt      time.Time      `json:"detected_at"`
	EscalationCount int            `json:"escalation_count,omitempty"`
}

// Recipient is one notification target (master tm_users row, PRD §5.9.8).
type Recipient struct {
	UserID   int64
	Email    string
	FullName string
}

// PrefRow is one tm_notification_preferences row.
type PrefRow struct {
	UserID      int64
	AlertType   string // 'all' or a concrete type
	Channel     string
	Enabled     bool
	MinSeverity string
}

// SpeedConfig is one tm_speed_configs row (vehicle_id 0 = global default).
type SpeedConfig struct {
	ID        int64
	VehicleID int64 // 0 = global
	MaxSpeed  int
	GracePct  int
	Severity  string
	Enabled   bool
}

// Geofence is one tm_geofences row with its enabled vehicle mapping.
type Geofence struct {
	ID         int64
	Name       string
	AreaType   string // circle | polygon
	CenterLat  float64
	CenterLon  float64
	RadiusM    int
	Boundary   [][2]float64 // polygon vertices [lat, lon]
	Severity   string
	OnEntry    bool
	OnExit     bool
	VehicleIDs map[int64]bool // enabled mappings only
}

// Waypoint is one route waypoint ([lat, lon]).
type Waypoint struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// Assignment is one in-progress route assignment with its waypoints.
type Assignment struct {
	ID        int64
	VehicleID int64
	Waypoints []Waypoint
}

// NotificationRow is one td_notifications row written by the delivery pipeline.
type NotificationRow struct {
	AlertID  int64
	UserID   int64
	Channel  string
	Status   string
	Reason   string
	Response map[string]any
}
