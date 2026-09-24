package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"

	"backend/internal/auth"
	"backend/internal/config"
	"backend/internal/dbclient"
	"backend/internal/logger"
	"backend/internal/redclient"
	"backend/internal/tenant"
	"backend/internal/utils"
	"backend/service-websocket/internal/ws"
)

type Handler struct {
	cfg *config.Config
	hub *ws.Hub
}

func NewHandler(cfg *config.Config, hub *ws.Hub) *Handler {
	return &Handler{cfg: cfg, hub: hub}
}

type ErrorResponse struct {
	Status    string `json:"status"`
	ErrorCode string `json:"error_code"`
	Message   string `json:"message"`
	Timestamp string `json:"timestamp"`
}

func (h *Handler) writeError(w http.ResponseWriter, status int, errorCode, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(ErrorResponse{
		Status:    "error",
		ErrorCode: errorCode,
		Message:   msg,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
}

func (h *Handler) writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func (h *Handler) auditLog(ctx context.Context, companyCode, action, outcome string, userID int64, email, role, detail string) {
	if dbclient.Pool == nil {
		return
	}
	
	// Global Audit Log for Super Admin
	globalQuery := `INSERT INTO adatrack_gps_master.tm_global_audit_logs (company_code, actor_email, actor_role, action, outcome, detail) VALUES ($1, $2, $3, $4, $5, $6)`
	_, err := dbclient.Pool.Exec(ctx, globalQuery, companyCode, email, role, action, outcome, detail)
	if err != nil {
		logger.Log.Error("Failed to write global audit log", "err", err)
	}

	// Tenant Audit Log (only if it's a valid tenant)
	if companyCode == "DEFAULT" || companyCode == "" || !tenant.IsValidCompanyCode(companyCode) {
		return
	}

	schema := fmt.Sprintf("adatrack_gps_%s", companyCode)
	query := fmt.Sprintf(`INSERT INTO %s.th_audit_logs (action, outcome, actor_user_id, actor_email, actor_role, after_data) VALUES ($1, $2, $3, $4, $5, $6)`, schema)
	afterJSON, _ := json.Marshal(map[string]string{"detail": detail})

	_, err = dbclient.Pool.Exec(ctx, query, action, outcome, userID, email, role, afterJSON)
	if err != nil {
		logger.Log.Error("Failed to write tenant audit log", "err", err, "action", action)
	}
}

type LoginRequest struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	CompanyCode string `json:"company_code"`
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid JSON body")
		return
	}

	if req.Email == "" || req.Password == "" {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "email and password are required")
		return
	}

	if req.CompanyCode != "" && !tenant.IsValidCompanyCode(req.CompanyCode) {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid company_code format")
		return
	}

	var userID int64
	var hash string
	var isActive, mustChange bool
	var fullName *string

	err := dbclient.Pool.QueryRow(r.Context(),
		"SELECT id, full_name, password_hash, is_active, must_change_password FROM adatrack_gps_master.tm_users WHERE email = $1 AND deleted_at IS NULL",
		req.Email).Scan(&userID, &fullName, &hash, &isActive, &mustChange)
	if err != nil {
		h.writeError(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "Invalid credentials")
		return
	}

	if !isActive {
		h.writeError(w, http.StatusForbidden, "USER_INACTIVE", "User account is inactive")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)); err != nil {
		h.writeError(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "Invalid credentials")
		return
	}

	if req.CompanyCode == "" {
		rows, err := dbclient.Pool.Query(r.Context(), "SELECT code FROM adatrack_gps_master.tm_companies WHERE deleted_at IS NULL ")
		if err != nil {
			h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to query companies")
			return
		}
		
		var codes []string
		for rows.Next() {
			var code string
			if err := rows.Scan(&code); err == nil {
				codes = append(codes, code)
			}
		}
		rows.Close()

		if len(codes) > 0 {
			var queryParts []string
			for _, code := range codes {
				schema := fmt.Sprintf("adatrack_gps_%s", strings.ToLower(code))
				queryParts = append(queryParts, fmt.Sprintf("SELECT '%s' as company_code FROM %s.tm_user_company_access WHERE user_id = $1 AND deleted_at IS NULL AND is_active = true", code, schema))
			}
			
			query := strings.Join(queryParts, " UNION ALL ")
			rowsAccess, err := dbclient.Pool.Query(r.Context(), query, userID)
			if err == nil {
				var userCompanies []string
				for rowsAccess.Next() {
					var comp string
					if err := rowsAccess.Scan(&comp); err == nil {
						found := false
						for _, c := range userCompanies {
							if c == comp {
								found = true
								break
							}
						}
						if !found {
							userCompanies = append(userCompanies, comp)
						}
					}
				}
				rowsAccess.Close()

				if len(userCompanies) == 1 {
					req.CompanyCode = userCompanies[0]
				} else if len(userCompanies) > 1 {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					json.NewEncoder(w).Encode(map[string]interface{}{
						"status": "multiple_companies",
						"data": map[string]interface{}{
							"companies": userCompanies,
						},
					})
					return
				}
			}
		}
		
		if req.CompanyCode == "" {
			h.writeError(w, http.StatusForbidden, "COMPANY_ACCESS_DENIED", "No access to any company")
			return
		}
	}

	schema := fmt.Sprintf("adatrack_gps_%s", strings.ToLower(req.CompanyCode))

	var roleCode *string
	var companyActive bool
	err = dbclient.Pool.QueryRow(r.Context(),
		fmt.Sprintf("SELECT role_code, is_active FROM %s.tm_user_company_access WHERE user_id = $1 AND deleted_at IS NULL", schema),
		userID).Scan(&roleCode, &companyActive)
	if err != nil {
		h.writeError(w, http.StatusForbidden, "COMPANY_ACCESS_DENIED", "No access to this company")
		return
	}

	if !companyActive {
		h.writeError(w, http.StatusForbidden, "COMPANY_ACCESS_INACTIVE", "Access to this company is inactive")
		return
	}

	role := ""
	if roleCode != nil {
		role = *roleCode
	}

	accessToken, err := auth.GenerateToken(h.cfg, userID, req.Email, req.CompanyCode, role, 24*time.Hour)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "TOKEN_GENERATION_FAILED", "Failed to generate access token")
		return
	}
	rb := make([]byte, 32)
	rand.Read(rb)
	refreshToken := hex.EncodeToString(rb)
	if redclient.Client != nil {
		claimsData, _ := json.Marshal(map[string]interface{}{
			"user_id":      userID,
			"email":        req.Email,
			"company_code": req.CompanyCode,
			"role":         role,
		})
		redclient.Client.Set(r.Context(), "refresh_token:"+refreshToken, string(claimsData), 7*24*time.Hour)
	}

	h.auditLog(r.Context(), req.CompanyCode, "LOGIN_SUCCESS", "success", userID, req.Email, role, "Login successful")

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"access_token":         accessToken,
			"refresh_token":        refreshToken,
			"must_change_password": mustChange,
			"role":                 role,
			"email":                req.Email,
			"name":                 func() string { if fullName != nil { return *fullName }; return "" }(),
			"company_code":         req.CompanyCode,
		},
	})
}

