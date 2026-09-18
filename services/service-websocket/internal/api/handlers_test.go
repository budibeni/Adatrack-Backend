package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	token, _ := auth.GenerateToken(cfg, 10, "admin@company.com", "COMPANY_A", "Admin", 1*time.Hour)

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
	token, err := auth.GenerateToken(cfg, claims.UserID, claims.Email, claims.CompanyCode, claims.Role, 1*time.Hour)
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
