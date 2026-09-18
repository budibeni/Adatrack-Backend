package coban

import (
	"testing"
)

func TestCobanDecodeLocation(t *testing.T) {
	d := &Decoder{}
	data := []byte("imei:123456789012345,tracker,210917,143000,F,035022.000,A,2232.1234,N,11404.1234,E,0.00,0.00")
	payload, err := d.DecodeLocation(data, "123456789012345", "test_company", 1)
	if err != nil {
		t.Fatalf("Failed to decode: %v", err)
	}
	
	if payload.Latitude == 0 {
		t.Errorf("Latitude not parsed")
	}
	if payload.Longitude == 0 {
		t.Errorf("Longitude not parsed")
	}
}
