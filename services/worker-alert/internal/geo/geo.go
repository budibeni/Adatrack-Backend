package geo

import (
	"math"
)

const earthRadius = 6371000 // meters

type Point struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// Haversine computes great-circle distance between two geographic points in meters
func Haversine(p1, p2 Point) float64 {
	lat1 := p1.Lat * math.Pi / 180
	lon1 := p1.Lon * math.Pi / 180
	lat2 := p2.Lat * math.Pi / 180
	lon2 := p2.Lon * math.Pi / 180

	dlat := lat2 - lat1
	dlon := lon2 - lon1

	a := math.Pow(math.Sin(dlat/2), 2) + math.Cos(lat1)*math.Cos(lat2)*math.Pow(math.Sin(dlon/2), 2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))

	return earthRadius * c
}

// RayCasting checks if a point is inside a polygon using the Jordan curve theorem
func RayCasting(point Point, polygon []Point) bool {
	if len(polygon) < 3 {
		return false
	}
	inside := false
	j := len(polygon) - 1
	for i := 0; i < len(polygon); i++ {
		if (polygon[i].Lon > point.Lon) != (polygon[j].Lon > point.Lon) &&
			point.Lat < (polygon[j].Lat-polygon[i].Lat)*(point.Lon-polygon[i].Lon)/(polygon[j].Lon-polygon[i].Lon)+polygon[i].Lat {
			inside = !inside
		}
		j = i
	}
	return inside
}

// DistanceToSegment calculates the minimum distance in meters from point p to segment [a, b]
func DistanceToSegment(p, a, b Point) float64 {
	// Vector AB
	dx := (b.Lon - a.Lon) * math.Cos((a.Lat+b.Lat)*math.Pi/360.0)
	dy := b.Lat - a.Lat

	// Vector AP
	px := (p.Lon - a.Lon) * math.Cos((a.Lat+p.Lat)*math.Pi/360.0)
	py := p.Lat - a.Lat

	segmentLenSq := dx*dx + dy*dy
	if segmentLenSq < 1e-12 {
		return Haversine(p, a)
	}

	// Project AP onto AB
	t := (px*dx + py*dy) / segmentLenSq
	if t < 0 {
		return Haversine(p, a)
	} else if t > 1 {
		return Haversine(p, b)
	}

	closest := Point{
		Lat: a.Lat + t*(b.Lat-a.Lat),
		Lon: a.Lon + t*(b.Lon-a.Lon),
	}
	return Haversine(p, closest)
}

// DistanceToPolyline calculates the minimum distance in meters from point p to a polyline
func DistanceToPolyline(p Point, waypoints []Point) float64 {
	if len(waypoints) == 0 {
		return 0
	}
	if len(waypoints) == 1 {
		return Haversine(p, waypoints[0])
	}

	minDist := math.MaxFloat64
	for i := 0; i < len(waypoints)-1; i++ {
		d := DistanceToSegment(p, waypoints[i], waypoints[i+1])
		if d < minDist {
			minDist = d
		}
	}
	return minDist
}