func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req map[string]string
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid JSON body")
		return
	}

	refreshToken := req["refresh_token"]
	if refreshToken == "" {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "refresh_token is required")
		return
	}

	var claimsData struct {
		UserID      int64  `json:"user_id"`
		Email       string `json:"email"`
		CompanyCode string `json:"company_code"`
		Role        string `json:"role"`
	}

	if redclient.Client != nil {
		val, err := redclient.Client.Get(r.Context(), "refresh_token:"+refreshToken).Result()
		if err != nil || val == "" {
			h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid or expired refresh token")
			return
		}

		// Invalidate old refresh token (rotate)
		redclient.Client.Del(r.Context(), "refresh_token:"+refreshToken)

		if err := json.Unmarshal([]byte(val), &claimsData); err != nil {
			h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid token data")
			return
		}
	} else {
		// Fallback when Redis is not configured (e.g. during unit tests or standalone)
		claims, err := auth.ValidateToken(h.cfg, refreshToken)
		if err != nil {
			h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid or expired refresh token")
			return
		}
		claimsData.UserID = claims.UserID
		claimsData.Email = claims.Email
		claimsData.CompanyCode = claims.CompanyCode
		claimsData.Role = claims.Role
	}

	accessToken, err := auth.GenerateToken(h.cfg, claimsData.UserID, claimsData.Email, claimsData.CompanyCode, claimsData.Role, 24*time.Hour)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "TOKEN_GENERATION_FAILED", "Failed to generate access token")
		return
	}

	rb := make([]byte, 32)
	rand.Read(rb)
	newRefreshToken := hex.EncodeToString(rb)
	if redclient.Client != nil {
		claimsBytes, _ := json.Marshal(claimsData)
		redclient.Client.Set(r.Context(), "refresh_token:"+newRefreshToken, string(claimsBytes), 7*24*time.Hour)
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
		"data": map[string]string{
			"access_token":  accessToken,
			"refresh_token": newRefreshToken,
		},
	})
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid authentication claims")
		return
	}
	
	ttl := time.Until(claims.ExpiresAt.Time)
	if ttl <= 0 {
		ttl = 1 * time.Hour
	}

	if redclient.Client != nil {
		if claims.ID != "" {
			redclient.Client.Set(r.Context(), "denylist:"+claims.ID, "1", ttl)
		}
		
		// If client also sends refresh_token, revoke it
		var req map[string]string
		if err := json.NewDecoder(r.Body).Decode(&req); err == nil && req["refresh_token"] != "" {
			redclient.Client.Del(r.Context(), "refresh_token:"+req["refresh_token"])
		}
	}

	h.auditLog(r.Context(), claims.CompanyCode, "LOGOUT", "success", claims.UserID, claims.Email, claims.Role, "User logged out")

	h.writeJSON(w, http.StatusOK, map[string]string{
		"status":  "success",
		"message": "Successfully logged out",
	})
}

