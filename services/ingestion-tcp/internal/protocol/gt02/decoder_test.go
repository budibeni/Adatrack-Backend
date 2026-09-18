package gt02

import (
	"encoding/hex"
	"testing"
)

func TestGT02DecodeLocation(t *testing.T) {
	d := &Decoder{}
	// Mock packet
	data, _ := hex.DecodeString("28280011223344556677880295C1700684C21030000D0A")
	payload, err := d.DecodeLocation(data, "12345678", "test", 1)
	if err != nil {
		t.Fatalf("Failed to decode: %v", err)
	}
	
	if payload.Latitude == 0 || payload.Longitude == 0 {
		t.Errorf("Coordinates not parsed")
	}
	if payload.Speed != 48 {
		t.Errorf("Speed mismatch: got %f", payload.Speed)
	}
}
