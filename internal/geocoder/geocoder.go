package geocoder

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// simple in-memory cache for reverse geocoding
var (
	cache = make(map[string]string)
	mu    sync.RWMutex
)

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

	// Fallback to Nominatim API (ensure to follow usage policy: 1 req/sec)
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
