package geo

import (
	"math"
	"testing"
)

// TestDistanceKMKnownPairs pins the Haversine result against well-known
// distances (FR-2.5 uses the same formula with radius 6371 km).
func TestDistanceKMKnownPairs(t *testing.T) {
	jakarta := Point{Lat: -6.2088, Lon: 106.8456}
	bandung := Point{Lat: -6.9175, Lon: 107.6191}
	surabaya := Point{Lat: -7.2575, Lon: 112.7521}

	cases := []struct {
		name     string
		a, b     Point
		min, max float64
	}{
		{"identical points are 0 km", jakarta, jakarta, 0, 0.0001},
		{"Jakarta→Bandung ≈ 120 km", jakarta, bandung, 115, 125},
		{"Jakarta→Surabaya ≈ 660 km", jakarta, surabaya, 640, 680},
	}
	for _, tc := range cases {
		got := DistanceKM(tc.a, tc.b)
		if got < tc.min || got > tc.max {
			t.Errorf("%s: DistanceKM = %.3f km, want within [%.3f, %.3f]", tc.name, got, tc.min, tc.max)
		}
		if back := DistanceKM(tc.b, tc.a); math.Abs(back-got) > 1e-9 {
			t.Errorf("%s: distance must be symmetric (%.9f vs %.9f)", tc.name, got, back)
		}
	}

	if m := DistanceM(jakarta, bandung); m < 115000 || m > 125000 {
		t.Errorf("DistanceM = %.1f m, want within [115000, 125000]", m)
	}
}

// TestIsNullIsland documents the "no GPS fix" sentinel used by the pipeline.
func TestIsNullIsland(t *testing.T) {
	if !(Point{}).IsNullIsland() {
		t.Error("(0,0) must be treated as no fix")
	}
	if (Point{Lat: -6.2, Lon: 0}).IsNullIsland() {
		t.Error("a valid latitude on the meridian is not the null island")
	}
}
