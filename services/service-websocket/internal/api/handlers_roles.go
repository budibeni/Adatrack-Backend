package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"context"
	"time"

	"github.com/go-chi/chi/v5"
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
		var permBytes []byte

		if err := rows.Scan(&id, &code, &name, &description, &isSystem, &permBytes); err != nil {
			continue
		}

		var permissions []string
		if len(permBytes) > 0 {
			json.Unmarshal(permBytes, &permissions)
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
