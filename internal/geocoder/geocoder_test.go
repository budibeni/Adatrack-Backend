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
	// test resolution for valid coordinate
	resolved, err := ReverseGeocode(context.Background(), -6.329155, 106.492297)
	if err != nil {
		t.Logf("Offline or rate-limited: %v", err)
	} else if resolved == "" {
		t.Fatalf("Expected non-empty resolved address")
	}
}
