package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"backend/internal/dbclient"
	"strconv"
	"strings"
	"time"

	"backend/internal/auth"
	"backend/internal/tenant"
	"github.com/go-chi/chi/v5"
)

type Menu struct {
	ID        int    `json:"id"`
	ModuleID  int    `json:"module_id"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	ParentID  *int   `json:"parent_id,omitempty"`
	SortOrder int    `json:"sort_order"`
	Enabled   bool   `json:"enabled"`
}

type Driver struct {
	ID            int       `json:"id"`
	Name          string    `json:"name"`
	Phone         string    `json:"phone,omitempty"`
	Email         string    `json:"email,omitempty"`
	LicenseNumber string    `json:"license_number,omitempty"`
	LicenseType   string    `json:"license_type,omitempty"`
	LicenseExpiry *string   `json:"license_expiry,omitempty"`
	RFIDTag       string    `json:"rfid_tag,omitempty"`
	GroupID       *int      `json:"group_id,omitempty"`
	KTPNumber     *string   `json:"ktp_number,omitempty"`
	PlaceOfBirth  *string   `json:"place_of_birth,omitempty"`
	DateOfBirth   *string   `json:"date_of_birth,omitempty"`
	Address       *string   `json:"address,omitempty"`
	Placement     *string   `json:"placement,omitempty"`
	JoinDate      *string   `json:"join_date,omitempty"`
}

type ShareLink struct {
	ID        int       `json:"id"`
	Token     string    `json:"token"`
	VehicleID int       `json:"vehicle_id"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedBy int       `json:"created_by"`
}

type MenuItemAccess struct {
	MenuID            int    `json:"menu_id"`
	ModuleID          int    `json:"module_id"`
	ModuleCode        string `json:"module_code"`
	ModuleName        string `json:"module_name"`
	MenuCode          string `json:"menu_code"`
	MenuName          string `json:"menu_name"`
	Path              string `json:"path"`
	SortOrder         int    `json:"sort_order"`
	CanView           bool   `json:"can_view"`
	RoleCanView       bool   `json:"role_can_view,omitempty"`
	HasCustomOverride bool   `json:"has_custom_override,omitempty"`
}

type MenuAccessUpdateRequest struct {
	Items []MenuAccessItemUpdate `json:"items"`
}

type MenuAccessItemUpdate struct {
	MenuID  int  `json:"menu_id"`
	CanView bool `json:"can_view"`
}

func (h *Handler) GetAccessibleMenus(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}

	// Super Admin always has access to all active menus
	if strings.ToLower(claims.Role) == "super_admin" || strings.ToLower(claims.Role) == "superadmin" {
		query := `
			SELECT m.id, m.module_id, m.code, m.name, m.path, m.parent_id, m.sort_order, m.enabled
			FROM adatrack_gps_master.tm_menus m
			JOIN adatrack_gps_master.tm_modules mod ON m.module_id = mod.id
			WHERE m.enabled = true AND mod.enabled = true
			ORDER BY mod.sort_order ASC, m.sort_order ASC
		`
		rows, err := dbclient.Pool.Query(r.Context(), query)
		if err != nil {
			h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to fetch menus")
			return
		}
		defer rows.Close()

		var menus []Menu
		for rows.Next() {
			var m Menu
			rows.Scan(&m.ID, &m.ModuleID, &m.Code, &m.Name, &m.Path, &m.ParentID, &m.SortOrder, &m.Enabled)
			menus = append(menus, m)
		}
		h.writeJSON(w, http.StatusOK, menus)
		return
	}

	schema := fmt.Sprintf("adatrack_gps_%s", strings.ToLower(claims.CompanyCode))

	// Effective access resolution:
	// 1. If tm_user_menu_access has an override, use user's can_view
	// 2. Else if tm_role_menu_access has a configuration, use role's can_view
	// 3. Else default to true
	query := fmt.Sprintf(`
		SELECT m.id, m.module_id, m.code, m.name, m.path, m.parent_id, m.sort_order, m.enabled
		FROM adatrack_gps_master.tm_menus m
		JOIN adatrack_gps_master.tm_modules mod ON m.module_id = mod.id
		LEFT JOIN %s.tm_role_menu_access rma ON m.id = rma.menu_id AND rma.role_code = $1 AND rma.deleted_at IS NULL
		LEFT JOIN %s.tm_user_menu_access uma ON m.id = uma.menu_id AND uma.user_id = $2 AND uma.deleted_at IS NULL
		WHERE m.enabled = true AND mod.enabled = true
		  AND COALESCE(uma.can_view, rma.can_view, true) = true
		ORDER BY mod.sort_order ASC, m.sort_order ASC
	`, schema, schema)

	rows, err := dbclient.Pool.Query(r.Context(), query, claims.Role, claims.UserID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to fetch menus: "+err.Error())
		return
	}
	defer rows.Close()

	var menus []Menu
	for rows.Next() {
		var m Menu
		rows.Scan(&m.ID, &m.ModuleID, &m.Code, &m.Name, &m.Path, &m.ParentID, &m.SortOrder, &m.Enabled)
		menus = append(menus, m)
	}
	h.writeJSON(w, http.StatusOK, menus)
}

