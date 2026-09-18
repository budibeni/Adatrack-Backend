package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"backend/internal/auth"
	"backend/internal/tenant"
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

type RoleMenuAccess struct {
	MenuID    int    `json:"menu_id"`
	CanView   bool   `json:"can_view"`
	CanCreate bool   `json:"can_create"`
	CanEdit   bool   `json:"can_edit"`
	CanDelete bool   `json:"can_delete"`
	Enabled   bool   `json:"enabled"`
}

func (h *Handler) GetAccessibleMenus(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}
	
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	// Fallback/example using claims.Role
	role := claims.Role

	query := fmt.Sprintf(`
		SELECT m.id, m.module_id, m.code, m.name, m.path, m.parent_id, m.sort_order, m.enabled
		FROM adatrack_gps_master.tm_menus m
		JOIN %s.tm_role_menu_access rma ON m.id = rma.menu_id
		WHERE rma.role = $1 AND rma.can_view = true AND rma.enabled = true AND m.enabled = true
		ORDER BY m.sort_order
	`, schema)

	rows, err := tenant.NewReadRouter(claims.CompanyCode).Query(r.Context(), query, role)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to fetch menus")
		return
	}
	defer rows.Close()

	var menus []Menu
	for rows.Next() {
		var m Menu
		if err := rows.Scan(&m.ID, &m.ModuleID, &m.Code, &m.Name, &m.Path, &m.ParentID, &m.SortOrder, &m.Enabled); err != nil {
			continue
		}
		menus = append(menus, m)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(menus)
}

func (h *Handler) GetRoleMenuAccess(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotImplemented)
}

func (h *Handler) UpdateRoleMenuAccess(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotImplemented)
}
