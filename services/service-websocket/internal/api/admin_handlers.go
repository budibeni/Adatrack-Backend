package api

import (
	"strings"
	"encoding/json"
	"fmt"
	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"
	"context"
	"net/http"
	"time"
	"strconv"
	
	"backend/internal/auth"
	"backend/internal/dbclient"
)

type DashboardStats struct {
	TotalTenants     int    `json:"total_tenants"`
	ActiveUsers      int    `json:"active_users"`
	SystemHealth     string `json:"system_health"`
	AvgResponseTime  string `json:"avg_response_time"`
}

func (h *Handler) GetDashboardStats(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var stats DashboardStats
	stats.SystemHealth = "99.9%"
	stats.AvgResponseTime = "45ms"

	err := dbclient.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM adatrack_gps_master.tm_companies WHERE deleted_at IS NULL").Scan(&stats.TotalTenants)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to count companies")
		return
	}

	err = dbclient.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM adatrack_gps_master.tm_users WHERE is_active = true AND deleted_at IS NULL").Scan(&stats.ActiveUsers)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to count users")
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "data": stats})
}

type CompanyInfo struct {
	ID          string `json:"id"`
	Code        string `json:"code"`
	Name        string `json:"name"`
	LegalName   string `json:"legal_name"`
	BusinessType string `json:"business_type"`
	Status      string `json:"status"`
}

func (h *Handler) ListCompanies(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

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
	_ = dbclient.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM adatrack_gps_master.tm_companies WHERE deleted_at IS NULL").Scan(&total)

	rows, err := dbclient.Pool.Query(ctx, "SELECT code, name, COALESCE(legal_name, ''), business_type FROM adatrack_gps_master.tm_companies WHERE deleted_at IS NULL ORDER BY created_at DESC LIMIT $1 OFFSET $2", limit, offset)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to fetch companies")
		return
	}
	defer rows.Close()

	companies := []CompanyInfo{}
	for rows.Next() {
		var c CompanyInfo
		if err := rows.Scan(&c.Code, &c.Name, &c.LegalName, &c.BusinessType); err == nil {
			c.ID = c.Code
			c.Status = "Active"
			companies = append(companies, c)
		}
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
		"data": companies,
		"pagination": map[string]interface{}{
			"page":  page,
			"limit": limit,
			"total": total,
		},
	})
}

type UserInfo struct {
	ID         int      `json:"id"`
	Email      string   `json:"email"`
	IsActive   bool     `json:"is_active"`
	CreatedAt  string   `json:"created_at"`
	GlobalRole string   `json:"global_role"`
	Tenants    []string `json:"tenants"`
}

func (h *Handler) ListUsers(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

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
	_ = dbclient.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM adatrack_gps_master.tm_users WHERE deleted_at IS NULL").Scan(&total)

	query := `
		SELECT u.id, u.email, u.is_active, u.created_at,
		       COALESCE(a.role_code, 'Tenant User') as global_role
		FROM adatrack_gps_master.tm_users u
		LEFT JOIN adatrack_gps_default.tm_user_company_access a ON u.id = a.user_id AND a.deleted_at IS NULL
		WHERE u.deleted_at IS NULL
		ORDER BY u.created_at DESC
		LIMIT $1 OFFSET $2
	`

	rows, err := dbclient.Pool.Query(ctx, query, limit, offset)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to fetch users")
		return
	}
	defer rows.Close()

	users := []UserInfo{}
	var userIDs []int
	userMap := make(map[int]*UserInfo)

	for rows.Next() {
		var u UserInfo
		var t time.Time
		if err := rows.Scan(&u.ID, &u.Email, &u.IsActive, &t, &u.GlobalRole); err == nil {
			u.CreatedAt = t.Format(time.RFC3339)
			u.Tenants = []string{}
			users = append(users, u)
		}
	}
	
	// Map users for O(1) access
	for i := range users {
		userIDs = append(userIDs, users[i].ID)
		userMap[users[i].ID] = &users[i]
	}

	if len(userIDs) > 0 {
		// Fetch all company codes
		compRows, err := dbclient.Pool.Query(ctx, "SELECT code FROM adatrack_gps_master.tm_companies WHERE deleted_at IS NULL AND business_type = 'b2b'")
		if err == nil {
			var codes []string
			for compRows.Next() {
				var code string
				if err := compRows.Scan(&code); err == nil {
					codes = append(codes, code)
				}
			}
			compRows.Close()

			if len(codes) > 0 {
				var queryParts []string
				for _, code := range codes {
					schema := fmt.Sprintf("adatrack_gps_%s", strings.ToLower(code))
					queryParts = append(queryParts, fmt.Sprintf("SELECT user_id, '%s' as company_code FROM %s.tm_user_company_access WHERE user_id = ANY($1) AND deleted_at IS NULL AND is_active = true", code, schema))
				}
				
				query := strings.Join(queryParts, " UNION ALL ")
				accessRows, err := dbclient.Pool.Query(ctx, query, userIDs)
				if err == nil {
					for accessRows.Next() {
						var uid int
						var ccode string
						if err := accessRows.Scan(&uid, &ccode); err == nil {
							if user, ok := userMap[uid]; ok {
								user.Tenants = append(user.Tenants, ccode)
							}
						}
					}
					accessRows.Close()
				}
			}
		}
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
		"data": users,
		"pagination": map[string]interface{}{
			"page":  page,
			"limit": limit,
			"total": total,
		},
	})
}

