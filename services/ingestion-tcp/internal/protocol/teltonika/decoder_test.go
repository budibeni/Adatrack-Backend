package teltonika

import (
	"encoding/hex"
	"testing"
)

func TestTeltonikaDecodeLogin(t *testing.T) {
	d := &Decoder{}
	// Example teltonika login: \x00\x0F followed by 15 bytes IMEI
	imei := "123456789012345"
	packet := append([]byte{0x00, 0x0F}, []byte(imei)...)
	
	parsedImei, resp, err := d.DecodeLogin(packet)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if parsedImei != imei {
		t.Errorf("Expected IMEI %s, got %s", imei, parsedImei)
	}
	if len(resp) != 1 || resp[0] != 0x01 {
		t.Errorf("Expected response 0x01, got %v", resp)
	}
}

func TestTeltonikaDecodeLocation(t *testing.T) {
	d := &Decoder{}
	
	// Create a dummy Codec 8 packet
	// 4 zeros, 4 len, 1 codec (0x08), 1 count, 8 timestamp, 1 prio, 4 lon, 4 lat, 2 alt, 2 angle, 1 sat, 2 speed, ...
	// Since we parse up to byte 34, we need at least 45 bytes to pass the length check
	packetHex := "000000000000002108010000017D00000000000000000000000000000000000000000000000000000000000000"
	packet, _ := hex.DecodeString(packetHex)
	
	_, err := d.DecodeLocation(packet, "12345", "COMP", 1)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
}
