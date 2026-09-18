package suntech

import (
	"testing"
)

func TestSuntechDecodeLocation(t *testing.T) {
	d := &Decoder{}
	data := []byte("SA200G;123456789012345;02;054;20081001;120000;123456;37.478519;126.879795;60.5;120.00")
	payload, err := d.DecodeLocation(data, "123456789012345", "test_company", 1)
	if err != nil {
		t.Fatalf("Failed to decode: %v", err)
	}
	
	if payload.Latitude != 37.478519 {
		t.Errorf("Latitude mismatch: got %f", payload.Latitude)
	}
	if payload.Longitude != 126.879795 {
		t.Errorf("Longitude mismatch: got %f", payload.Longitude)
	}
	if payload.Speed != 60.5 {
		t.Errorf("Speed mismatch: got %f", payload.Speed)
	}
	if payload.Heading != 120.00 {
		t.Errorf("Heading mismatch: got %f", payload.Heading)
	}
}