type AdminCreateUserRequest struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	RoleCode    string `json:"role_code"`
	CompanyCode string `json:"company_code"`
	VerificationPassword string `json:"verification_password"`
}

func (h *Handler) AdminCreateUser(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var req AdminCreateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid payload")
		return
	}

	if req.Email == "" || req.Password == "" || req.RoleCode == "" {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Email, password, and role are required")
		return
	}

	if req.RoleCode != "SUPER_ADMIN" && req.CompanyCode == "" {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Company code is required for non-SuperAdmin users")
		return
	}

	if req.VerificationPassword == "" {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Verification password is required")
		return
	}

	claims := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	var currentHash string
	if err := dbclient.Pool.QueryRow(ctx, "SELECT password_hash FROM adatrack_gps_master.tm_users WHERE id = $1", claims.UserID).Scan(&currentHash); err != nil || bcrypt.CompareHashAndPassword([]byte(currentHash), []byte(req.VerificationPassword)) != nil {
		h.writeError(w, http.StatusForbidden, "INVALID_PASSWORD", "Invalid verification password")
		return
	}

	tx, err := dbclient.Pool.Begin(ctx)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to start transaction")
		return
	}
	defer tx.Rollback(ctx)

	var newUserID int
	err = tx.QueryRow(ctx, "SELECT id FROM adatrack_gps_master.tm_users WHERE email = $1", req.Email).Scan(&newUserID)
	if err != nil {
		hash, _ := bcrypt.GenerateFromPassword([]byte(req.Password), 12)
		err = tx.QueryRow(ctx, "INSERT INTO adatrack_gps_master.tm_users (email, password_hash) VALUES ($1, $2) RETURNING id", req.Email, string(hash)).Scan(&newUserID)
		if err != nil {
			h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to create user in master.")
			return
		}
	}

	if req.RoleCode != "SUPER_ADMIN" && req.CompanyCode != "" {
		schema := fmt.Sprintf("adatrack_gps_%s", strings.ToLower(req.CompanyCode))
		var existingAccess int
		errCheck := tx.QueryRow(ctx, fmt.Sprintf("SELECT 1 FROM %s.tm_user_company_access WHERE user_id = $1", schema), newUserID).Scan(&existingAccess)
		if errCheck == nil {
			h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "User already registered in this company.")
			return
		}
		_, err = tx.Exec(ctx, fmt.Sprintf("INSERT INTO %s.tm_user_company_access (user_id, role_code, is_active) VALUES ($1, $2, true)", schema), newUserID, req.RoleCode)
		if err != nil {
			h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to link user to company schema.")
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to commit user creation")
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "message": "User created successfully"})
}

type AdminResetPasswordRequest struct {
	NewPassword string `json:"new_password"`
	VerificationPassword string `json:"verification_password"`
}

func (h *Handler) AdminResetPassword(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	userID := chi.URLParam(r, "id")
	if userID == "" {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "User ID is required")
		return
	}

	var req AdminResetPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.NewPassword == "" {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "new_password is required")
		return
	}

	if req.VerificationPassword == "" {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Verification password is required")
		return
	}

	claims := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	var currentHash string
	if err := dbclient.Pool.QueryRow(ctx, "SELECT password_hash FROM adatrack_gps_master.tm_users WHERE id = $1", claims.UserID).Scan(&currentHash); err != nil || bcrypt.CompareHashAndPassword([]byte(currentHash), []byte(req.VerificationPassword)) != nil {
		h.writeError(w, http.StatusForbidden, "INVALID_PASSWORD", "Invalid verification password")
		return
	}

	hash, _ := bcrypt.GenerateFromPassword([]byte(req.NewPassword), 12)
	res, err := dbclient.Pool.Exec(ctx, "UPDATE adatrack_gps_master.tm_users SET password_hash = $1 WHERE id = $2", string(hash), userID)
	if err != nil || res.RowsAffected() == 0 {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to reset password or user not found")
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "message": "Password reset successfully"})
}

