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

// TelemetryMessageHasFuel reports whether the message carries a fuel reading of
// any kind (level, volume, or temperature). It is the canonical guard used by the
// worker loops so fuel-only telemetry is not silently ignored (FR-7.3/FR-7.6).
func (t TelemetryMessage) HasFuel() bool {
	return t.FuelLevel != nil || t.FuelVolume != nil || t.FuelTempC != nil
}

// FuelLevelPct converts a raw fuel reading into a percentage for the delta
// evaluation (FR-7.6), using the current tank calibration when available.
//   - calibrated tank (TankHeightCM > 0): returns the % of the current level
//     relative to the tank height (clamped into 0..100).
//   - uncalibrated: falls back to the observed fuel_level directly (still
//     0..100 when the field is populated) so threshold arithmetic stays
//     comparable across calibrated and uncalibrated readers.
func (t TelemetryMessage) FuelLevelPct(tankHeightCM float64) float64 {
	if t.FuelLevel != nil && *t.FuelLevel > 0 {
		lv := *t.FuelLevel
		if tankHeightCM > 0 {
			pct := (lv / tankHeightCM) * 100.0
			if pct < 0 {
				return 0
			}
			if pct > 100 {
				return 100
			}
			return pct
		}
		return lv
	}
	// No level: fall back to volume if present, otherwise 0.
	if t.FuelVolume != nil && *t.FuelVolume > 0 {
		return *t.FuelVolume // keep the raw proxy (absent ≠ zero still applies elsewhere)
	}
	return 0
}

// SeverityOf normalises the fuel-drop severity config to one of the four canonical
// severities (PRD §5.9). Unknown values fall back to `critical`, which is also the
// documented default severity for FUEL_DROP (FR-7.6).
func SeverityOf(raw string) string {
	switch raw {
	case SeverityLow, SeverityMedium, SeverityHigh, SeverityCritical:
		return raw
	default:
		return SeverityCritical
	}
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

// FuelConfig reflects one tm_fuel_configs row (PRD Module 7, B5a / migration
// 013): vehicle_id 0 = tenant-wide default, non-zero = per-vehicle override
// (the vehicle row wins, same precedence rule as tm_speed_configs).
type FuelConfig struct {
	ID       int64
	VehicleID int64 // 0 = tenant-wide default
	// DropThresholdPct / RefuelThresholdPct are percentages of the observed
	// level; WindowSeconds is the sliding window of the delta evaluation.
	DropThresholdPct   int
	RefuelThresholdPct int
	WindowSeconds      int
	// Severity applies to FUEL_DROP (REFUEL stays `low`, PRD FR-7.6).
	Severity string
	// RequireACC enables the strict literal ACC gate; ACCStaleSeconds is the
	// staleness window of that gate (2026-08-26 decision, FR-7.6).
	RequireACC      bool
	ACCStaleSeconds int
	Enabled         bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
	DeletedAt       *time.Time
}

// EffectiveFuelConfig returns the config that applies to a vehicle: the
// vehicle-specific row wins over the tenant-wide default; disabled rows are
// skipped (nil when none applies — the caller then falls back to the global
// env defaults in Config.Fuel, FR-7.6).
func EffectiveFuelConfig(configs []FuelConfig, vehicleID int64) *FuelConfig {
	var global *FuelConfig
	for i := range configs {
		c := configs[i]
		if !c.Enabled {
			continue
		}
		if c.VehicleID == vehicleID && vehicleID != 0 {
			return &configs[i]
		}
		if c.VehicleID == 0 && global == nil {
			global = &configs[i]
		}
	}
	return global
}

// FuelLogEntry is one th_fuel_logs row read model (B5a): the persisted fuel-only
// snapshot the history endpoint returns, plus the live-state fuel fields that
// worker-live merges on partial fuel-only messages (FR-7.5).
type FuelLogEntry struct {
	IMEI        string   `json:"imei"`
	CompanyCode string   `json:"company_code"`
	VehicleID   int64    `json:"vehicle_id"`
	EngineHours float64  `json:"engine_hours"`
	TankHeightCM float64  `json:"tank_height_cm"`
	TankCapacityCM uint16 `json:"tank_capacity_cm"`
	FilledLevel   uint16   `json:"filled_level"`
	FuelTempC   *float64  `json:"fuel_temp_c,omitempty"`
	FuelLevel    *float64  `json:"fuel_level,omitempty"`
	FuelVolume   *float64  `json:"fuel_volume,omitempty"`
	Timestamp   string    `json:"timestamp"`
}

// NotificationRow is one td_notifications delivery-audit row (PRD §5.9.8). It is
// returned by the notification pipeline so callers can replay failures/SKIPs
// (stored in batch via InsertNotifications).
type NotificationRow struct {
	AlertID      int64
	UserID       int64
	Channel      string
	Status       string
	Reason       string
	ResponseJSON []byte // optional provider-specific response, stored as jsonb
}