func (h *Handler) GetSystemMenus(w http.ResponseWriter, r *http.Request) {
	query := `
		SELECT m.id, m.module_id, mod.code, mod.name, m.code, m.name, m.path, m.sort_order, m.enabled
		FROM adatrack_gps_master.tm_menus m
		JOIN adatrack_gps_master.tm_modules mod ON m.module_id = mod.id
		WHERE m.enabled = true AND mod.enabled = true
		ORDER BY mod.sort_order ASC, m.sort_order ASC
	`
	rows, err := dbclient.Pool.Query(r.Context(), query)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to fetch system menus: "+err.Error())
		return
	}
	defer rows.Close()

	items := make([]MenuItemAccess, 0)
	for rows.Next() {
		var item MenuItemAccess
		var enabled bool
		if err := rows.Scan(&item.MenuID, &item.ModuleID, &item.ModuleCode, &item.ModuleName, &item.MenuCode, &item.MenuName, &item.Path, &item.SortOrder, &enabled); err == nil {
			item.CanView = enabled
			items = append(items, item)
		}
	}
	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "data": items})
}

func (h *Handler) GetRoleMenuAccess(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}
	roleCode := chi.URLParam(r, "role")
	if roleCode == "" {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Role code is required")
		return
	}

	schema := fmt.Sprintf("adatrack_gps_%s", strings.ToLower(claims.CompanyCode))

	query := fmt.Sprintf(`
		SELECT m.id, m.module_id, mod.code, mod.name, m.code, m.name, m.path, m.sort_order,
		       COALESCE(rma.can_view, true) as can_view
		FROM adatrack_gps_master.tm_menus m
		JOIN adatrack_gps_master.tm_modules mod ON m.module_id = mod.id
		LEFT JOIN %s.tm_role_menu_access rma ON m.id = rma.menu_id AND rma.role_code = $1 AND rma.deleted_at IS NULL
		WHERE m.enabled = true AND mod.enabled = true
		ORDER BY mod.sort_order ASC, m.sort_order ASC
	`, schema)

	rows, err := dbclient.Pool.Query(r.Context(), query, roleCode)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to fetch role menu access: "+err.Error())
		return
	}
	defer rows.Close()

	items := make([]MenuItemAccess, 0)
	for rows.Next() {
		var item MenuItemAccess
		if err := rows.Scan(&item.MenuID, &item.ModuleID, &item.ModuleCode, &item.ModuleName, &item.MenuCode, &item.MenuName, &item.Path, &item.SortOrder, &item.CanView); err == nil {
			items = append(items, item)
		}
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "data": items})
}