func (h *Handler) CreateCompany(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code        string `json:"code"`
		Name        string `json:"name"`
		CountryCode string `json:"country_code"`
		Timezone    string `json:"timezone"`
		Password    string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid JSON payload")
		return
	}
	if req.Code == "" || req.Name == "" {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "code and name are required")
		return
	}
	req.Code = strings.ToUpper(req.Code)
	if !tenant.IsValidCompanyCode(req.Code) {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid company code format")
		return
	}

	claims := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	
	if req.Password == "" {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Verification password is required")
		return
	}
	var currentHash string
	if err := dbclient.Pool.QueryRow(r.Context(), "SELECT password_hash FROM adatrack_gps_master.tm_users WHERE id = $1", claims.UserID).Scan(&currentHash); err != nil || bcrypt.CompareHashAndPassword([]byte(currentHash), []byte(req.Password)) != nil {
		h.writeError(w, http.StatusForbidden, "INVALID_PASSWORD", "Invalid verification password")
		return
	}

	if req.Timezone == "" {
		req.Timezone = "UTC"
	}
	if req.CountryCode == "" {
		req.CountryCode = "ID"
	}
	
	// Generate random password
	rb := make([]byte, 6)
	rand.Read(rb)
	randomPassword := hex.EncodeToString(rb)

	tx, err := dbclient.Pool.Begin(r.Context())
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to start transaction")
		return
	}
	defer tx.Rollback(r.Context())

	// 1. Create company record in master
	_, err = tx.Exec(r.Context(), `
		INSERT INTO adatrack_gps_master.tm_companies (code, name, country_code, timezone, business_type)
		VALUES ($1, $2, $3, $4, 'b2b')
		ON CONFLICT (code) DO NOTHING
	`, req.Code, req.Name, req.CountryCode, req.Timezone)
	if err != nil {
		logger.Log.Error("Failed to create company", "err", err)
		h.writeError(w, http.StatusInternalServerError, "COMPANY_CREATION_FAILED", "Failed to insert company into master")
		return
	}

	// 2. Create admin user in master
	adminEmail := fmt.Sprintf("admin@%s.local", strings.ToLower(req.Code))
	hash, _ := bcrypt.GenerateFromPassword([]byte(randomPassword), 12)

	var newUserID int64
	err = tx.QueryRow(r.Context(), `
		INSERT INTO adatrack_gps_master.tm_users (email, full_name, password_hash, must_change_password)
		VALUES ($1, $2, $3, true)
		ON CONFLICT (email) DO UPDATE SET password_hash = EXCLUDED.password_hash, must_change_password = true
		RETURNING id
	`, adminEmail, "Admin", string(hash)).Scan(&newUserID)
	if err != nil {
		logger.Log.Error("Failed to create admin user", "err", err)
		h.writeError(w, http.StatusInternalServerError, "USER_CREATION_FAILED", "Failed to create tenant admin user")
		return
	}

	// 3. Create schema
	schema := fmt.Sprintf("adatrack_gps_%s", req.Code)
	_, err = tx.Exec(r.Context(), fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", schema))
	if err != nil {
		logger.Log.Error("Failed to create schema", "err", err)
		h.writeError(w, http.StatusInternalServerError, "SCHEMA_CREATION_FAILED", "Failed to create company schema")
		return
	}

	// 4. Apply migrations to company schema
	var migrationDir string
	candidates := []string{
		"../../database/migrations/company_pg",
		"database/migrations/company_pg",
		"/app/database/migrations/company_pg",
	}
	for _, c := range candidates {
		if _, statErr := os.Stat(c); statErr == nil {
			migrationDir = c
			break
		}
	}
	if migrationDir != "" {
		files, _ := filepath.Glob(filepath.Join(migrationDir, "*.up.sql"))
		sort.Strings(files)
		for _, file := range files {
			sqlBytes, err := os.ReadFile(file)
			if err == nil {
				execSQL := fmt.Sprintf("SET search_path TO %s, public; %s", schema, string(sqlBytes))
				_, err := tx.Exec(r.Context(), execSQL)
				if err != nil {
					logger.Log.Error("Migration error in company schema", "file", file, "err", err)
				}
			}
		}
	}

	// 5. Grant admin access
	_, err = tx.Exec(r.Context(), fmt.Sprintf(`
		INSERT INTO %s.tm_user_company_access (user_id, role_code, is_active)
		VALUES ($1, 'ADMIN', true)
		ON CONFLICT DO NOTHING
	`, schema), newUserID)
	if err != nil {
		logger.Log.Error("Failed to grant admin access", "err", err)
		h.writeError(w, http.StatusInternalServerError, "ACCESS_CREATION_FAILED", "Failed to grant admin access to tenant: " + err.Error())
		return
	}
	
	if err := tx.Commit(r.Context()); err != nil {
		logger.Log.Error("Failed to commit tenant provisioning", "err", err)
		h.writeError(w, http.StatusInternalServerError, "TX_COMMIT_FAILED", "Failed to finalize tenant provisioning")
		return
	}

	h.auditLog(r.Context(), req.Code, "COMPANY_CREATED", "success", claims.UserID, claims.Email, claims.Role, "Company "+req.Code+" created")
	h.auditLog(r.Context(), req.Code, "TENANT_PROVISIONED", "success", claims.UserID, claims.Email, claims.Role, "Tenant provisioned")
	h.auditLog(r.Context(), req.Code, "ADMIN_USER_AUTOCREATED", "success", claims.UserID, claims.Email, claims.Role, "Admin user created: "+adminEmail)

	h.writeJSON(w, http.StatusCreated, map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"code": req.Code,
			"name": req.Name,
			"admin_user": map[string]interface{}{
				"email":                adminEmail,
				"password":             randomPassword,
				"must_change_password": true,
			},
		},
	})
}

