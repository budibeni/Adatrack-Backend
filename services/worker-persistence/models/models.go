package models

import "time"

// TelemetryMessage is the canonical telemetry payload published by ingestion-tcp.
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
	// Timestamp is the device event time (unix seconds).
	Timestamp int64 `json:"timestamp"`

	// --- B5a: Fuel sensor (PRD v1.3.0 Module 7) ---
	FuelLevel  *float64 `json:"fuel_level,omitempty"`
	FuelVolume *float64 `json:"fuel_volume,omitempty"`
	FuelTempC  *float64 `json:"fuel_temp_c,omitempty"`
}

// TelemetryRow is a single row queued for the batch insert. CompanyCode is the
// routing key: rows are grouped per company and inserted into the company DB
// adatrack_gps_{lowercase(company_code)}.telemetry_logs. Rows with IsFuelOnly
// (B5a fuel sensor reading without GPS fix) are routed to fuel_logs instead —
// they never enter telemetry_logs.
type TelemetryRow struct {
	IMEI        string
	CompanyCode string
	VehicleID   int64
	EventTS     time.Time
	Lat, Lon    float64
	Speed       float64
	Heading     int16
	Satellites  uint8
	HDOP        float64
	Battery     uint8
	ACC         bool
	// --- B5a: Fuel sensor fields ---
	FuelLevel  *float64
	FuelVolume *float64
	FuelTempC  *float64
	IsFuelOnly bool // true = partial message without GPS fix → fuel_logs
}

// Batch tuning constants for the persistence worker.
const (
	// MaxBatchSize flushes when this many records are buffered.
	MaxBatchSize = 500
	// FlushInterval triggers a flush when the buffer is non-empty.
	FlushInterval = 5 * time.Second
	// MaxRetries is the number of insert attempts after the first failure.
	MaxRetries = 3
	// BaseDelay is the starting exponential-backoff delay.
	BaseDelay = 1 * time.Second
	// BatchWait bounds the graceful-shutdown settle wait.
	BatchWait = 10 * time.Second
)
