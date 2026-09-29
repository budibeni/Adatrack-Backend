package geofence

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"backend/internal/dbclient"
)

type Point struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

type Geofence struct {
	ID        int
	Name      string
	AreaType  string
	Boundaries []Point
}

var (
	geofenceCache = make(map[string][]Geofence)
	lastFetch     = make(map[string]time.Time)
	mu            sync.RWMutex
)

func LoadGeofences(ctx context.Context, companyCode string) ([]Geofence, error) {
	mu.RLock()
	cached, ok := geofenceCache[companyCode]
	last := lastFetch[companyCode]
	mu.RUnlock()

	// Cache for 5 minutes
	if ok && time.Since(last) < 5*time.Minute {
		return cached, nil
	}

	schema := fmt.Sprintf("adatrack_gps_%s", companyCode)
	query := fmt.Sprintf("SELECT id, name, area_type, boundary_points FROM %s.tm_geofences WHERE deleted_at IS NULL", schema)
	
	rows, err := dbclient.Pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var geofences []Geofence
	for rows.Next() {
		var g Geofence
		var boundaryJSON []byte
		if err := rows.Scan(&g.ID, &g.Name, &g.AreaType, &boundaryJSON); err == nil {
			var pts []Point
			if err := json.Unmarshal(boundaryJSON, &pts); err == nil {
				g.Boundaries = pts
				geofences = append(geofences, g)
			}
		}
	}

	mu.Lock()
	geofenceCache[companyCode] = geofences
	lastFetch[companyCode] = time.Now()
	mu.Unlock()

	return geofences, nil
}

// Ray-Casting algorithm for Point in Polygon
func isPointInPolygon(p Point, polygon []Point) bool {
	inside := false
	for i, j := 0, len(polygon)-1; i < len(polygon); j, i = i, i+1 {
		pi := polygon[i]
		pj := polygon[j]
		if ((pi.Lng > p.Lng) != (pj.Lng > p.Lng)) &&
			(p.Lat < (pj.Lat-pi.Lat)*(p.Lng-pi.Lng)/(pj.Lng-pi.Lng)+pi.Lat) {
			inside = !inside
		}
	}
	return inside
}

func CheckGeofence(ctx context.Context, companyCode string, lat, lon float64) (string, string) {
	geofences, err := LoadGeofences(ctx, companyCode)
	if err != nil || len(geofences) == 0 {
		return "", ""
	}

	p := Point{Lat: lat, Lng: lon}
	for _, g := range geofences {
		if g.AreaType == "polygon" && len(g.Boundaries) >= 3 {
			if isPointInPolygon(p, g.Boundaries) {
				return g.Name, g.AreaType
			}
		}
	}
	return "", ""
}