type VehicleItem struct {
	ID          int64       `json:"id"`
	IMEI        string      `json:"imei"`
	PlateNumber string      `json:"plate_number"`
	Make        string      `json:"make"`
	Model       string      `json:"model"`
	Status      string      `json:"status"`
	OdometerKM  float64     `json:"odometer_km"`
	EngineHours float64     `json:"engine_hours"`
	LiveState   interface{} `json:"live_state"`
}

func (h *Handler) ListVehicles(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)

	// Pagination
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 200 {
		limit = 50
	}
	offset := (page - 1) * limit

	var total int
	var query string
	var countQuery string
	var args []interface{}

	if claims.Role == "Admin" || claims.Role == "SuperAdmin" || claims.Role == "Manager" {
		countQuery = fmt.Sprintf("SELECT COUNT(*) FROM %s.tm_vehicles WHERE deleted_at IS NULL", schema)
		query = fmt.Sprintf(`
			SELECT id, imei, COALESCE(plate_number, ''), COALESCE(make, ''), COALESCE(model, ''), status, COALESCE(odometer_km, 0), COALESCE(engine_hours, 0)
			FROM %s.tm_vehicles
			WHERE deleted_at IS NULL
			ORDER BY id ASC LIMIT $1 OFFSET $2
		`, schema)
		args = append(args, limit, offset)
	} else {
		countQuery = fmt.Sprintf(`
			SELECT COUNT(*) FROM %s.tm_vehicles v 
			JOIN %s.tm_user_vehicles uv ON v.id = uv.vehicle_id 
			WHERE uv.user_id = $1 AND v.deleted_at IS NULL
		`, schema, schema)
		query = fmt.Sprintf(`
			SELECT v.id, v.imei, COALESCE(v.plate_number, ''), COALESCE(v.make, ''), COALESCE(v.model, ''), v.status, COALESCE(v.odometer_km, 0), COALESCE(v.engine_hours, 0)
			FROM %s.tm_vehicles v
			JOIN %s.tm_user_vehicles uv ON v.id = uv.vehicle_id
			WHERE uv.user_id = $1 AND v.deleted_at IS NULL
			ORDER BY v.id ASC LIMIT $2 OFFSET $3
		`, schema, schema)
		args = append(args, claims.UserID, limit, offset)
	}

	router := tenant.NewReadRouter(claims.CompanyCode)
	if claims.Role == "Admin" || claims.Role == "SuperAdmin" || claims.Role == "Manager" {
		_ = router.QueryRow(r.Context(), countQuery).Scan(&total)
	} else {
		_ = router.QueryRow(r.Context(), countQuery, claims.UserID).Scan(&total)
	}

	rows, err := router.Query(r.Context(), query, args...)
	if err != nil {
		logger.Log.Error("Failed to query vehicles", "err", err)
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to retrieve vehicles")
		return
	}
	defer rows.Close()

	var vehicles []VehicleItem
	for rows.Next() {
		var v VehicleItem
		if err := rows.Scan(&v.ID, &v.IMEI, &v.PlateNumber, &v.Make, &v.Model, &v.Status, &v.OdometerKM, &v.EngineHours); err == nil {
			vehicles = append(vehicles, v)
		}
	}

	// Enrich with Redis live state: adatrack_gps:<tenant>:vehicle:state:<imei>
	enriched := make([]VehicleItem, 0, len(vehicles))
	for _, v := range vehicles {
		if redclient.Client != nil && v.IMEI != "" {
			key := fmt.Sprintf("adatrack_gps:%s:vehicle:state:%s", claims.CompanyCode, v.IMEI)
			val, err := redclient.Client.Get(r.Context(), key).Result()
			if err == nil && val != "" {
				var state map[string]interface{}
				if err := json.Unmarshal([]byte(val), &state); err == nil {
					v.LiveState = state
				}
			}
		}
		enriched = append(enriched, v)
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
		"data":   enriched,
		"pagination": map[string]interface{}{
			"page":  page,
			"limit": limit,
			"total": total,
		},
	})
}

