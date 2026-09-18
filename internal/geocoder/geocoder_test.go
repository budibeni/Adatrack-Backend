package geocoder

import (
	"context"
	"testing"
)

func TestReverseGeocode_CacheHit(t *testing.T) {
	ClearCache()

	lat, lon := -6.2088, 106.8456
	expectedAddr := "Jakarta Pusat, DKI Jakarta, Indonesia"

	SetCache(lat, lon, expectedAddr)

	ctx := context.Background()
	addr, err := ReverseGeocode(ctx, lat, lon)
	if err != nil {
		t.Fatalf("expected no error on cache hit, got %v", err)
	}

	if addr != expectedAddr {
		t.Errorf("expected %s, got %s", expectedAddr, addr)
	}
}

func TestReverseGeocode_ClearCache(t *testing.T) {
	ClearCache()

	lat, lon := -7.2575, 112.7521
	SetCache(lat, lon, "Surabaya, Jawa Timur, Indonesia")

	ClearCache()

	mu.RLock()
	count := len(cache)
	mu.RUnlock()

	if count != 0 {
		t.Errorf("expected cache to be empty after ClearCache, got len %d", count)
	}
}
