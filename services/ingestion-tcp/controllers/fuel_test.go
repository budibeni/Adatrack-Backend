package controllers

import (
	"testing"
	"time"

	"adatrack_gps/ingestion-tcp/models"
)

// fuelPayload builds a 0x94/0x0D information-transmission payload with the
// version-3.1 sensor sentence `!AIOIL,02,<height>,<temp>,519J,0200,027.140,0,00,9F`.
func fuelPayload(t *testing.T, when time.Time, height, temp string) []byte {
	t.Helper()
	payload := []byte{0x0D}
	payload = append(payload,
		byte(when.Year()-2000), byte(when.Month()), byte(when.Day()),
		byte(when.Hour()), byte(when.Minute()), byte(when.Second()))
	payload = append(payload, "!AIOIL,02,"+height+","+temp+",519J,0200,027.140,0,00,9F"...)
	payload = append(payload, 0x00, 0x01) // information serial number
	return payload
}

// TestApplyFuelCalibration covers FR-7.3/FR-7.8: a calibrated tank turns the raw
// sensor height into fuel_level (%) + fuel_volume, while an uncalibrated or
// height-less message keeps both absent (absent ≠ zero).
func TestApplyFuelCalibration(t *testing.T) {
	when := time.Now().UTC()

	msg, ok := ParseInfoTransmit(fuelPayload(t, when, "060.000", "025.400"))
	if !ok {
		t.Fatal("ParseInfoTransmit rejected the fuel payload")
	}
	if msg.FuelLevel != nil || msg.FuelVolume != nil {
		t.Fatalf("parser must not invent fuel_level/volume (got %v/%v)", msg.FuelLevel, msg.FuelVolume)
	}

	ApplyFuelCalibration(&msg, 100)
	if msg.FuelLevel == nil || *msg.FuelLevel != 60 {
		t.Errorf("fuel_level = %v, want 60 (60 cm of a 100 cm tank)", msg.FuelLevel)
	}
	if msg.FuelVolume == nil || *msg.FuelVolume != 60 {
		t.Errorf("fuel_volume = %v, want 60 (raw cm scale)", msg.FuelVolume)
	}
	if msg.FuelHeightCM == nil || *msg.FuelHeightCM != 60 {
		t.Errorf("fuel_height_cm = %v, want the raw 60 cm preserved", msg.FuelHeightCM)
	}

	// Uncalibrated (FUEL_TANK_HEIGHT_CM=0): the raw height is all we publish.
	raw, _ := ParseInfoTransmit(fuelPayload(t, when, "060.000", "025.400"))
	ApplyFuelCalibration(&raw, 0)
	if raw.FuelLevel != nil || raw.FuelVolume != nil {
		t.Errorf("uncalibrated message must keep fuel_level/volume absent, got %v/%v",
			raw.FuelLevel, raw.FuelVolume)
	}
}

// TestApplyFuelCalibrationClamps documents the 0..100 boundary handling.
func TestApplyFuelCalibrationClamps(t *testing.T) {
	tests := []struct {
		height, tank, want float64
	}{
		{height: 120, tank: 100, want: 100}, // above tank height → clamp
		{height: 75, tank: 150, want: 50},   // half full
		{height: 0, tank: 100, want: 0},     // empty tank is a real reading
	}
	for _, tc := range tests {
		msg := fuelMessage(tc.height)
		ApplyFuelCalibration(&msg, tc.tank)
		if msg.FuelLevel == nil || *msg.FuelLevel != tc.want {
			t.Errorf("height %.0f/%.0f → fuel_level %v, want %.0f", tc.height, tc.tank, msg.FuelLevel, tc.want)
		}
	}
	if msg := fuelMessage(10); true {
		ApplyFuelCalibration(&msg, -5) // negative tank height is ignored
		if msg.FuelLevel != nil {
			t.Errorf("negative tank height must not calibrate, got %v", *msg.FuelLevel)
		}
	}
	ApplyFuelCalibration(nil, 100) // must not panic
}

// fuelMessage builds a decoded message with only the raw height populated.
func fuelMessage(heightCM float64) models.TelemetryMessage {
	h := heightCM
	return models.TelemetryMessage{FuelHeightCM: &h}
}