func (h *Handler) GetVehicle(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}

	idStr := chi.URLParam(r, "id")
	vehicleID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_ID", "Vehicle ID must be a valid integer")
		return
	}

	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)

	// RBAC row-level: non-admin must have vehicle assigned
	if claims.Role != "Admin" && claims.Role != "SuperAdmin" && claims.Role != "Manager" {
		var exists bool
		router := tenant.NewReadRouter(claims.CompanyCode)
		err := router.QueryRow(r.Context(),
			fmt.Sprintf("SELECT EXISTS(SELECT 1 FROM %s.tm_user_vehicles WHERE user_id = $1 AND vehicle_id = $2)", schema),
			claims.UserID, vehicleID).Scan(&exists)
		if err != nil || !exists {
			h.writeError(w, http.StatusForbidden, "UNAUTHORIZED_VEHICLE", "Access to vehicle not authorized")
			return
		}
	}

	var v VehicleItem
	router := tenant.NewReadRouter(claims.CompanyCode)
	err = router.QueryRow(r.Context(), fmt.Sprintf(`
		SELECT id, imei, COALESCE(plate_number, ''), COALESCE(make, ''), COALESCE(model, ''), status, COALESCE(odometer_km, 0), COALESCE(engine_hours, 0)
		FROM %s.tm_vehicles
		WHERE id = $1 AND deleted_at IS NULL
	`, schema), vehicleID).Scan(&v.ID, &v.IMEI, &v.PlateNumber, &v.Make, &v.Model, &v.Status, &v.OdometerKM, &v.EngineHours)
	if err != nil {
		h.writeError(w, http.StatusNotFound, "VEHICLE_NOT_FOUND", fmt.Sprintf("Vehicle with ID %d not found", vehicleID))
		return
	}

	// Enrich with Redis live state
	if redclient.Client != nil && v.IMEI != "" {
		key := fmt.Sprintf("adatrack_gps:%s:vehicle:state:%s", claims.CompanyCode, v.IMEI)
		val, err := redclient.Client.Get(r.Context(), key).Result()
		if err == nil && val != "" {
			var state map[string]interface{}
			if err := json.Unmarshal([]byte(val), &state); err == nil {
				v.LiveState = state
			}
		}
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
		"data":   v,
	})
}

