package controllers

import "adatrack_gps/ingestion-tcp/models"

// ApplyFuelCalibration derives the standard FR-7.3 payload fields from a raw GT06
// 0x0D sensor height when the tank is calibrated (`FUEL_TANK_HEIGHT_CM > 0`):
//
//   - fuel_volume = the raw height in centimetres (the sensor's own scale; a
//     litres conversion stays out of core per FR-7.8, but the value must travel
//     with the payload so consumer-side thresholds have a comparable number),
//   - fuel_level  = height / tank height × 100, clamped into 0..100.
//
// Uncalibrated (0) or absent height leaves every fuel field untouched — the
// documented "absent ≠ zero" contract (FR-7.3): the raw `fuel_height_cm` is still
// published for consumers that do their own calibration.
func ApplyFuelCalibration(t *models.TelemetryMessage, tankHeightCM float64) {
	if t == nil || t.FuelHeightCM == nil || tankHeightCM <= 0 {
		return
	}
	height := *t.FuelHeightCM
	volume := height
	t.FuelVolume = &volume

	level := height / tankHeightCM * 100
	switch {
	case level < 0:
		level = 0
	case level > 100:
		level = 100
	}
	t.FuelLevel = &level
}
