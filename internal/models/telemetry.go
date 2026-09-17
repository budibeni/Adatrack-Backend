package models

import "time"

// TelemetryPayload represents the standardized JSON structure passing through NATS
type TelemetryPayload struct {
	IMEI        string    `json:"imei"`
	CompanyCode string    `json:"company_code"`
	VehicleID   int       `json:"vehicle_id"`
	Latitude    float64   `json:"lat"`
	Longitude   float64   `json:"lon"`
	Speed       float64   `json:"speed"`
	Heading     float64   `json:"heading"`
	Altitude    float64   `json:"altitude"`
	ACCStatus   int16     `json:"acc_status"` // 0: OFF, 1: ON
	Battery     float64   `json:"battery_level"`
	Satellites  int       `json:"satellites"`
	GSMSignal   int       `json:"gsm_signal"`
	Timestamp   time.Time `json:"timestamp"`
	RawData     string    `json:"raw_data,omitempty"` // Hex representation for debugging
	EventCode   int       `json:"event_code,omitempty"`
	Status      string    `json:"status,omitempty"` // ONLINE, IDLE, OFFLINE

	// Fuel sensor data
	FuelLevel  *float64 `json:"fuel_level,omitempty"`
	FuelVolume *float64 `json:"fuel_volume,omitempty"`
	FuelTempC  *float64 `json:"fuel_temp_c,omitempty"`
}
