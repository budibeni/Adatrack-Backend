// Package geo holds the shared geodesic helpers of phase B7:
//
//   - Haversine distance / odometer accumulation (FR-2.5),
//   - Ramer–Douglas–Peucker point reduction for playback (B7.4),
//   - the offline reverse-geocoding index built from the master region tables
//     (`tm_provinces`→`tm_subdistricts`, B7.3).
//
// Everything here is pure and dependency-free, so worker-live (accumulators) and
// service-websocket (playback + geocoding) compute identical values.
package geo

import "math"

// EarthRadiusKM is the mean Earth radius pinned by PRD FR-2.5 for the odometer
// accumulation (Haversine, radius 6371 km).
const EarthRadiusKM = 6371.0

// DegToRad converts degrees to radians.
const DegToRad = math.Pi / 180

// Point is one WGS84 coordinate.
type Point struct {
	Lat float64
	Lon float64
}

// DistanceKM returns the great-circle (Haversine) distance in kilometres.
//
//	2 · R · asin(√(sin²(Δφ/2) + cos φ1 · cos φ2 · sin²(Δλ/2)))
func DistanceKM(a, b Point) float64 {
	lat1 := a.Lat * DegToRad
	lat2 := b.Lat * DegToRad
	dLat := (b.Lat - a.Lat) * DegToRad
	dLon := (b.Lon - a.Lon) * DegToRad

	sinLat := math.Sin(dLat / 2)
	sinLon := math.Sin(dLon / 2)
	h := sinLat*sinLat + math.Cos(lat1)*math.Cos(lat2)*sinLon*sinLon
	if h > 1 {
		h = 1
	}
	return 2 * EarthRadiusKM * math.Asin(math.Sqrt(h))
}

// DistanceM returns DistanceKM in metres (playback tolerance, region search).
func DistanceM(a, b Point) float64 { return DistanceKM(a, b) * 1000 }

// IsNullIsland reports the (0,0) coordinate, which the telemetry pipeline uses
// for "no GPS fix" (FR-3.4 positionless routing) — never a real position.
func (p Point) IsNullIsland() bool { return p.Lat == 0 && p.Lon == 0 }
