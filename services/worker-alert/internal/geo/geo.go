package geo

import (
	"math"
)

const earthRadius = 6371000 // meters

type Point struct {
	Lat float64
	Lon float64
}

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

// RayCasting checks if a point is inside a polygon
func RayCasting(point Point, polygon []Point) bool {
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
