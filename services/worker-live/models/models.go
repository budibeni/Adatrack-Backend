package models

import "time"

// TelemetryMessage mirrors the payload published by ingestion-tcp.
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
	Timestamp   int64   `json:"timestamp"`

	// --- B5a: Fuel sensor (PRD v1.3.0 Module 7) ---
	FuelLevel  *float64 `json:"fuel_level,omitempty"`
	FuelVolume *float64 `json:"fuel_volume,omitempty"`
	FuelTempC  *float64 `json:"fuel_temp_c,omitempty"`
}

// LiveState is stored under Redis key adatrack_gps:{company_code}:vehicle:state:<IMEI>
// (company_code lowercase; PRD §7 REDIS_KEY_PREFIX / Key Decision 7).
type LiveState struct {
	IMEI        string  `json:"imei"`
	CompanyCode string  `json:"company_code"`
	Lat         float64 `json:"lat"`
	Lon         float64 `json:"lon"`
	Speed       float64 `json:"speed"`
	Heading     int16   `json:"heading"`
	Status      string  `json:"status"`
	LastSeen    int64   `json:"last_seen"`

	// --- B5a: Fuel sensor (PRD v1.3.0 Module 7) ---
	FuelLevel *float64 `json:"fuel_level,omitempty"`
	FuelTempC *float64 `json:"fuel_temp_c,omitempty"`
}

// Batch / TTL tuning for the live state writer.
const (
	// MaxBuffer bounds the number of buffered states before a forced flush.
	MaxBuffer = 100
	// FlushInterval is the maximum time between Redis writes.
	FlushInterval = 100 * time.Millisecond
	// StateTTL is the Redis key TTL for vehicle:state:<IMEI> (PRD FR-2.2).
	StateTTL = 5 * time.Minute
	// OfflineAfter marks a device OFFLINE when no event for this long.
	OfflineAfter = 3 * time.Minute
)
