package main

import (
	"encoding/json"
	"strings"
	"testing"

	"ajb_gps/ingestion-tcp/controllers"
	"ajb_gps/ingestion-tcp/models"
)

func TestProtocolConstants(t *testing.T) {
	if models.ProtoLogin != 0x01 {
		t.Errorf("ProtoLogin = 0x%02x, want 0x01", models.ProtoLogin)
	}
	if models.ProtoPosition != 0x22 {
		t.Errorf("ProtoPosition = 0x%02x, want 0x22", models.ProtoPosition)
	}
	if models.ProtoPosition2 != 0x12 {
		t.Errorf("ProtoPosition2 = 0x%02x, want 0x12", models.ProtoPosition2)
	}
	if models.ProtoHeartbeat != 0x13 {
		t.Errorf("ProtoHeartbeat = 0x%02x, want 0x13", models.ProtoHeartbeat)
	}
	if models.ProtoHeartbeatEG != 0x23 {
		t.Errorf("ProtoHeartbeatEG = 0x%02x, want 0x23", models.ProtoHeartbeatEG)
	}
	if models.ProtoAlarm != 0x26 {
		t.Errorf("ProtoAlarm = 0x%02x, want 0x26", models.ProtoAlarm)
	}
	if models.ProtoAlarmHVT != 0x27 {
		t.Errorf("ProtoAlarmHVT = 0x%02x, want 0x27", models.ProtoAlarmHVT)
	}
	if models.ProtoAlarmLBS != 0x19 {
		t.Errorf("ProtoAlarmLBS = 0x%02x, want 0x19", models.ProtoAlarmLBS)
	}
}

func TestTelemetryMessageJSON(t *testing.T) {
	tm := models.TelemetryMessage{
		IMEI:       "864201040512345",
		Lat:        -6.2088,
		Lon:        106.8456,
		Speed:      45.2,
		Heading:    180,
		Satellites: 8,
		Timestamp:  1723800000,
	}
	data, err := json.Marshal(tm)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if !strings.Contains(string(data), "864201040512345") {
		t.Error("marshalled data should contain IMEI")
	}
}

func TestBcdToInt(t *testing.T) {
	tests := []struct {
		input byte
		want  int
	}{
		{0x00, 0}, {0x01, 1}, {0x09, 9}, {0x10, 10}, {0x12, 12}, {0x99, 99}, {0x26, 26},
	}
	for _, tc := range tests {
		if got := controllers.BcdToInt(tc.input); got != tc.want {
			t.Errorf("BcdToInt(0x%02x) = %d, want %d", tc.input, got, tc.want)
		}
	}
}

func TestIntToBcd(t *testing.T) {
	for _, tc := range []struct {
		in   int
		want byte
	}{
		{0, 0x00}, {9, 0x09}, {10, 0x10}, {26, 0x26}, {99, 0x99},
	} {
		if got := controllers.IntToBcd(tc.in); got != tc.want {
			t.Errorf("IntToBcd(%d) = 0x%02x, want 0x%02x", tc.in, got, tc.want)
		}
	}
}

func TestParseLoginIMEI(t *testing.T) {
	imei := "864201040512345"
	if result := controllers.ParseLoginIMEI([]byte(imei)); result != imei {
		t.Errorf("ParseLoginIMEI() = %q, want %q", result, imei)
	}
}

func TestParseLoginIMEITruncates(t *testing.T) {
	long := "8642010405123456789extra"
	if result := controllers.ParseLoginIMEI([]byte(long)); result != "864201040512345" {
		t.Errorf("ParseLoginIMEI() = %q, want %q", result, "864201040512345")
	}
}
