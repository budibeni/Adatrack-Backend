package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"backend/internal/auth"
	"backend/internal/config"
	"backend/service-websocket/internal/ws"
)

func TestHandler_LoginValidation(t *testing.T) {
	cfg := &config.Config{JWTSecret: "test_secret"}
	hub := ws.NewHub(cfg)
	h := NewHandler(cfg, hub)

	// 1. Empty body
	req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader([]byte("{}")))
	w := httptest.NewRecorder()
	h.Login(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", w.Code)
	}
	var errResp ErrorResponse
	json.NewDecoder(w.Body).Decode(&errResp)
	if errResp.ErrorCode != "VALIDATION_ERROR" {
		t.Errorf("expected error_code VALIDATION_ERROR, got %s", errResp.ErrorCode)
	}

	// 2. Malformed JSON
	reqBad := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader([]byte("not-json")))
	wBad := httptest.NewRecorder()
	h.Login(wBad, reqBad)
	if wBad.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 for bad json, got %d", wBad.Code)
	}
	var errBad ErrorResponse
	json.NewDecoder(wBad.Body).Decode(&errBad)
	if errBad.ErrorCode != "INVALID_JSON" {
		t.Errorf("expected error_code INVALID_JSON, got %s", errBad.ErrorCode)
	}
}

func TestHandler_UnauthorizedWithoutToken(t *testing.T) {
	cfg := &config.Config{JWTSecret: "test_secret"}
	hub := ws.NewHub(cfg)
	router := SetupRouter(cfg, hub)

	// 1. GET /api/v1/vehicles without token
	req := httptest.NewRequest("GET", "/api/v1/vehicles", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401 without auth header, got %d", w.Code)
	}

	// 2. GET /api/v1/vehicles/1 with invalid token
	reqInvalid := httptest.NewRequest("GET", "/api/v1/vehicles/1", nil)
	reqInvalid.Header.Set("Authorization", "Bearer invalid-token-xyz")
	wInvalid := httptest.NewRecorder()
	router.ServeHTTP(wInvalid, reqInvalid)

	if wInvalid.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401 for invalid token, got %d", wInvalid.Code)
	}
}

func TestHandler_PlatformOnlyMiddleware(t *testing.T) {
	cfg := &config.Config{JWTSecret: "test_secret"}
	hub := ws.NewHub(cfg)
	router := SetupRouter(cfg, hub)

	// User with role Admin (not SuperAdmin) trying to call POST /api/v1/companies
	token, _ := auth.GenerateToken(cfg, 10, "admin@company.com", "COMPANY_A", "Admin", []string{"*"}, 1*time.Hour)

	req := httptest.NewRequest("POST", "/api/v1/companies", bytes.NewReader([]byte(`{"code":"NEWCO","name":"New Company"}`)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected status 403 PLATFORM_ONLY for non-SuperAdmin, got %d", w.Code)
	}
}

func TestHandler_ClaimsContext(t *testing.T) {
	cfg := &config.Config{JWTSecret: "test_secret"}
	hub := ws.NewHub(cfg)
	h := NewHandler(cfg, hub)

	claims := &auth.Claims{
		UserID:      1,
		Email:       "user@test.local",
		CompanyCode: "TESTCO",
		Role:        "Admin",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
		},
	}

	// Refresh with valid token
	token, err := auth.GenerateToken(cfg, claims.UserID, claims.Email, claims.CompanyCode, claims.Role, []string{"*"}, 1*time.Hour)
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}

	body, _ := json.Marshal(map[string]string{"refresh_token": token})
	req := httptest.NewRequest("POST", "/api/v1/auth/refresh", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), auth.ClaimsKey, claims)
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	h.Refresh(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200 on refresh, got %d, body: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "success" {
		t.Errorf("expected status success, got %v", resp["status"])
	}
	data := resp["data"].(map[string]interface{})
	if data["access_token"] == "" {
		t.Errorf("expected access_token to be non-empty")
	}
}

func TestHandler_MobileTrackingAuth(t *testing.T) {
	cfg := &config.Config{JWTSecret: "test_secret"}
	hub := ws.NewHub(cfg)
	h := NewHandler(cfg, hub)

	// 1. Without Authorization header -> 401
	req1 := httptest.NewRequest("POST", "/api/v1/tracking/mobile", bytes.NewReader([]byte(`{"device_id":"TEST-1"}`)))
	w1 := httptest.NewRecorder()
	h.HandleMobileTracking(w1, req1)
	if w1.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without auth header, got %d", w1.Code)
	}

	// 2. With legacy X-Device-Token header -> 401 rejected
	req2 := httptest.NewRequest("POST", "/api/v1/tracking/mobile", bytes.NewReader([]byte(`{"device_id":"TEST-1"}`)))
	req2.Header.Set("X-Device-Token", "adatrack-mobile-secret-2026")
	w2 := httptest.NewRecorder()
	h.HandleMobileTracking(w2, req2)
	if w2.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with X-Device-Token, got %d", w2.Code)
	}
	var resp2 map[string]interface{}
	json.NewDecoder(w2.Body).Decode(&resp2)
	if msg, ok := resp2["message"].(string); !ok || !strings.Contains(msg, "Authorization: Bearer") {
		t.Errorf("expected rejection message referencing Bearer, got: %v", resp2["message"])
	}

	// 3. With invalid Bearer token -> 401
	req3 := httptest.NewRequest("POST", "/api/v1/tracking/mobile", bytes.NewReader([]byte(`{"device_id":"TEST-1"}`)))
	req3.Header.Set("Authorization", "Bearer invalid.jwt.token")
	w3 := httptest.NewRecorder()
	h.HandleMobileTracking(w3, req3)
	if w3.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for invalid Bearer token, got %d", w3.Code)
	}

	// 4. With empty Bearer token -> 401
	req4 := httptest.NewRequest("POST", "/api/v1/tracking/mobile", bytes.NewReader([]byte(`{"device_id":"TEST-1"}`)))
	req4.Header.Set("Authorization", "Bearer ")
	w4 := httptest.NewRecorder()
	h.HandleMobileTracking(w4, req4)
	if w4.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for empty Bearer token, got %d", w4.Code)
	}

	// 5. With valid Bearer token, but malformed JSON body -> passes auth, fails at body validation (400)
	validToken, err := auth.GenerateToken(cfg, 1, "driver@test.local", "DEMOCO", "Driver", []string{"driver:track"}, 1*time.Hour)
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}
	req5 := httptest.NewRequest("POST", "/api/v1/tracking/mobile", bytes.NewReader([]byte(`invalid-json`)))
	req5.Header.Set("Authorization", "Bearer "+validToken)
	w5 := httptest.NewRecorder()
	h.HandleMobileTracking(w5, req5)
	if w5.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad json body after successful auth, got %d", w5.Code)
	}
}