func (h *Handler) UpdateRoleMenuAccess(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}
	roleCode := chi.URLParam(r, "role")
	if roleCode == "" {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Role code is required")
		return
	}

	var req MenuAccessUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid payload")
		return
	}

	schema := fmt.Sprintf("adatrack_gps_%s", strings.ToLower(claims.CompanyCode))

	tx, err := dbclient.Pool.Begin(r.Context())
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to begin transaction: "+err.Error())
		return
	}
	defer tx.Rollback(r.Context())

	for _, item := range req.Items {
		upsertQuery := fmt.Sprintf(`
			INSERT INTO %s.tm_role_menu_access (role_code, menu_id, can_view, enabled)
			VALUES ($1, $2, $3, true)
			ON CONFLICT (role_code, menu_id) 
			DO UPDATE SET can_view = EXCLUDED.can_view, enabled = true, deleted_at = NULL
		`, schema)
		_, err := tx.Exec(r.Context(), upsertQuery, roleCode, item.MenuID, item.CanView)
		if err != nil {
			h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to update role menu access: "+err.Error())
			return
		}
	}

	if err := tx.Commit(r.Context()); err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to commit: "+err.Error())
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "message": "Hak akses menu role berhasil disimpan"})
}

func (h *Handler) GetUserMenuAccess(w http.ResponseWriter, r *http.Request) {
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

	schema := fmt.Sprintf("adatrack_gps_%s", strings.ToLower(claims.CompanyCode))

	var roleCode string
	err := dbclient.Pool.QueryRow(r.Context(),
		fmt.Sprintf("SELECT COALESCE(role_code, '') FROM %s.tm_user_company_access WHERE user_id = $1 AND deleted_at IS NULL", schema),
		userID).Scan(&roleCode)
	if err != nil {
		h.writeError(w, http.StatusNotFound, "NOT_FOUND", "Pengguna tidak ditemukan di perusahaan ini")
		return
	}

	query := fmt.Sprintf(`
		SELECT m.id, m.module_id, mod.code, mod.name, m.code, m.name, m.path, m.sort_order,
		       COALESCE(rma.can_view, true) as role_can_view,
		       (uma.id IS NOT NULL AND uma.deleted_at IS NULL) as has_custom_override,
		       COALESCE(uma.can_view, rma.can_view, true) as effective_can_view
		FROM adatrack_gps_master.tm_menus m
		JOIN adatrack_gps_master.tm_modules mod ON m.module_id = mod.id
		LEFT JOIN %s.tm_role_menu_access rma ON m.id = rma.menu_id AND rma.role_code = $1 AND rma.deleted_at IS NULL
		LEFT JOIN %s.tm_user_menu_access uma ON m.id = uma.menu_id AND uma.user_id = $2 AND uma.deleted_at IS NULL
		WHERE m.enabled = true AND mod.enabled = true
		ORDER BY mod.sort_order ASC, m.sort_order ASC
	`, schema, schema)

	rows, err := dbclient.Pool.Query(r.Context(), query, roleCode, userID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to fetch user menu access: "+err.Error())
		return
	}
	defer rows.Close()

	items := make([]MenuItemAccess, 0)
	for rows.Next() {
		var item MenuItemAccess
		if err := rows.Scan(&item.MenuID, &item.ModuleID, &item.ModuleCode, &item.ModuleName, &item.MenuCode, &item.MenuName, &item.Path, &item.SortOrder, &item.RoleCanView, &item.HasCustomOverride, &item.CanView); err == nil {
			items = append(items, item)
		}
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"user_id":   userID,
			"role_code": roleCode,
			"menus":     items,
		},
	})
}

