package controllers

import (
	"bufio"
	"bytes"
	"testing"
	"time"

	"ajb_gps/ingestion-tcp/models"
)

// TestParseFuelSensorGolden verifies the fuel-sensor parser against the
// exact frame documented in docs/docs-device/GPS_Tracker_communication_protocol_v3.1.md
// ("0D Fuel sensor data"), ensuring 1:1 parity with the vendor sample.
func TestParseFuelSensorGolden(t *testing.T) {
	// Frame construction:
	//   Start(79 79) + Len(2 bytes) + Proto(94) + Type(0D)
	//   + Time(11 09 07 08 0C 03)
	//   + ASCII sensor (!AIOIL,02,025.900,025.400,519J,0200,027.140,0,00,9F)
	//   + Serial(0D 12)
	//   + CRC-ITU(2 bytes, computed)
	//   + Stop(0D 0A)
	//
	// The CRC is computed over [length bytes || payload bytes] exactly as
	// ReadPacket / crc16Frame do, so this is a true 1:1 golden test.

	timeBlock := []byte{0x11, 0x09, 0x07, 0x08, 0x0C, 0x03}
	// Expected date: year 0x11+2000=2017, month 9, day 7, 08:12:03
	expectedTime := time.Date(2017, 9, 7, 8, 12, 3, 0, time.UTC)

	ascii := []byte("!AIOIL,02,025.900,025.400,519J,0200,027.140,0,00,9F")

	// Build payload: Proto | Type | Time | ASCII | Serial
	payload := []byte{0x94, 0x0D}
	payload = append(payload, timeBlock...)
	payload = append(payload, ascii...)
	payload = append(payload, 0x0D, 0x12) // serial number

	length := uint16(len(payload))
	hdr := []byte{byte(length >> 8), byte(length)}

	crc := crc16Frame(append(hdr, payload...))

	// Assemble full frame: start(79 79) + len(2) + payload + crc(2) + stop(0D 0A)
	frame := []byte{0x79, 0x79}
	frame = append(frame, hdr...)
	frame = append(frame, payload...)
	frame = append(frame, crc...)
	frame = append(frame, 0x0D, 0x0A) // stop bits

	// Verify the frame can be parsed by ReadPacket (CRC validation)
	packet, err := ReadPacket(bufio.NewReader(bytes.NewReader(frame)))
	if err != nil {
		t.Fatalf("ReadPacket failed on fuel-sensor frame: %v", err)
	}
	if packet.Protocol != models.ProtoInfoTransmit {
		t.Fatalf("expected protocol 0x94, got 0x%02X", packet.Protocol)
	}

	// Parse with ParseFuelSensor
	tm, ok := ParseFuelSensor(packet.Data)
	if !ok {
		t.Fatal("ParseFuelSensor returned ok=false on valid frame")
	}

	// Timestamp
	if !time.Unix(tm.Timestamp, 0).Equal(expectedTime) {
		t.Errorf("timestamp: got %d (%s), want %d (%s)",
			tm.Timestamp, time.Unix(tm.Timestamp, 0), expectedTime.Unix(), expectedTime)
	}

	// Fuel level = 027.140 (field index 6)
	if tm.FuelLevel == nil {
		t.Fatal("FuelLevel is nil")
	}
	if *tm.FuelLevel != 027.140 {
		t.Errorf("FuelLevel: got %.3f, want 27.140", *tm.FuelLevel)
	}

	// FuelTempC = 025.400 (field index 3)
	if tm.FuelTempC == nil {
		t.Fatal("FuelTempC is nil")
	}
	if *tm.FuelTempC != 025.400 {
		t.Errorf("FuelTempC: got %.3f, want 25.400", *tm.FuelTempC)
	}
}

// TestParseFuelSensorErrors verifies error handling for malformed payloads.
func TestParseFuelSensorErrors(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"too_short", []byte{0x0D, 0x01, 0x02}},
		{"wrong_info_type", []byte{0x0E, 0x11, 0x09, 0x07, 0x08, 0x0C, 0x03, '!'}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := ParseFuelSensor(tt.data)
			if ok {
				t.Error("expected ok=false for malformed input")
			}
		})
	}
}

// TestParseFuelSensorFromHex verifies parsing from a minimal data block
// (without relying on ReadPacket).
func TestParseFuelSensorFromHex(t *testing.T) {
	timeBlock := []byte{0x11, 0x09, 0x07, 0x08, 0x0C, 0x03}
	ascii := []byte("!AIOIL,02,025.900,025.400,519J,0200,027.140,0,00,9F")

	data := []byte{0x0D}
	data = append(data, timeBlock...)
	data = append(data, ascii...)

	// No serial suffix — parser should handle this (len <= 2 after sensor start)
	tm, ok := ParseFuelSensor(data)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if tm.FuelLevel == nil || *tm.FuelLevel != 27.14 {
		t.Errorf("FuelLevel: got %v, want 27.14", tm.FuelLevel)
	}
	if tm.FuelTempC == nil || *tm.FuelTempC != 25.4 {
		t.Errorf("FuelTempC: got %v, want 25.4", tm.FuelTempC)
	}
}
