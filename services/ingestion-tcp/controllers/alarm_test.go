package controllers

import (
	"testing"
	"time"
)

// TestParseAlarm decodes the alarm tail (terminal info + battery + GSM + code).
func TestParseAlarm(t *testing.T) {
	when := time.Date(2026, 9, 15, 3, 30, 0, 0, time.UTC)
	payload := buildGPSBlock(when, 0x08, -6.2, 106.8, 0, false, true)
	// LBS block (9 bytes) + terminal info (ACC bit1) + voltage + GSM + alarm.
	payload = append(payload, 0x02, 0x01, 0xF4, 0x01, 0x00, 0x01, 0x00, 0x01, 0x23)
	payload = append(payload, 0x02) // ACC high
	payload = append(payload, 0x05) // battery level
	payload = append(payload, 0x04) // gsm signal
	payload = append(payload, 0x01, 0x00)

	msg, ok := ParseAlarm(payload)
	if !ok {
		t.Fatal("ParseAlarm returned ok=false")
	}
	if !msg.ACC {
		t.Error("expected ACC=true from the terminal information byte (0x02)")
	}
	if msg.Battery != 5 {
		t.Errorf("battery = %d, want 5", msg.Battery)
	}
	if msg.GsmSignal != 4 {
		t.Errorf("gsm_signal = %d, want 4", msg.GsmSignal)
	}
	if msg.AlarmCode != 0x01 {
		t.Errorf("alarm_code = %d, want 1", msg.AlarmCode)
	}
}

// TestParseLBSAlarm checks the LBS-only alarm produces a "now" timestamp and no
// coordinates (the device could not fix a position).
func TestParseLBSAlarm(t *testing.T) {
	before := time.Now().Unix()
	msg := ParseLBSAlarm(nil, "864201040512345", "DEV001", 7)
	if msg.IMEI != "864201040512345" || msg.CompanyCode != "DEV001" || msg.VehicleID != 7 {
		t.Errorf("tenant context lost: %+v", msg)
	}
	if msg.Lat != 0 || msg.Lon != 0 {
		t.Errorf("expected no coordinates for an LBS alarm, got %.4f/%.4f", msg.Lat, msg.Lon)
	}
	if msg.Timestamp < before {
		t.Errorf("timestamp %d is older than the test start %d", msg.Timestamp, before)
	}
}

// TestParseInfoTransmit decodes the 0x94 fuel information packet and rejects
// unsupported information types.
func TestParseInfoTransmit(t *testing.T) {
	msg, ok := ParseInfoTransmit([]byte{0x0D, 0x01, 0x00, 0x7B})
	if !ok {
		t.Fatal("ParseInfoTransmit rejected information type 0x0D")
	}
	if msg.FuelLevel == nil || *msg.FuelLevel != 123 {
		t.Errorf("fuel_level = %v, want 123", msg.FuelLevel)
	}
	if _, ok := ParseInfoTransmit([]byte{0x01, 0x00}); ok {
		t.Error("ParseInfoTransmit accepted an unsupported information type")
	}
	if _, ok := ParseInfoTransmit([]byte{0x0D}); ok {
		t.Error("ParseInfoTransmit accepted a truncated payload")
	}
}
