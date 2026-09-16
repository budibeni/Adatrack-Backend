// Package controllers implements the worker-alert engine (PRD Module 6, B3).
package controllers

import "math"

// EarthRadiusM is the mean Earth radius used by the Haversine formula.
const EarthRadiusM = 6371000.0

// HaversineM returns the great-circle distance in METRES between two points
// given in decimal degrees (PRD §5.9.1: circle geofence detection).
func HaversineM(lat1, lon1, lat2, lon2 float64) float64 {
	rad := math.Pi / 180
	phi1, phi2 := lat1*rad, lat2*rad
	dphi := (lat2 - lat1) * rad
	dlambda := (lon2 - lon1) * rad
	a := math.Sin(dphi/2)*math.Sin(dphi/2) +
		math.Cos(phi1)*math.Cos(phi2)*math.Sin(dlambda/2)*math.Sin(dlambda/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return EarthRadiusM * c
}

// PointInPolygon reports whether the point (lat, lon) lies inside the polygon
// using the ray-casting algorithm (PRD §5.9.1). The polygon ring does NOT need
// to be explicitly closed: the first vertex is re-tested at the end. Holes are
// not supported (out of scope for B3).
func PointInPolygon(lat, lon float64, poly [][2]float64) bool {
	if len(poly) < 3 {
		return false
	}
	inside := false
	j := len(poly) - 1
	for i := 0; i < len(poly); i++ {
		yi, xi := poly[i][0], poly[i][1] // vertex i: [lat, lon]
		yj, xj := poly[j][0], poly[j][1]
		if (yi > lat) != (yj > lat) {
			// Crossing test on longitude at the point's latitude.
			xCross := (xj-xi)*(lat-yi)/(yj-yi) + xi
			if lon < xCross {
				inside = !inside
			}
		}
		j = i
	}
	return inside
}

// NearestWaypointM returns the smallest Haversine distance (metres) from the
// point to any waypoint, plus the index of that waypoint (-1 when empty).
func NearestWaypointM(lat, lon float64, waypoints [][2]float64) (float64, int) {
	best := math.MaxFloat64
	bestIdx := -1
	for i, w := range waypoints {
		d := HaversineM(lat, lon, w[0], w[1])
		if d < best {
			best, bestIdx = d, i
		}
	}
	return best, bestIdx
}
