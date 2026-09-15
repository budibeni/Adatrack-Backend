// Package models holds the persistence row shape for worker-persistence
// (PRD Module 3 / FR-3.1..FR-3.4).
package models

import "time"

// TelemetryMessage mirrors the ingestion payload on `telemetry.raw.<IMEI>`.
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

// Row is one prepared `th_telemetry_logs` insert.
type Row struct {
	IMEI        string
	CompanyCode string
	VehicleID   int64
	Lat         float64
	Lon         float64
	Speed       float64
	Heading     float64
	Altitude    float64
	ACC         bool
	Battery     int
	Timestamp   time.Time
}

// Positionless reports whether the message carries no usable position (fuel-only
// or heartbeat-only packets): those are routed to their own paths instead of
// `th_telemetry_logs` (FR-3.4).
func Positionless(t TelemetryMessage) bool {
	return t.Lat == 0 && t.Lon == 0 && t.Speed == 0
}

// ToRow converts a message into an insertable row (UTC timestamp).
func ToRow(t TelemetryMessage) Row {
	ts := t.Timestamp
	if ts <= 0 {
		ts = time.Now().Unix()
	}
	return Row{
		IMEI:        t.IMEI,
		CompanyCode: t.CompanyCode,
		VehicleID:   t.VehicleID,
		Lat:         t.Lat,
		Lon:         t.Lon,
		Speed:       t.Speed,
		Heading:     float64(t.Heading),
		Altitude:    float64(t.Altitude),
		ACC:         t.ACC,
		Battery:     int(t.Battery),
		Timestamp:   time.Unix(ts, 0).UTC(),
	}
}

// TableName is the partition-per-month telemetry table (PRD §6.2, `th_` prefix).
const TableName = "th_telemetry_logs"

// InsertColumns are the columns written for every batch (order matters for the
// parameter binding in internal.BatchInsert).
var InsertColumns = []string{
	"vehicle_id", "imei", "company_code", "latitude", "longitude",
	"speed", "heading", "altitude", "acc_status", "battery_level", "timestamp",
}

// Values renders a row as the parameter slice matching InsertColumns.
func (r Row) Values() []any {
	acc := 0
	if r.ACC {
		acc = 1
	}
	return []any{
		r.VehicleID, r.IMEI, r.CompanyCode, r.Lat, r.Lon,
		r.Speed, r.Heading, r.Altitude, acc, r.Battery, r.Timestamp,
	}
}