func (h *Handler) UpdateUserMenuAccess(w http.ResponseWriter, r *http.Request) {
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

	var req MenuAccessUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid payload")
		return
	}

	schema := fmt.Sprintf("adatrack_gps_%s", strings.ToLower(claims.CompanyCode))

	tx, err := dbclient.Pool.Begin(r.Context())
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to begin transaction: "+err.Error())
		return
	}
	defer tx.Rollback(r.Context())

	for _, item := range req.Items {
		upsertQuery := fmt.Sprintf(`
			INSERT INTO %s.tm_user_menu_access (user_id, menu_id, can_view, enabled)
			VALUES ($1, $2, $3, true)
			ON CONFLICT (user_id, menu_id)
			DO UPDATE SET can_view = EXCLUDED.can_view, enabled = true, deleted_at = NULL
		`, schema)
		_, err := tx.Exec(r.Context(), upsertQuery, userID, item.MenuID, item.CanView)
		if err != nil {
			h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to update user menu access: "+err.Error())
			return
		}
	}

	if err := tx.Commit(r.Context()); err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to commit: "+err.Error())
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "message": "Override akses menu pengguna berhasil disimpan"})
}

func (h *Handler) ResetUserMenuAccess(w http.ResponseWriter, r *http.Request) {
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

	schema := fmt.Sprintf("adatrack_gps_%s", strings.ToLower(claims.CompanyCode))

	query := fmt.Sprintf("DELETE FROM %s.tm_user_menu_access WHERE user_id = $1", schema)
	_, err := dbclient.Pool.Exec(r.Context(), query, userID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to reset user menu access: "+err.Error())
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "message": "Akses menu pengguna berhasil dikembalikan ke default role"})
}

