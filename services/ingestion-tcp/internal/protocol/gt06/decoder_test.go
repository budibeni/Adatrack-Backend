package gt06

import (
	"encoding/hex"
	"testing"
)

func TestGT06DecodeLogin(t *testing.T) {
	d := &Decoder{}
	packetHex := "78781101012345678901234500018C2A0D0A" // typical login
	packet, _ := hex.DecodeString(packetHex)
	
	imei, resp, err := d.DecodeLogin(packet)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if imei != "123456789012345" {
		t.Errorf("Expected IMEI 123456789012345, got %s", imei)
	}
	if len(resp) == 0 || resp[3] != 0x01 {
		t.Errorf("Expected login response, got %v", resp)
	}
}

func TestGT06DecodeLocation(t *testing.T) {
	d := &Decoder{}
	packetHex := "78781F120B081D112E0A03813C1C0C4658440014020101010000000000000D0A"
	packet, _ := hex.DecodeString(packetHex)
	
	payload, err := d.DecodeLocation(packet, "12345", "COMP", 1)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if payload.Satellites != 3 {
		t.Errorf("Expected 3 satellites, got %d", payload.Satellites)
	}
}
