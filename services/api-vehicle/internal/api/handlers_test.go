package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"backend/internal/auth"
	"backend/internal/config"
)

func TestApiVehicle_AuthRequired(t *testing.T) {
	cfg := &config.Config{JWTSecret: "test_secret"}
	router := SetupRouter(cfg, nil)

	endpoints := []struct {
		method string
		path   string
	}{
		{"GET", "/api/v1/vehicles"},
		{"POST", "/api/v1/vehicles"},
		{"GET", "/api/v1/geofences"},
		{"POST", "/api/v1/geofences"},
		{"GET", "/api/v1/routes"},
		{"POST", "/api/v1/routes"},
		{"GET", "/api/v1/speed-configs"},
		{"POST", "/api/v1/speed-configs"},
		{"GET", "/api/v1/alerts"},
		{"GET", "/api/v1/notification-preferences"},
	}

	for _, ep := range endpoints {
		req := httptest.NewRequest(ep.method, ep.path, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("[%s %s] expected 401 Unauthorized without token, got %d", ep.method, ep.path, w.Code)
		}
	}
}

func TestApiVehicle_Validation(t *testing.T) {
	cfg := &config.Config{JWTSecret: "test_secret"}
	router := SetupRouter(cfg, nil)

	token, _ := auth.GenerateToken(cfg, 1, "admin@test.local", "DEFAULT", "Admin", 1*time.Hour)

	// 1. Create vehicle with empty body
	req := httptest.NewRequest("POST", "/api/v1/vehicles", bytes.NewReader([]byte("{}")))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty vehicle body, got %d", w.Code)
	}

	var errResp ErrorResponse
	json.NewDecoder(w.Body).Decode(&errResp)
	if errResp.ErrorCode != "VALIDATION_ERROR" {
		t.Errorf("expected VALIDATION_ERROR, got %s", errResp.ErrorCode)
	}

	// 2. Create geofence with empty body
	reqGeo := httptest.NewRequest("POST", "/api/v1/geofences", bytes.NewReader([]byte("{}")))
	reqGeo.Header.Set("Authorization", "Bearer "+token)
	reqGeo.Header.Set("Content-Type", "application/json")
	wGeo := httptest.NewRecorder()
	router.ServeHTTP(wGeo, reqGeo)

	if wGeo.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty geofence body, got %d", wGeo.Code)
	}

	// 3. Create route with empty body
	reqRoute := httptest.NewRequest("POST", "/api/v1/routes", bytes.NewReader([]byte("{}")))
	reqRoute.Header.Set("Authorization", "Bearer "+token)
	reqRoute.Header.Set("Content-Type", "application/json")
	wRoute := httptest.NewRecorder()
	router.ServeHTTP(wRoute, reqRoute)

	if wRoute.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty route body, got %d", wRoute.Code)
	}
}

func TestApiVehicle_RBACForbidden(t *testing.T) {
	cfg := &config.Config{JWTSecret: "test_secret"}
	router := SetupRouter(cfg, nil)

	// Driver user trying to create vehicle (requires Admin/Manager)
	driverToken, _ := auth.GenerateToken(cfg, 99, "driver@test.local", "DEFAULT", "Driver", 1*time.Hour)

	req := httptest.NewRequest("POST", "/api/v1/vehicles", bytes.NewReader([]byte(`{"imei":"123","plate_number":"B1234XYZ"}`)))
	req.Header.Set("Authorization", "Bearer "+driverToken)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for Driver role creating vehicle, got %d", w.Code)
	}
}

func TestApiVehicle_Healthz(t *testing.T) {
	cfg := &config.Config{JWTSecret: "test_secret"}
	router := SetupRouter(cfg, nil)

	req := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 from /healthz, got %d", w.Code)
	}
}
