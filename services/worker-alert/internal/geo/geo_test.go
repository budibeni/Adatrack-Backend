package geo

import (
	"math"
	"testing"
)

func TestHaversine(t *testing.T) {
	// Jakarta (-6.2088, 106.8456) to Bandung (-6.9175, 107.6191) ~ 115-125 km
	jkt := Point{Lat: -6.2088, Lon: 106.8456}
	bdg := Point{Lat: -6.9175, Lon: 107.6191}

	dist := Haversine(jkt, bdg)
	if dist < 110000 || dist > 135000 {
		t.Fatalf("expected distance between Jakarta and Bandung ~120km, got %.1f meters", dist)
	}

	// Same point distance must be 0
	if zero := Haversine(jkt, jkt); zero > 1e-6 {
		t.Fatalf("expected distance to self to be 0, got %f", zero)
	}
}

func TestRayCasting(t *testing.T) {
	// Square polygon around (-6.2, 106.8)
	polygon := []Point{
		{Lat: -6.1, Lon: 106.7},
		{Lat: -6.1, Lon: 106.9},
		{Lat: -6.3, Lon: 106.9},
		{Lat: -6.3, Lon: 106.7},
	}

	inside := Point{Lat: -6.2, Lon: 106.8}
	if !RayCasting(inside, polygon) {
		t.Errorf("expected point (-6.2, 106.8) to be INSIDE polygon")
	}

	outside := Point{Lat: -6.5, Lon: 107.0}
	if RayCasting(outside, polygon) {
		t.Errorf("expected point (-6.5, 107.0) to be OUTSIDE polygon")
	}

	// Degenerate polygon (< 3 points)
	if RayCasting(inside, []Point{{Lat: 0, Lon: 0}, {Lat: 1, Lon: 1}}) {
		t.Errorf("expected false for polygon with less than 3 points")
	}
}

func TestDistanceToPolyline(t *testing.T) {
	waypoints := []Point{
		{Lat: -6.2000, Lon: 106.8000},
		{Lat: -6.2000, Lon: 106.8100},
		{Lat: -6.2000, Lon: 106.8200},
	}

	// Point right on segment
	onRoute := Point{Lat: -6.2000, Lon: 106.8050}
	distOn := DistanceToPolyline(onRoute, waypoints)
	if distOn > 10 {
		t.Errorf("expected point on route to have distance ~0, got %.2f meters", distOn)
	}

	// Point 500m north (1 degree latitude ~ 111km -> 0.0045 deg ~ 500m)
	offRoute := Point{Lat: -6.1955, Lon: 106.8050}
	distOff := DistanceToPolyline(offRoute, waypoints)
	if math.Abs(distOff-500) > 60 {
		t.Errorf("expected distance ~500m, got %.2f meters", distOff)
	}
}
