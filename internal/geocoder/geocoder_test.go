package geocoder

import (
	"context"
	"testing"
)

func TestGeocoderCache(t *testing.T) {
	ClearCache()
	SetCache(-6.2, 106.8, "Jakarta")
	
	addr, err := ReverseGeocode(context.Background(), -6.2, 106.8)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if addr != "Jakarta" {
		t.Fatalf("Expected Jakarta, got %s", addr)
	}
	
	ClearCache()
	// test miss
	_, err = ReverseGeocode(context.Background(), -6.2, 106.8)
	if err == nil {
		t.Fatalf("Expected error for DB not configured in test")
	}
}
