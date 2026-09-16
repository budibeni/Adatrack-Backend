package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"

	"backend/internal/config"
	"backend/internal/dbclient"
	"backend/internal/logger"
	"backend/internal/redclient"
	"backend/internal/auth"
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
	Email       string `json:"email"`
	Password    string `json:"password"`
	CompanyCode string `json:"company_code"`
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}

	if req.Email == "" || req.Password == "" || req.CompanyCode == "" {
		h.writeError(w, http.StatusBadRequest, "email, password, and company_code are required")
		return
	}

	var userID int64
	var hash, globalRole string
	var isActive, mustChange bool

	// Check master user
	err := dbclient.Pool.QueryRow(r.Context(), "SELECT id, password_hash, global_role, is_active, must_change_password FROM adatrack_gps_master.tm_users WHERE email = $1 AND deleted_at IS NULL", req.Email).Scan(&userID, &hash, &globalRole, &isActive, &mustChange)
	if err != nil {
		h.writeError(w, http.StatusUnauthorized, "Invalid credentials")
		return
	}

	if !isActive {
		h.writeError(w, http.StatusForbidden, "User inactive")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)); err != nil {
		h.writeError(w, http.StatusUnauthorized, "Invalid credentials")
		return
	}

	// Resolve role
	role := globalRole
	if globalRole != "SuperAdmin" || req.CompanyCode != "DEFAULT" {
		// Check company access
		schema := fmt.Sprintf("adatrack_gps_%s", req.CompanyCode)
		var roleOverride *string
		var companyActive bool
		err = dbclient.Pool.QueryRow(r.Context(), fmt.Sprintf("SELECT role_override, is_active FROM %s.tm_user_company_access WHERE user_id = $1 AND deleted_at IS NULL", schema), userID).Scan(&roleOverride, &companyActive)
		if err != nil {
			h.writeError(w, http.StatusForbidden, "No access to this company")
			return
		}
		if !companyActive {
			h.writeError(w, http.StatusForbidden, "Company access inactive")
			return
		}
		if roleOverride != nil && *roleOverride != "" {
			role = *roleOverride
		}
	}

	accessToken, _ := auth.GenerateToken(h.cfg, userID, req.Email, req.CompanyCode, role, 24*time.Hour)
	refreshToken, _ := auth.GenerateToken(h.cfg, userID, req.Email, req.CompanyCode, role, 7*24*time.Hour)
	
	// Audit log
	h.auditLog(r.Context(), req.CompanyCode, "LOGIN_SUCCESS", "success", userID, req.Email, role, "Login successful")

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"access_token":         accessToken,
			"refresh_token":        refreshToken,
			"must_change_password": mustChange,
		},
	})
}

func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "Invalid claims")
		return
	}
	accessToken, _ := auth.GenerateToken(h.cfg, claims.UserID, claims.Email, claims.CompanyCode, claims.Role, 24*time.Hour)
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
	
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "Invalid claims")
		return
	}
	ttl := time.Until(claims.ExpiresAt.Time)
	
	redclient.Client.Set(r.Context(), "denylist:"+tokenStr, "1", ttl)
	
	h.auditLog(r.Context(), claims.CompanyCode, "LOGOUT", "success", claims.UserID, claims.Email, claims.Role, "User logged out")
	
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func (h *Handler) writeError(w http.ResponseWriter, status int, msg string) {
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"status": "error", "message": msg})
}

func (h *Handler) auditLog(ctx context.Context, companyCode, action, outcome string, userID int64, email, role, detail string) {
	schema := "adatrack_gps_master"
	if companyCode != "DEFAULT" && companyCode != "" {
		schema = fmt.Sprintf("adatrack_gps_%s", companyCode)
	}
	
	query := fmt.Sprintf(`INSERT INTO %s.tm_audit_logs (action, outcome, actor_user_id, actor_email, actor_role, company_code, after) VALUES ($1, $2, $3, $4, $5, $6, $7)`, schema)
	
	afterJSON, _ := json.Marshal(map[string]string{"detail": detail})
	
	_, err := dbclient.Pool.Exec(ctx, query, action, outcome, userID, email, role, companyCode, afterJSON)
	if err != nil {
		logger.Log.Error("Failed to write audit log", "err", err, "action", action)
	}
}

// REST ENDPOINTS

