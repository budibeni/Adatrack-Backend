package geocoder

import (
	"context"
	"fmt"
	"sync"
	"strings"

	"backend/internal/dbclient"
)

// simple in-memory cache for reverse geocoding
var (
	cache = make(map[string]string)
	mu    sync.RWMutex
)

func ClearCache() {
	mu.Lock()
	defer mu.Unlock()
	cache = make(map[string]string)
}

func SetCache(lat, lon float64, address string) {
	cacheKey := fmt.Sprintf("%.3f,%.3f", lat, lon)
	mu.Lock()
	defer mu.Unlock()
	cache[cacheKey] = address
}

func ReverseGeocode(ctx context.Context, lat, lon float64) (string, error) {
	cacheKey := fmt.Sprintf("%.3f,%.3f", lat, lon)

	mu.RLock()
	if val, ok := cache[cacheKey]; ok {
		mu.RUnlock()
		return val, nil
	}
	mu.RUnlock()

	if dbclient.Pool != nil {
		query := `
			SELECT r.name, r.level, p1.name, p2.name, p3.name
			FROM adatrack_gps_master.tm_regions r
			LEFT JOIN adatrack_gps_master.tm_regions p1 ON r.parent_id = p1.id
			LEFT JOIN adatrack_gps_master.tm_regions p2 ON p1.parent_id = p2.id
			LEFT JOIN adatrack_gps_master.tm_regions p3 ON p2.parent_id = p3.id
			WHERE ST_Contains(r.geom, ST_SetSRID(ST_MakePoint($2, $1), 4326))
			ORDER BY 
			  CASE r.level 
			    WHEN 'village' THEN 1 
			    WHEN 'district' THEN 2 
			    WHEN 'city' THEN 3 
			    WHEN 'province' THEN 4 
			    ELSE 5 
			  END ASC
			LIMIT 1
		`
		var name, level string
		var p1, p2, p3 *string
		err := dbclient.Pool.QueryRow(ctx, query, lat, lon).Scan(&name, &level, &p1, &p2, &p3)
		if err == nil && name != "" {
			parts := []string{name}
			if p1 != nil { parts = append(parts, *p1) }
			if p2 != nil { parts = append(parts, *p2) }
			if p3 != nil { parts = append(parts, *p3) }
			parts = append(parts, "Indonesia")
			addr := strings.Join(parts, ", ")

			mu.Lock()
			cache[cacheKey] = addr
			mu.Unlock()
			return addr, nil
		}

		var cityName, provinceName string
		queryFallbackAlt := `
			SELECT c.name, COALESCE(p.name, '')
			FROM adatrack_gps_master.tm_cities c
			LEFT JOIN adatrack_gps_master.tm_provinces p ON c.province_id = p.id
			WHERE c.latitude IS NOT NULL AND c.longitude IS NOT NULL
			ORDER BY ((c.latitude - $1) * (c.latitude - $1) + (c.longitude - $2) * (c.longitude - $2)) ASC
			LIMIT 1
		`
		err = dbclient.Pool.QueryRow(ctx, queryFallbackAlt, lat, lon).Scan(&cityName, &provinceName)
		if err == nil && cityName != "" {
			var addr string
			if provinceName != "" {
				addr = fmt.Sprintf("%s, %s, Indonesia", cityName, provinceName)
			} else {
				addr = fmt.Sprintf("%s, Indonesia", cityName)
			}
			mu.Lock()
			cache[cacheKey] = addr
			mu.Unlock()
			return addr, nil
		}
	}

	return "", fmt.Errorf("geocoding failed: no offline data found")
}
