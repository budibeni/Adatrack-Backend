// Package models holds the request/response shapes of api-vehicle (PRD §8.1
// envelope, §8.2 endpoints, §6.2 fleet management, phase B3).
package models

// Role names (master `tm_users.global_role`, PRD §3.1) — the same vocabulary as
// service-websocket so the JWT claims interoperate.
const (
	RoleSuperAdmin = "SuperAdmin"
	RoleAdmin      = "Admin"
	RoleManager    = "Manager"
	RoleOperator   = "Operator"
	RoleDriver     = "Driver"
)

// PlatformCompanyCode is the reserved platform tenant (PRD §3.1).
const PlatformCompanyCode = "DEFAULT"

// Pagination is the optional PRD §8.1 pagination block.
type Pagination struct {
	Page  int   `json:"page"`
	Limit int   `json:"limit"`
	Total int64 `json:"total"`
}

// Envelope is the generic PRD §8.1 success response.
type Envelope struct {
	Status     string      `json:"status"`
	Data       any         `json:"data,omitempty"`
	Pagination *Pagination `json:"pagination,omitempty"`
}

// ErrorEnvelope is the generic PRD §8.1 error response.
type ErrorEnvelope struct {
	Status    string            `json:"status"`
	ErrorCode string            `json:"error_code"`
	Message   string            `json:"message"`
	Timestamp string            `json:"timestamp"`
	Errors    map[string]string `json:"errors,omitempty"`
}

// Vehicle is the fleet master row (company `tm_vehicles`).
type Vehicle struct {
	ID           int64   `json:"id"`
	IMEI         string  `json:"imei"`
	PlateNumber  string  `json:"plate_number"`
	Make         *string `json:"make,omitempty"`
	Model        *string `json:"model,omitempty"`
	Year         *int    `json:"year_of_manufacture,omitempty"`
	Color        *string `json:"color,omitempty"`
	FuelType     *string `json:"fuel_type,omitempty"`
	CategoryCode *string `json:"vehicle_category_code,omitempty"`
	TypeCode     *string `json:"vehicle_type_code,omitempty"`
	DriverUserID *int64  `json:"driver_user_id,omitempty"`
	DriverName   *string `json:"driver_name,omitempty"`
	DeviceModel  *string `json:"device_model,omitempty"`
	// Protocol is the universal brand/protocol code (PRD Module 1c, B11): the
	// registry key that routes the device to its ingestion decoder. Nil means
	// "unknown" (the listener port remains the fallback discriminator).
	Protocol     *string  `json:"protocol,omitempty"`
	ProtocolPort *int     `json:"protocol_port,omitempty"`
	Brand        *string  `json:"brand,omitempty"`
	Status       string   `json:"status"`
	LastSeenAt   *string  `json:"last_seen_at,omitempty"`
	CurrentLat   *float64 `json:"current_lat,omitempty"`
	CurrentLon   *float64 `json:"current_lon,omitempty"`
	CurrentSpeed *float64 `json:"current_speed,omitempty"`
	CreatedAt    string   `json:"created_at"`
	UpdatedAt    string   `json:"updated_at"`
	DeletedAt    *string  `json:"deleted_at,omitempty"`
	// Live is the Redis live-state snapshot overlaid by the API (phase B6:
	// "REST enrich live-state: overlay fuel_level & acc dari Redis"). It is nil
	// when the vehicle has never reported telemetry or Redis is unavailable.
	Live *LiveState `json:"live,omitempty"`
}

// LiveState is the worker-live snapshot (FR-2.1) as consumed by the REST layer.
// It carries the freshest telemetry — including the fuel channel
// (fuel_level / fuel_volume / fuel_temp_c) and the REAL device ACC flag — which
// the `tm_vehicles` mirror can lag behind (PRD Module 7, phase B6).
//
// It deliberately mirrors the Redis JSON one-to-one so the WS `VEHICLE_UPDATE`
// payload and the REST `live` block stay interchangeable for the frontend.
type LiveState struct {
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
	// ACC is the real ignition line state (nil when the device never reported it).
	ACC     *bool  `json:"acc,omitempty"`
	Mileage uint32 `json:"mileage,omitempty"`
	Fix     bool   `json:"fix"`
	// Status is the CONNECTION state machine (ONLINE/IDLE/OFFLINE, FR-2.2) and
	// is intentionally distinct from Vehicle.Status, which is the fleet
	// life-cycle (active/inactive/maintenance).
	Status     string   `json:"status"`
	LastSeen   int64    `json:"last_seen"`
	Timestamp  int64    `json:"timestamp"`
	FuelLevel  *float64 `json:"fuel_level,omitempty"`
	FuelVolume *float64 `json:"fuel_volume,omitempty"`
	FuelTempC  *float64 `json:"fuel_temp_c,omitempty"`
}

