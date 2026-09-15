// Package models holds the live-state shapes for worker-live (PRD Module 2).
package models

import "time"

// TelemetryMessage mirrors the ingestion payload published on
// `telemetry.raw.<IMEI>` (only the fields the live state needs are decoded).
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
	Fix         bool    `json:"fix"`
	Timestamp   int64   `json:"timestamp"`

	FuelLevel  *float64 `json:"fuel_level,omitempty"`
	FuelVolume *float64 `json:"fuel_volume,omitempty"`
	FuelTempC  *float64 `json:"fuel_temp_c,omitempty"`
}

// LiveState is the Redis value stored at
// `adatrack_gps:{company_code}:vehicle:state:{IMEI}` (FR-2.1).
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
	ACC         *bool   `json:"acc,omitempty"`
	Mileage     uint32  `json:"mileage,omitempty"`
	Fix         bool    `json:"fix"`
	Status      string  `json:"status"`
	// LastSeen is the server receive time (UTC epoch seconds) used for the
	// ONLINE/IDLE/OFFLINE state machine (FR-2.2). Timestamp is the device time.
	LastSeen   int64    `json:"last_seen"`
	Timestamp  int64    `json:"timestamp"`
	FuelLevel  *float64 `json:"fuel_level,omitempty"`
	FuelVolume *float64 `json:"fuel_volume,omitempty"`
	FuelTempC  *float64 `json:"fuel_temp_c,omitempty"`
}

// Status values of the connection state machine (FR-2.2).
const (
	StatusOnline  = "ONLINE"  // last message < 90 s
	StatusIdle    = "IDLE"    // 90 s – 3 min
	StatusOffline = "OFFLINE" // > 3 min without a message
)

// Defaults for the live-state writer.
const (
	// FlushInterval is the batch cadence (FR-2.3: 100 ms → 100× fewer Redis ops).
	FlushInterval = 100 * time.Millisecond
	// StateTTL is the live-state key TTL (FR-2.1: 5 minutes).
	StateTTL = 5 * time.Minute
	// MaxBuffer bounds the pending buffer (FR-4.4 bounded buffer).
	MaxBuffer = 5000
	// OfflineAfter is the OFFLINE threshold (FR-2.2).
	OfflineAfter = 3 * time.Minute
	// IdleAfter is the ONLINE → IDLE threshold (FR-2.2).
	IdleAfter = 90 * time.Second
)
