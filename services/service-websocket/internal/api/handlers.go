package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
	"strings"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"

	"backend/internal/config"
	"backend/internal/dbclient"
	"backend/internal/logger"
	"backend/internal/redclient"
	"backend/service-websocket/internal/auth"
	"backend/service-websocket/internal/ws"
)

type Handler struct {
	cfg *config.Config
	hub *ws.Hub
}

func NewHandler(cfg *config.Config, hub *ws.Hub) *Handler {
	return &Handler{cfg: cfg, hub: hub}
}

type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	var userID int64
	var hash, companyCode, role string
	
	// Example query to master db (tenant logic needs more refinement)
	// PRD says users might be in tm_users or tm_users_b2c depending on company, but for now assuming tm_users.
	err := dbclient.Pool.QueryRow(r.Context(), "SELECT user_id, password_hash, default_company_code, role FROM tm_users WHERE email = $1", req.Email).Scan(&userID, &hash, &companyCode, &role)
	if err != nil {
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)); err != nil {
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}

	accessToken, _ := auth.GenerateToken(h.cfg, userID, req.Email, companyCode, role, 15*time.Minute)
	refreshToken, _ := auth.GenerateToken(h.cfg, userID, req.Email, companyCode, role, 7*24*time.Hour)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "success",
		"data": map[string]string{
			"access_token":  accessToken,
			"refresh_token": refreshToken,
		},
	})
}

func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	// Simplified refresh logic
	claims, _ := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	accessToken, _ := auth.GenerateToken(h.cfg, claims.UserID, claims.Email, claims.CompanyCode, claims.Role, 15*time.Minute)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "success",
		"data": map[string]string{
			"access_token": accessToken,
		},
	})
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	authHeader := r.Header.Get("Authorization")
	tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
	
	claims, _ := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	ttl := time.Until(claims.ExpiresAt.Time)
	
	// Add to denylist
	redclient.RDB.Set(r.Context(), "denylist:"+tokenStr, "1", ttl)
	
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func (h *Handler) CreateCompany(w http.ResponseWriter, r *http.Request) {
	// Implement tenant auto-provisioning
	json.NewEncoder(w).Encode(map[string]string{"status": "success", "message": "Company created"})
}

func (h *Handler) ListVehicles(w http.ResponseWriter, r *http.Request) {
	// Add live state enrichment from Redis
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "success", "data": []string{}})
}

func (h *Handler) GetVehicle(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "success", "data": map[string]string{"id": id}})
}

func (h *Handler) GetVehicleHistory(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "success", "data": map[string]string{"id": id, "history": "[]"}})
}