func (h *Handler) CreateCompany(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code        string `json:"code"`
		Name        string `json:"name"`
		CountryCode string `json:"country_code"`
		Timezone    string `json:"timezone"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	claims := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	
	// Create company in master
	_, err := dbclient.Pool.Exec(r.Context(), `
		INSERT INTO adatrack_gps_master.tm_companies (code, name, country_code, timezone, business_type)
		VALUES ($1, $2, $3, $4, 'b2b')
	`, req.Code, req.Name, req.CountryCode, req.Timezone)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "Failed to create company")
		return
	}
	
	// Create user in master
	adminEmail := fmt.Sprintf("admin@%s.local", req.Code)
	hash, _ := bcrypt.GenerateFromPassword([]byte("Admin@123"), 12)
	
	var newUserID int64
	err = dbclient.Pool.QueryRow(r.Context(), `
		INSERT INTO adatrack_gps_master.tm_users (email, password_hash, global_role, must_change_password)
		VALUES ($1, $2, 'Admin', true) RETURNING id
	`, adminEmail, string(hash)).Scan(&newUserID)
	
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "Failed to create admin user")
		return
	}
	
	schema := fmt.Sprintf("adatrack_gps_%s", req.Code)
	_, err = dbclient.Pool.Exec(r.Context(), fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", schema))
	if err != nil {
		logger.Log.Error("Failed to create schema", "err", err)
	} else {
	    // Apply migrations
	    files, _ := filepath.Glob("../../database/migrations/company_pg/*.up.sql")
	    for _, file := range files {
	        sqlBytes, err := os.ReadFile(file)
	        if err == nil {
	            // Set search path for this execution
	            execSQL := fmt.Sprintf("SET search_path TO %s; %s", schema, string(sqlBytes))
	            _, err := dbclient.Pool.Exec(r.Context(), execSQL)
	            if err != nil {
	                logger.Log.Error("Migration failed", "file", file, "err", err)
	            }
	        }
	    }
	    
	    // Give admin user access
	    dbclient.Pool.Exec(r.Context(), fmt.Sprintf("INSERT INTO %s.tm_user_company_access (user_id, role_override) VALUES ($1, 'Admin')", schema), newUserID)
	}
	
	h.auditLog(r.Context(), req.Code, "COMPANY_CREATED", "success", claims.UserID, claims.Email, claims.Role, "Company "+req.Code+" created")
	h.auditLog(r.Context(), req.Code, "TENANT_PROVISIONED", "success", claims.UserID, claims.Email, claims.Role, "Tenant provisioned")
	h.auditLog(r.Context(), req.Code, "ADMIN_USER_AUTOCREATED", "success", claims.UserID, claims.Email, claims.Role, "Admin user created")

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"code": req.Code,
			"admin_user": map[string]interface{}{
				"email": adminEmail,
				"must_change_password": true,
			},
		},
	})
}

func (h *Handler) ListVehicles(w http.ResponseWriter, r *http.Request) {
	claims := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	
	type Vehicle struct {
		ID          int64  `json:"id"`
		PlateNumber string `json:"plate_number"`
		Status      string `json:"status"`
	}
	var vehicles []Vehicle
	
	var query string
	var args []interface{}
	
	if claims.Role == "Admin" || claims.Role == "SuperAdmin" {
		query = fmt.Sprintf("SELECT id, plate_number, status FROM %s.tm_vehicles WHERE deleted_at IS NULL", schema)
	} else {
		query = fmt.Sprintf("SELECT v.id, v.plate_number, v.status FROM %s.tm_vehicles v JOIN %s.tm_user_vehicles uv ON v.id = uv.vehicle_id WHERE uv.user_id = $1 AND v.deleted_at IS NULL", schema, schema)
		args = append(args, claims.UserID)
	}
	
	rows, err := dbclient.Pool.Query(r.Context(), query, args...)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var v Vehicle
			rows.Scan(&v.ID, &v.PlateNumber, &v.Status)
			vehicles = append(vehicles, v)
		}
	}
	
	// Enrich with Redis Live State
	var enriched []map[string]interface{}
	for _, v := range vehicles {
		vMap := map[string]interface{}{
			"id": v.ID,
			"plate_number": v.PlateNumber,
			"db_status": v.Status,
		}
		
		key := fmt.Sprintf("telemetry:live:%s:%d", claims.CompanyCode, v.ID)
		val, err := redclient.Client.Get(r.Context(), key).Result()
		if err == nil && val != "" {
			var state map[string]interface{}
			if err := json.Unmarshal([]byte(val), &state); err == nil {
				vMap["live_state"] = state
			}
		} else {
			vMap["live_state"] = nil
		}
		enriched = append(enriched, vMap)
	}
	
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "success",
		"data": enriched,
	})
}


func (h *Handler) GetVehicle(w http.ResponseWriter, r *http.Request) {
	_ = chi.URLParam(r, "id")
	// Must check RBAC tm_user_vehicles logic here.
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{"id": chi.URLParam(r, "id"), "position": "live_data"},
	})
}

func (h *Handler) GetVehicleHistory(w http.ResponseWriter, r *http.Request) {
	_ = chi.URLParam(r, "id")
	// Fetch from th_telemetry_logs
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "success",
		"data": []interface{}{},
	})
}