// UpsertVehicleRequest is the POST/PATCH vehicle body (PRD §8.5 validation).
type UpsertVehicleRequest struct {
	IMEI         string  `json:"imei" binding:"required,min=5,max=30"`
	PlateNumber  string  `json:"plate_number" binding:"required,min=2,max=20"`
	Make         *string `json:"make" binding:"omitempty,max=50"`
	Model        *string `json:"model" binding:"omitempty,max=50"`
	Year         *int    `json:"year_of_manufacture" binding:"omitempty,min=1980,max=2100"`
	Color        *string `json:"color" binding:"omitempty,max=30"`
	FuelType     *string `json:"fuel_type" binding:"omitempty,oneof=petrol diesel electric hybrid gas"`
	CategoryCode *string `json:"vehicle_category_code" binding:"omitempty,max=20"`
	TypeCode     *string `json:"vehicle_type_code" binding:"omitempty,max=40"`
	DriverUserID *int64  `json:"driver_user_id" binding:"omitempty,min=1"`
	DriverName   *string `json:"driver_name" binding:"omitempty,max=100"`
	DeviceModel  *string `json:"device_model" binding:"omitempty,max=50"`
	// Protocol is validated against the universal registry (`internal/protocol`)
	// by the handler — an unknown brand is rejected 400 (PRD Module 1c).
	Protocol *string `json:"protocol" binding:"omitempty,max=40"`
	Brand    *string `json:"brand" binding:"omitempty,max=80"`
	Status   *string `json:"status" binding:"omitempty,oneof=active inactive maintenance"`
}

// Geofence is one zone (company `tm_geofences`, PRD §5.9.1).
type Geofence struct {
	ID          int64        `json:"id"`
	Name        string       `json:"name"`
	Description *string      `json:"description,omitempty"`
	AreaType    string       `json:"area_type"`
	CenterLat   *float64     `json:"center_lat,omitempty"`
	CenterLon   *float64     `json:"center_lon,omitempty"`
	RadiusM     *int         `json:"radius_meters,omitempty"`
	Boundary    [][2]float64 `json:"boundary_points,omitempty"`
	Severity    string       `json:"severity"`
	OnEntry     bool         `json:"on_entry"`
	OnExit      bool         `json:"on_exit"`
	Active      bool         `json:"active"`
	VehicleIDs  []int64      `json:"vehicle_ids,omitempty"`
	CreatedAt   string       `json:"created_at"`
	UpdatedAt   string       `json:"updated_at"`
	DeletedAt   *string      `json:"deleted_at,omitempty"`
}

// UpsertGeofenceRequest is the POST/PATCH geofence body.
type UpsertGeofenceRequest struct {
	Name        string       `json:"name" binding:"required,min=2,max=100"`
	Description *string      `json:"description" binding:"omitempty,max=255"`
	AreaType    string       `json:"area_type" binding:"required,oneof=circle polygon"`
	CenterLat   *float64     `json:"center_lat" binding:"omitempty,gte=-90,lte=90"`
	CenterLon   *float64     `json:"center_lon" binding:"omitempty,gte=-180,lte=180"`
	RadiusM     *int         `json:"radius_meters" binding:"omitempty,min=10,max=100000000"`
	Boundary    [][2]float64 `json:"boundary_points" binding:"omitempty"`
	Severity    string       `json:"severity" binding:"omitempty,oneof=low medium high critical"`
	OnEntry     *bool        `json:"on_entry,omitempty"`
	OnExit      *bool        `json:"on_exit,omitempty"`
	Active      *bool        `json:"active,omitempty"`
}
