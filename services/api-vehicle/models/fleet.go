package models

// Route is one route (company `tm_routes`, PRD §5.9.2).
type Route struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	Description *string    `json:"description,omitempty"`
	Waypoints   []Waypoint `json:"waypoints"`
	EstMinutes  *int       `json:"estimated_duration_min,omitempty"`
	CreatedAt   string     `json:"created_at"`
	UpdatedAt   string     `json:"updated_at"`
	DeletedAt   *string    `json:"deleted_at,omitempty"`
}

// Waypoint is one ordered stop ([lat, lon] + optional name).
type Waypoint struct {
	Seq  int     `json:"seq"`
	Lat  float64 `json:"lat"`
	Lon  float64 `json:"lon"`
	Name string  `json:"name,omitempty"`
}

// UpsertRouteRequest is the POST/PATCH route body.
type UpsertRouteRequest struct {
	Name        string     `json:"name" binding:"required,min=2,max=100"`
	Description *string    `json:"description" binding:"omitempty,max=255"`
	Waypoints   []Waypoint `json:"waypoints" binding:"required,min=2,dive"`
	EstMinutes  *int       `json:"estimated_duration_min" binding:"omitempty,min=1,max=1000000"`
}

// RouteAssignment is one th_route_assignments row.
type RouteAssignment struct {
	ID           int64   `json:"id"`
	RouteID      int64   `json:"route_id"`
	VehicleID    int64   `json:"vehicle_id"`
	DriverUserID *int64  `json:"driver_user_id,omitempty"`
	Status       string  `json:"status"`
	DeviationM   float64 `json:"deviation_meters"`
	StartedAt    *string `json:"started_at,omitempty"`
	CompletedAt  *string `json:"completed_at,omitempty"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
	DeletedAt    *string `json:"deleted_at,omitempty"`
}

// Assignment status machine (PRD §5.9.2: not_started → in_progress →
// completed | delayed).
const (
	AssignNotStarted = "not_started"
	AssignInProgress = "in_progress"
	AssignCompleted  = "completed"
	AssignDelayed    = "delayed"
)

// AllowedAssignmentTransitions is the manual status machine enforced by the API.
var AllowedAssignmentTransitions = map[string][]string{
	AssignNotStarted: {AssignInProgress, AssignDelayed},
	AssignInProgress: {AssignCompleted, AssignDelayed},
	AssignDelayed:    {AssignInProgress, AssignCompleted},
	AssignCompleted:  {},
}

// CreateAssignmentRequest is the POST assignment body.
type CreateAssignmentRequest struct {
	VehicleID    int64  `json:"vehicle_id" binding:"required,min=1"`
	DriverUserID *int64 `json:"driver_user_id" binding:"omitempty,min=1"`
}

// PatchAssignmentRequest is the PATCH assignment body (manual status transition).
type PatchAssignmentRequest struct {
	Status string `json:"status" binding:"required,oneof=not_started in_progress completed delayed"`
}

// SpeedConfig is one tm_speed_configs row (vehicle_id null = global default).
type SpeedConfig struct {
	ID          int64   `json:"id"`
	VehicleID   *int64  `json:"vehicle_id,omitempty"`
	MaxSpeedKMH int     `json:"max_speed_kmh"`
	GracePct    int     `json:"grace_margin_percent"`
	Severity    string  `json:"alert_severity"`
	Enabled     bool    `json:"enabled"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
	DeletedAt   *string `json:"deleted_at,omitempty"`
}

// UpsertSpeedConfigRequest is the POST/PATCH speed config body.
type UpsertSpeedConfigRequest struct {
	VehicleID   *int64 `json:"vehicle_id" binding:"omitempty,min=1"`
	MaxSpeedKMH int    `json:"max_speed_kmh" binding:"required,min=5,max=300"`
	GracePct    int    `json:"grace_margin_percent" binding:"min=0,max=100"`
	Severity    string `json:"alert_severity" binding:"omitempty,oneof=low medium high critical"`
	Enabled     *bool  `json:"enabled,omitempty"`
}

// FuelConfig is one tm_fuel_configs row (B5a, PRD Module 7 / FR-7.6):
// vehicle_id null = tenant-wide default; a vehicle row wins over the global one.
type FuelConfig struct {
	ID                 int64   `json:"id"`
	VehicleID          *int64  `json:"vehicle_id,omitempty"`
	DropThresholdPct   int     `json:"drop_threshold_percent"`
	RefuelThresholdPct int     `json:"refuel_threshold_percent"`
	WindowSeconds      int     `json:"window_seconds"`
	Severity           string  `json:"alert_severity"`
	RequireACC         bool    `json:"require_acc"`
	ACCStaleSeconds    int     `json:"acc_stale_seconds"`
	Enabled            bool    `json:"enabled"`
	CreatedAt          string  `json:"created_at"`
	UpdatedAt          string  `json:"updated_at"`
	DeletedAt          *string `json:"deleted_at,omitempty"`
}

// UpsertFuelConfigRequest is the POST/PATCH fuel config body.
type UpsertFuelConfigRequest struct {
	VehicleID          *int64 `json:"vehicle_id" binding:"omitempty,min=1"`
	DropThresholdPct   int    `json:"drop_threshold_percent" binding:"required,min=1,max=100"`
	RefuelThresholdPct int    `json:"refuel_threshold_percent" binding:"required,min=1,max=100"`
	WindowSeconds      int    `json:"window_seconds" binding:"omitempty,min=1,max=86400"`
	Severity           string `json:"alert_severity" binding:"omitempty,oneof=low medium high critical"`
	RequireACC         *bool  `json:"require_acc,omitempty"`
	ACCStaleSeconds    int    `json:"acc_stale_seconds" binding:"omitempty,min=1,max=86400"`
	Enabled            *bool  `json:"enabled,omitempty"`
}

// FuelLog is one td_fuel_logs row of the vehicle fuel history (FR-7.7).
type FuelLog struct {
	Timestamp  string   `json:"timestamp"`
	FuelLevel  *float64 `json:"fuel_level,omitempty"`
	FuelVolume *float64 `json:"fuel_volume,omitempty"`
	FuelTempC  *float64 `json:"fuel_temp_c,omitempty"`
	Lat        *float64 `json:"latitude,omitempty"`
	Lon        *float64 `json:"longitude,omitempty"`
	ACC        *bool    `json:"acc,omitempty"`
}

// Alert is one th_alerts row (read model + life-cycle transitions).
type Alert struct {
	ID          int64          `json:"id"`
	Type        string         `json:"type"`
	Severity    string         `json:"severity"`
	VehicleID   int64          `json:"vehicle_id"`
	IMEI        string         `json:"imei"`
	Lat         *float64       `json:"lat,omitempty"`
	Lon         *float64       `json:"lon,omitempty"`
	Speed       *float64       `json:"speed,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	Status      string         `json:"status"`
	AckedBy     *int64         `json:"acknowledged_by,omitempty"`
	AckedAt     *string        `json:"acknowledged_at,omitempty"`
	ResolvedAt  *string        `json:"resolved_at,omitempty"`
	SOSTTA      *int           `json:"sos_time_to_acknowledge_seconds,omitempty"`
	Escalations int            `json:"escalation_count"`
	DetectedAt  string         `json:"detected_at"`
	CreatedAt   string         `json:"created_at"`
	UpdatedAt   string         `json:"updated_at"`
}
