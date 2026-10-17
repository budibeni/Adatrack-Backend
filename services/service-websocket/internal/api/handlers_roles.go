package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"context"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"
	"backend/internal/auth"
	"backend/internal/dbclient"
)

// RolePayload represents a tenant role payload
type RolePayload struct {
	Code        string   `json:"code"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
}

func (h *Handler) GetTenantRoles(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	rows, err := dbclient.Pool.Query(ctx, `
		SELECT id, code, name, description, is_system, permissions 
		FROM adatrack_gps_master.tm_roles 
		WHERE company_code = $1 OR company_code IS NULL
		ORDER BY is_system DESC, name ASC
	`, claims.CompanyCode)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to fetch roles")
		return
	}
	defer rows.Close()

	roles := make([]map[string]interface{}, 0)
	for rows.Next() {
		var id int
		var code, name string
		var description *string
		var isSystem bool
		var permStr *string

		if err := rows.Scan(&id, &code, &name, &description, &isSystem, &permStr); err != nil {
			fmt.Println("Scan error:", err)
			continue
		}

		var permissions []string
		if permStr != nil && *permStr != "" {
			json.Unmarshal([]byte(*permStr), &permissions)
		}
		if permissions == nil {
			permissions = []string{}
		}

		desc := ""
		if description != nil {
			desc = *description
		}

		roles = append(roles, map[string]interface{}{
			"id":          id,
			"code":        code,
			"name":        name,
			"description": desc,
			"is_system":   isSystem,
			"permissions": permissions,
		})
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "data": roles})
}

func (h *Handler) CreateTenantRole(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}

	// Dynamic RBAC Check
	if !claims.HasPermission("settings:write") {
		h.writeError(w, http.StatusForbidden, "FORBIDDEN", "Insufficient permissions")
		return
	}

	var req RolePayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid payload")
		return
	}

	if req.Code == "" || req.Name == "" {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "code and name are required")
		return
	}
	
	// Prefix code to avoid collision
	actualCode := fmt.Sprintf("CUSTOM_%s", req.Code)
	
	permBytes, _ := json.Marshal(req.Permissions)

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	_, err := dbclient.Pool.Exec(ctx, `
		INSERT INTO adatrack_gps_master.tm_roles (company_code, code, name, description, is_system, permissions) 
		VALUES ($1, $2, $3, $4, false, $5)
	`, claims.CompanyCode, actualCode, req.Name, req.Description, string(permBytes))

	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to create role or role code already exists")
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success"})
}

func (h *Handler) UpdateTenantRole(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}

	if !claims.HasPermission("settings:write") {
		h.writeError(w, http.StatusForbidden, "FORBIDDEN", "Insufficient permissions")
		return
	}

	roleID, _ := strconv.Atoi(chi.URLParam(r, "id"))

	var req RolePayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid payload")
		return
	}

	permBytes, _ := json.Marshal(req.Permissions)

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	res, err := dbclient.Pool.Exec(ctx, `
		UPDATE adatrack_gps_master.tm_roles 
		SET name = $1, description = $2, permissions = $3, updated_at = CURRENT_TIMESTAMP
		WHERE id = $4 AND company_code = $5 AND is_system = false
	`, req.Name, req.Description, string(permBytes), roleID, claims.CompanyCode)

	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to update role")
		return
	}

	if res.RowsAffected() == 0 {
		h.writeError(w, http.StatusNotFound, "NOT_FOUND", "Role not found or cannot edit system role")
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success"})
}

func (h *Handler) DeleteTenantRole(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}

	if !claims.HasPermission("settings:write") {
		h.writeError(w, http.StatusForbidden, "FORBIDDEN", "Insufficient permissions")
		return
	}

	roleID, _ := strconv.Atoi(chi.URLParam(r, "id"))

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	res, err := dbclient.Pool.Exec(ctx, `
		DELETE FROM adatrack_gps_master.tm_roles 
		WHERE id = $1 AND company_code = $2 AND is_system = false
	`, roleID, claims.CompanyCode)

	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to delete role (it might be in use)")
		return
	}

	if res.RowsAffected() == 0 {
		h.writeError(w, http.StatusNotFound, "NOT_FOUND", "Role not found or cannot delete system role")
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success"})
}

type TenantUserPayload struct {
	Email    string `json:"email"`
	FullName string `json:"full_name"`
	Password string `json:"password,omitempty"`
	RoleCode string `json:"role_code"`
	IsActive *bool  `json:"is_active,omitempty"`
}

type TenantUserItem struct {
	ID        int    `json:"id"`
	Email     string `json:"email"`
	FullName  string `json:"full_name"`
	RoleCode  string `json:"role_code"`
	RoleName  string `json:"role_name"`
	IsActive  bool   `json:"is_active"`
	CreatedAt string `json:"created_at"`
}

func (h *Handler) GetTenantUsers(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}

	schema := fmt.Sprintf("adatrack_gps_%s", strings.ToLower(claims.CompanyCode))

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	query := fmt.Sprintf(`
		SELECT u.id, u.email, COALESCE(u.full_name, '') as full_name, 
		       COALESCE(a.role_code, '') as role_code, 
		       COALESCE((
		           SELECT name FROM adatrack_gps_master.tm_roles r 
		           WHERE r.code = a.role_code AND (r.company_code = $1 OR r.company_code IS NULL)
		           ORDER BY (CASE WHEN r.company_code = $1 THEN 0 ELSE 1 END) ASC
		           LIMIT 1
		       ), a.role_code, 'Pengguna') as role_name,
		       a.is_active, u.created_at
		FROM %s.tm_user_company_access a
		JOIN adatrack_gps_master.tm_users u ON u.id = a.user_id
		WHERE a.deleted_at IS NULL AND u.deleted_at IS NULL
		ORDER BY u.created_at DESC
	`, schema)

	rows, err := dbclient.Pool.Query(ctx, query, claims.CompanyCode)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to fetch tenant users: "+err.Error())
		return
	}
	defer rows.Close()

	users := make([]TenantUserItem, 0)
	for rows.Next() {
		var item TenantUserItem
		var t time.Time
		if err := rows.Scan(&item.ID, &item.Email, &item.FullName, &item.RoleCode, &item.RoleName, &item.IsActive, &t); err == nil {
			item.CreatedAt = t.Format(time.RFC3339)
			users = append(users, item)
		}
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "data": users})
}

func (h *Handler) CreateTenantUser(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}

	var req TenantUserPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid payload")
		return
	}

	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	req.FullName = strings.TrimSpace(req.FullName)
	req.RoleCode = strings.TrimSpace(req.RoleCode)

	if req.Email == "" || req.RoleCode == "" {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Email dan Role wajib diisi")
		return
	}
	if req.Password == "" {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Password wajib diisi untuk pengguna baru")
		return
	}

	schema := fmt.Sprintf("adatrack_gps_%s", strings.ToLower(claims.CompanyCode))

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	tx, err := dbclient.Pool.Begin(ctx)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Gagal memulai transaksi: "+err.Error())
		return
	}
	defer tx.Rollback(ctx)

	var targetUserID int
	err = tx.QueryRow(ctx, "SELECT id FROM adatrack_gps_master.tm_users WHERE email = $1", req.Email).Scan(&targetUserID)
	if err != nil {
		// User does not exist, create master user
		hash, errHash := bcrypt.GenerateFromPassword([]byte(req.Password), 12)
		if errHash != nil {
			h.writeError(w, http.StatusInternalServerError, "HASH_ERROR", "Gagal memproses password")
			return
		}
		err = tx.QueryRow(ctx, `
			INSERT INTO adatrack_gps_master.tm_users (email, full_name, password_hash, is_active)
			VALUES ($1, $2, $3, true)
			RETURNING id
		`, req.Email, req.FullName, string(hash)).Scan(&targetUserID)
		if err != nil {
			h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Gagal membuat master user: "+err.Error())
			return
		}
	} else {
		// Master user exists, update full name if provided
		if req.FullName != "" {
			_, _ = tx.Exec(ctx, "UPDATE adatrack_gps_master.tm_users SET full_name = $1 WHERE id = $2", req.FullName, targetUserID)
		}
	}

	// Check if already in company schema
	var existingActive bool
	var isDeleted bool
	checkQuery := fmt.Sprintf(`
		SELECT is_active, (deleted_at IS NOT NULL) 
		FROM %s.tm_user_company_access 
		WHERE user_id = $1
		ORDER BY id DESC LIMIT 1
	`, schema)
	err = tx.QueryRow(ctx, checkQuery, targetUserID).Scan(&existingActive, &isDeleted)
	if err == nil && !isDeleted {
		h.writeError(w, http.StatusBadRequest, "DUPLICATE_USER", "Pengguna sudah terdaftar di perusahaan ini")
		return
	}

	isActive := true
	if req.IsActive != nil {
		isActive = *req.IsActive
	}

	if isDeleted {
		// Restore user in this company
		restoreQuery := fmt.Sprintf(`
			UPDATE %s.tm_user_company_access 
			SET role_code = $1, is_active = $2, deleted_at = NULL, updated_at = CURRENT_TIMESTAMP
			WHERE user_id = $3
		`, schema)
		_, err = tx.Exec(ctx, restoreQuery, req.RoleCode, isActive, targetUserID)
	} else {
		insertQuery := fmt.Sprintf(`
			INSERT INTO %s.tm_user_company_access (user_id, role_code, is_active)
			VALUES ($1, $2, $3)
		`, schema)
		_, err = tx.Exec(ctx, insertQuery, targetUserID, req.RoleCode, isActive)
	}

	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Gagal menambahkan user ke tenant: "+err.Error())
		return
	}

	if err := tx.Commit(ctx); err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Gagal menyimpan perubahan: "+err.Error())
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "message": "Pengguna berhasil ditambahkan"})
}

func (h *Handler) UpdateTenantUser(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}

	userID, _ := strconv.Atoi(chi.URLParam(r, "id"))
	if userID <= 0 {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid user ID")
		return
	}

	var req TenantUserPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid payload")
		return
	}

	schema := fmt.Sprintf("adatrack_gps_%s", strings.ToLower(claims.CompanyCode))

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	tx, err := dbclient.Pool.Begin(ctx)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Gagal memulai transaksi: "+err.Error())
		return
	}
	defer tx.Rollback(ctx)

	// Update master user name and password if provided
	if req.FullName != "" {
		_, _ = tx.Exec(ctx, "UPDATE adatrack_gps_master.tm_users SET full_name = $1 WHERE id = $2", req.FullName, userID)
	}
	if req.Password != "" {
		hash, errHash := bcrypt.GenerateFromPassword([]byte(req.Password), 12)
		if errHash == nil {
			_, _ = tx.Exec(ctx, "UPDATE adatrack_gps_master.tm_users SET password_hash = $1 WHERE id = $2", string(hash), userID)
		}
	}

	// Update tenant company access
	updateQuery := fmt.Sprintf(`
		UPDATE %s.tm_user_company_access
		SET role_code = COALESCE(NULLIF($1, ''), role_code),
		    is_active = COALESCE($2, is_active),
		    updated_at = CURRENT_TIMESTAMP
		WHERE user_id = $3 AND deleted_at IS NULL
	`, schema)

	_, err = tx.Exec(ctx, updateQuery, req.RoleCode, req.IsActive, userID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Gagal mengupdate akses pengguna: "+err.Error())
		return
	}

	if err := tx.Commit(ctx); err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Gagal menyimpan perubahan: "+err.Error())
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "message": "Pengguna berhasil diperbarui"})
}

func (h *Handler) DeleteTenantUser(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}

	userID, _ := strconv.Atoi(chi.URLParam(r, "id"))
	if userID <= 0 {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid user ID")
		return
	}

	if int64(userID) == claims.UserID {
		h.writeError(w, http.StatusBadRequest, "CANNOT_DELETE_SELF", "Anda tidak dapat menghapus akun Anda sendiri")
		return
	}

	schema := fmt.Sprintf("adatrack_gps_%s", strings.ToLower(claims.CompanyCode))

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	query := fmt.Sprintf(`
		UPDATE %s.tm_user_company_access
		SET deleted_at = CURRENT_TIMESTAMP, is_active = false
		WHERE user_id = $1 AND deleted_at IS NULL
	`, schema)

	res, err := dbclient.Pool.Exec(ctx, query, userID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Gagal menghapus pengguna: "+err.Error())
		return
	}

	if res.RowsAffected() == 0 {
		h.writeError(w, http.StatusNotFound, "NOT_FOUND", "Pengguna tidak ditemukan di perusahaan ini")
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "message": "Pengguna berhasil dihapus"})
}
