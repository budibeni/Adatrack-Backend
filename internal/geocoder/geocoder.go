package geocoder

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"backend/internal/dbclient"
)

// simple in-memory cache for reverse geocoding
var (
	cache = make(map[string]string)
	mu    sync.RWMutex
)

// ClearCache clears all in-memory geocoding cache entries.
func ClearCache() {
	mu.Lock()
	defer mu.Unlock()
	cache = make(map[string]string)
}

// SetCache sets an explicit address in cache for testing or pre-warming.
func SetCache(lat, lon float64, address string) {
	cacheKey := fmt.Sprintf("%.3f,%.3f", lat, lon)
	mu.Lock()
	defer mu.Unlock()
	cache[cacheKey] = address
}

type NominatimResponse struct {
	DisplayName string `json:"display_name"`
	Address     struct {
		Village  string `json:"village"`
		Town     string `json:"town"`
		City     string `json:"city"`
		Province string `json:"state"`
		Country  string `json:"country"`
	} `json:"address"`
}

// ReverseGeocode tries to resolve (lat, lon) to a formatted address string.
func ReverseGeocode(ctx context.Context, lat, lon float64) (string, error) {
	// Key based on ~100m precision (3 decimal places)
	cacheKey := fmt.Sprintf("%.3f,%.3f", lat, lon)

	mu.RLock()
	if val, ok := cache[cacheKey]; ok {
		mu.RUnlock()
		return val, nil
	}
	mu.RUnlock()

	// 1. Try local database spatial lookup first (offline first, fast & enterprise grade)
	if dbclient.Pool != nil {
		var cityName, provinceName string
		query := `
			SELECT c.name, COALESCE(p.name, '')
			FROM adatrack_gps_master.tm_cities c
			LEFT JOIN adatrack_gps_master.tm_provinces p ON c.province_id = p.id
			WHERE c.latitude IS NOT NULL AND c.longitude IS NOT NULL
			ORDER BY ((c.latitude - $1) * (c.latitude - $1) + (c.longitude - $2) * (c.longitude - $2)) ASC
			LIMIT 1
		`
		err := dbclient.Pool.QueryRow(ctx, query, lat, lon).Scan(&cityName, &provinceName)
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

	// 2. Fallback to Nominatim API (ensure to follow usage policy: 1 req/sec)
	apiURL := fmt.Sprintf("https://nominatim.openstreetmap.org/reverse?format=json&lat=%f&lon=%f&zoom=18&addressdetails=1", lat, lon)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Adatrack-Backend/1.0")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("geocoding failed: status %d", resp.StatusCode)
	}

	var data NominatimResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", err
	}

	mu.Lock()
	cache[cacheKey] = data.DisplayName
	mu.Unlock()

	return data.DisplayName, nil
}