func (h *Handler) ListShareLinks(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, http.StatusOK, []string{})
}
func (h *Handler) CreateShareLink(w http.ResponseWriter, r *http.Request) {
	claims := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	var sl ShareLink
	if err := json.NewDecoder(r.Body).Decode(&sl); err != nil {
		h.writeError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	query := fmt.Sprintf(`INSERT INTO %s.tm_shared_locations (token, vehicle_id, expires_at, created_by) VALUES ($1, $2, $3, $4) RETURNING id`, schema)
	err := dbclient.Pool.QueryRow(r.Context(), query, sl.Token, sl.VehicleID, sl.ExpiresAt, claims.UserID).Scan(&sl.ID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	h.writeJSON(w, http.StatusOK, sl)
}
func (h *Handler) RevokeShareLink(w http.ResponseWriter, r *http.Request) {
	claims := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	id := chi.URLParam(r, "id")
	query := fmt.Sprintf(`UPDATE %s.tm_shared_locations SET deleted_at = NOW() WHERE id = $1`, schema)
	_, err := dbclient.Pool.Exec(r.Context(), query, id)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type Group struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Type        string `json:"type,omitempty"`
}

type Asset struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	SerialNumber string `json:"serial_number,omitempty"`
}

type Organization struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type Integration struct {
	ID      int    `json:"id"`
	Type    string `json:"type"`
	Webhook string `json:"webhook,omitempty"`
}

func (h *Handler) ListGroups(w http.ResponseWriter, r *http.Request) {
	claims := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	query := fmt.Sprintf(`SELECT id, name, description, group_type FROM %s.tm_groups WHERE deleted_at IS NULL`, schema)
	rows, err := tenant.NewReadRouter(claims.CompanyCode).Query(r.Context(), query)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	defer rows.Close()
	var items []Group
	for rows.Next() {
		var g Group
		var gtype sql.NullString
		rows.Scan(&g.ID, &g.Name, &g.Description, &gtype)
		if gtype.Valid {
			g.Type = gtype.String
		} else {
			g.Type = "vehicle"
		}
		items = append(items, g)
	}
	h.writeJSON(w, http.StatusOK, items)
}

func (h *Handler) ListAssets(w http.ResponseWriter, r *http.Request) {
	claims := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	query := fmt.Sprintf(`SELECT id, name, type, serial_number FROM %s.tm_assets WHERE deleted_at IS NULL`, schema)
	rows, err := tenant.NewReadRouter(claims.CompanyCode).Query(r.Context(), query)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	defer rows.Close()
	var items []Asset
	for rows.Next() {
		var a Asset
		rows.Scan(&a.ID, &a.Name, &a.Type, &a.SerialNumber)
		items = append(items, a)
	}
	h.writeJSON(w, http.StatusOK, items)
}

func (h *Handler) ListOrganizations(w http.ResponseWriter, r *http.Request) {
	claims := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	query := fmt.Sprintf(`SELECT id, name FROM %s.tm_organizations WHERE deleted_at IS NULL`, schema)
	rows, err := tenant.NewReadRouter(claims.CompanyCode).Query(r.Context(), query)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	defer rows.Close()
	var items []Organization
	for rows.Next() {
		var o Organization
		rows.Scan(&o.ID, &o.Name)
		items = append(items, o)
	}
	h.writeJSON(w, http.StatusOK, items)
}

func (h *Handler) ListIntegrations(w http.ResponseWriter, r *http.Request) {
	claims := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	query := fmt.Sprintf(`SELECT id, integration_type, webhook_url FROM %s.tm_integrations WHERE deleted_at IS NULL`, schema)
	rows, err := tenant.NewReadRouter(claims.CompanyCode).Query(r.Context(), query)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	defer rows.Close()
	var items []Integration
	for rows.Next() {
		var i Integration
		rows.Scan(&i.ID, &i.Type, &i.Webhook)
		items = append(items, i)
	}
	h.writeJSON(w, http.StatusOK, items)
}

func (h *Handler) CreateGroup(w http.ResponseWriter, r *http.Request) {
	claims := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	
	var g Group
	if err := json.NewDecoder(r.Body).Decode(&g); err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_BODY", "Invalid JSON body")
		return
	}
	
	if g.Type == "" {
		g.Type = "vehicle"
	}
	
	query := fmt.Sprintf(`INSERT INTO %s.tm_groups (name, description, group_type, created_at, updated_at) VALUES ($1, $2, $3, NOW(), NOW()) RETURNING id`, schema)
	err := dbclient.Pool.QueryRow(r.Context(), query, g.Name, g.Description, g.Type).Scan(&g.ID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	
	h.writeJSON(w, http.StatusCreated, g)
}

func (h *Handler) UpdateGroup(w http.ResponseWriter, r *http.Request) {
	claims := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	idStr := chi.URLParam(r, "id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_ID", "Invalid group ID")
		return
	}
	
	var g Group
	if err := json.NewDecoder(r.Body).Decode(&g); err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_BODY", "Invalid JSON body")
		return
	}
	
	query := fmt.Sprintf(`UPDATE %s.tm_groups SET name = $1, description = $2, group_type = $3, updated_at = NOW() WHERE id = $4 AND deleted_at IS NULL`, schema)
	res, err := dbclient.Pool.Exec(r.Context(), query, g.Name, g.Description, g.Type, id)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	
	rowsAffected := res.RowsAffected()
	if rowsAffected == 0 {
		h.writeError(w, http.StatusNotFound, "NOT_FOUND", "Group not found")
		return
	}
	
	g.ID = id
	h.writeJSON(w, http.StatusOK, g)
}

func (h *Handler) DeleteGroup(w http.ResponseWriter, r *http.Request) {
	claims := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	idStr := chi.URLParam(r, "id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_ID", "Invalid group ID")
		return
	}
	
	query := fmt.Sprintf(`UPDATE %s.tm_groups SET deleted_at = NOW() WHERE id = $1 AND deleted_at IS NULL`, schema)
	res, err := dbclient.Pool.Exec(r.Context(), query, id)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	
	rowsAffected := res.RowsAffected()
	if rowsAffected == 0 {
		h.writeError(w, http.StatusNotFound, "NOT_FOUND", "Group not found")
		return
	}
	
	w.WriteHeader(http.StatusNoContent)
}
