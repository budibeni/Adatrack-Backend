package geocoder

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"strings"
	"time"

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
	cacheKey := fmt.Sprintf("%.4f,%.4f", lat, lon)
	mu.Lock()
	defer mu.Unlock()
	cache[cacheKey] = address
}

func GetCached(lat, lon float64) (string, bool) {
	cacheKey := fmt.Sprintf("%.4f,%.4f", lat, lon)
	mu.RLock()
	defer mu.RUnlock()
	val, ok := cache[cacheKey]
	return val, ok && val != ""
}

func ReverseGeocode(ctx context.Context, lat, lon float64) (string, error) {
	cacheKey := fmt.Sprintf("%.4f,%.4f", lat, lon)

	mu.RLock()
	if val, ok := cache[cacheKey]; ok && val != "" {
		mu.RUnlock()
		return val, nil
	}
	mu.RUnlock()

	// 1. Priority: Google Maps Geocoding API if GOOGLE_MAPS_API_KEY is configured
	if apiKey := os.Getenv("GOOGLE_MAPS_API_KEY"); apiKey != "" {
		addr, err := reverseGeocodeGoogle(lat, lon, apiKey)
		if err == nil && addr != "" {
			mu.Lock()
			cache[cacheKey] = addr
			mu.Unlock()
			return addr, nil
		}
	}

	// 2. High-precision Reverse Geocode via Nominatim (OpenStreetMap) with Indonesian language
	addr, err := reverseGeocodeNominatim(lat, lon)
	if err == nil && addr != "" {
		mu.Lock()
		cache[cacheKey] = addr
		mu.Unlock()
		return addr, nil
	}

	// 3. Fallback: Local database spatial query if offline / external network unavailable
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
			dbAddr := strings.Join(parts, ", ")

			mu.Lock()
			cache[cacheKey] = dbAddr
			mu.Unlock()
			return dbAddr, nil
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
			var dbAddr string
			if provinceName != "" {
				dbAddr = fmt.Sprintf("%s, %s, Indonesia", cityName, provinceName)
			} else {
				dbAddr = fmt.Sprintf("%s, Indonesia", cityName)
			}
			mu.Lock()
			cache[cacheKey] = dbAddr
			mu.Unlock()
			return dbAddr, nil
		}
	}

	return "", fmt.Errorf("geocoding failed: no address found")
}

func reverseGeocodeGoogle(lat, lon float64, apiKey string) (string, error) {
	url := fmt.Sprintf("https://maps.googleapis.com/maps/api/geocode/json?latlng=%f,%f&key=%s&language=id", lat, lon, apiKey)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("google maps returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var data struct {
		Status  string `json:"status"`
		Results []struct {
			FormattedAddress string `json:"formatted_address"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return "", err
	}

	if data.Status == "OK" && len(data.Results) > 0 && data.Results[0].FormattedAddress != "" {
		return data.Results[0].FormattedAddress, nil
	}

	return "", fmt.Errorf("google geocoding status: %s", data.Status)
}

func reverseGeocodeNominatim(lat, lon float64) (string, error) {
	url := fmt.Sprintf("https://nominatim.openstreetmap.org/reverse?format=json&lat=%f&lon=%f&accept-language=id", lat, lon)
	
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Adatrack-Geocoder/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("nominatim returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var data struct {
		DisplayName string `json:"display_name"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return "", err
	}

	return data.DisplayName, nil
}