type ModuleAccess struct {
	ModuleID   int    `json:"module_id"`
	ModuleCode string `json:"module_code"`
	ModuleName string `json:"module_name"`
	AppType    string `json:"app_type"`
	Enabled    bool   `json:"enabled"`
}

func (h *Handler) AdminGetTenantModules(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	code := chi.URLParam(r, "code")
	if code == "" {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Company code is required")
		return
	}

	schema := fmt.Sprintf("adatrack_gps_%s", strings.ToLower(code))
	
	// Check if schema exists safely
	var schemaExists bool
	err := dbclient.Pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM information_schema.schemata WHERE schema_name = $1)", schema).Scan(&schemaExists)
	if err != nil || !schemaExists {
		h.writeError(w, http.StatusNotFound, "NOT_FOUND", "Tenant schema not found")
		return
	}

	query := fmt.Sprintf(`
		SELECT 
			m.id, m.code, m.name, m.app,
			COALESCE(a.enabled, false) as enabled
		FROM adatrack_gps_master.tm_modules m
		LEFT JOIN %s.tm_module_access a ON m.id = a.module_id AND a.deleted_at IS NULL
		ORDER BY m.app, m.sort_order
	`, schema)

	rows, err := dbclient.Pool.Query(ctx, query)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to fetch modules")
		return
	}
	defer rows.Close()

	var modules []ModuleAccess
	for rows.Next() {
		var mod ModuleAccess
		if err := rows.Scan(&mod.ModuleID, &mod.ModuleCode, &mod.ModuleName, &mod.AppType, &mod.Enabled); err != nil {
			continue
		}
		modules = append(modules, mod)
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "data": modules})
}

type UpdateTenantModulesRequest struct {
	Password string `json:"password"`
	Modules []struct {
		ModuleID int  `json:"module_id"`
		Enabled  bool `json:"enabled"`
	} `json:"modules"`
}

func (h *Handler) AdminUpdateTenantModules(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	code := chi.URLParam(r, "code")
	var req UpdateTenantModulesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid payload")
		return
	}

	if req.Password == "" {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "SuperAdmin password is required")
		return
	}

	claims := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	var hash string
	err := dbclient.Pool.QueryRow(ctx, "SELECT password_hash FROM adatrack_gps_master.tm_users WHERE id = $1", claims.UserID).Scan(&hash)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		h.writeError(w, http.StatusForbidden, "INVALID_PASSWORD", "Invalid super admin password")
		return
	}

	schema := fmt.Sprintf("adatrack_gps_%s", strings.ToLower(code))
	
	tx, err := dbclient.Pool.Begin(ctx)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to start transaction")
		return
	}
	defer tx.Rollback(ctx)

	for _, mod := range req.Modules {
		if mod.Enabled {
			// Wait, tm_module_access doesn't have a unique constraint on module_id in the table definition. 
			// We should do: delete existing then insert.
			_, _ = tx.Exec(ctx, fmt.Sprintf("DELETE FROM %s.tm_module_access WHERE module_id = $1", schema), mod.ModuleID)
			_, _ = tx.Exec(ctx, fmt.Sprintf("INSERT INTO %s.tm_module_access (module_id, enabled) VALUES ($1, true)", schema), mod.ModuleID)
		} else {
			_, _ = tx.Exec(ctx, fmt.Sprintf("DELETE FROM %s.tm_module_access WHERE module_id = $1", schema), mod.ModuleID)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to commit changes")
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "message": "Modules updated successfully"})
}

// ListRoles returns a list of all global roles
func (h *Handler) ListRoles(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	rows, err := dbclient.Pool.Query(ctx, "SELECT code, name, description FROM adatrack_gps_master.tm_roles ORDER BY id ASC")
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to fetch roles")
		return
	}
	defer rows.Close()

	type Role struct {
		Code        string `json:"code"`
		Name        string `json:"name"`
		Description string `json:"description"`
	}

	var roles []Role
	for rows.Next() {
		var r Role
		var desc *string
		if err := rows.Scan(&r.Code, &r.Name, &desc); err == nil {
			if desc != nil {
				r.Description = *desc
			}
			roles = append(roles, r)
		}
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"data": roles,
	})
}