type TelemetryHistoryItem struct {
	ID           int64     `json:"id"`
	VehicleID    int64     `json:"vehicle_id"`
	IMEI         string    `json:"imei"`
	Lat          float64   `json:"lat"`
	Lon          float64   `json:"lon"`
	Speed        float64   `json:"speed"`
	Heading      *float64  `json:"heading,omitempty"`
	Altitude     *float64  `json:"altitude,omitempty"`
	ACCStatus    int16     `json:"acc_status"`
	BatteryLevel *float64  `json:"battery_level,omitempty"`
	Satellites   int       `json:"satellites"`
	GSMSignal    int       `json:"gsm_signal"`
	Timestamp    time.Time `json:"timestamp"`
}

func (h *Handler) GetVehicleHistory(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}

	idStr := chi.URLParam(r, "id")
	vehicleID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_ID", "Vehicle ID must be a valid integer")
		return
	}

	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)

	// RBAC row-level
	if claims.Role != "Admin" && claims.Role != "SuperAdmin" && claims.Role != "Manager" {
		var exists bool
		router := tenant.NewReadRouter(claims.CompanyCode)
		err := router.QueryRow(r.Context(),
			fmt.Sprintf("SELECT EXISTS(SELECT 1 FROM %s.tm_user_vehicles WHERE user_id = $1 AND vehicle_id = $2)", schema),
			claims.UserID, vehicleID).Scan(&exists)
		if err != nil || !exists {
			h.writeError(w, http.StatusForbidden, "UNAUTHORIZED_VEHICLE", "Access to vehicle history not authorized")
			return
		}
	}

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 1000 {
		limit = 100
	}
	offset := (page - 1) * limit

	startStr := r.URL.Query().Get("start")
	endStr := r.URL.Query().Get("end")

	var query string
	var countQuery string
	var args []interface{}
	args = append(args, vehicleID)

	if startStr != "" && endStr != "" {
		startTime, err1 := time.Parse(time.RFC3339, startStr)
		endTime, err2 := time.Parse(time.RFC3339, endStr)
		if err1 == nil && err2 == nil {
			args = append(args, startTime, endTime)
			countQuery = fmt.Sprintf("SELECT COUNT(*) FROM %s.th_telemetry_logs WHERE vehicle_id = $1 AND timestamp >= $2 AND timestamp <= $3", schema)
			query = fmt.Sprintf(`
				SELECT id, vehicle_id, imei, lat, lon, speed, heading, altitude, acc_status, battery_level, satellites, gsm_signal, timestamp
				FROM %s.th_telemetry_logs
				WHERE vehicle_id = $1 AND timestamp >= $2 AND timestamp <= $3
				ORDER BY timestamp DESC
				LIMIT $%d OFFSET $%d
			`, schema, len(args)+1, len(args)+2)
			args = append(args, limit, offset)
		}
	}

	if query == "" {
		countQuery = fmt.Sprintf("SELECT COUNT(*) FROM %s.th_telemetry_logs WHERE vehicle_id = $1", schema)
		query = fmt.Sprintf(`
			SELECT id, vehicle_id, imei, lat, lon, speed, heading, altitude, acc_status, battery_level, satellites, gsm_signal, timestamp
			FROM %s.th_telemetry_logs
			WHERE vehicle_id = $1
			ORDER BY timestamp DESC
			LIMIT $2 OFFSET $3
		`, schema)
		args = []interface{}{vehicleID, limit, offset}
	}

	var total int
	router := tenant.NewReadRouter(claims.CompanyCode)
	if countQuery != "" {
		if len(args) == 3 {
			_ = router.QueryRow(r.Context(), countQuery, vehicleID).Scan(&total)
		} else if len(args) == 5 {
			_ = router.QueryRow(r.Context(), countQuery, args[0], args[1], args[2]).Scan(&total)
		}
	}

	rows, err := router.Query(r.Context(), query, args...)
	if err != nil {
		logger.Log.Error("Failed to query vehicle history", "err", err)
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to retrieve telemetry history")
		return
	}
	defer rows.Close()

	history := make([]TelemetryHistoryItem, 0)
	for rows.Next() {
		var item TelemetryHistoryItem
		if err := rows.Scan(&item.ID, &item.VehicleID, &item.IMEI, &item.Lat, &item.Lon, &item.Speed, &item.Heading, &item.Altitude, &item.ACCStatus, &item.BatteryLevel, &item.Satellites, &item.GSMSignal, &item.Timestamp); err == nil {
			history = append(history, item)
		}
	}

	useRdp := r.URL.Query().Get("rdp") == "true"
	if useRdp && len(history) > 2 {
		epsilonStr := r.URL.Query().Get("epsilon")
		epsilon := 0.0001 // default roughly 10 meters
		if e, err := strconv.ParseFloat(epsilonStr, 64); err == nil && e > 0 {
			epsilon = e
		}

		// Convert to points
		var points []utils.Point
		for _, h := range history {
			points = append(points, utils.Point{X: h.Lat, Y: h.Lon})
		}
		reduced := utils.RamerDouglasPeucker(points, epsilon)

		// Map back to history items (matching by lat/lon since order is preserved)
		var reducedHistory []TelemetryHistoryItem
		pIdx := 0
		for _, h := range history {
			if pIdx < len(reduced) && h.Lat == reduced[pIdx].X && h.Lon == reduced[pIdx].Y {
				reducedHistory = append(reducedHistory, h)
				pIdx++
			}
		}
		history = reducedHistory
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
		"data":   history,
		"pagination": map[string]interface{}{
			"page":  page,
			"limit": limit,
			"total": total,
		},
	})
}
