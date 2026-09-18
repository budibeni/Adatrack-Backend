package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"backend/internal/auth"
	"backend/internal/dbclient"
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
	LicenseExpiry time.Time `json:"license_expiry,omitempty"`
	RFIDTag       string    `json:"rfid_tag,omitempty"`
	GroupID       *int      `json:"group_id,omitempty"`
}

type ShareLink struct {
	ID        int       `json:"id"`
	Token     string    `json:"token"`
	VehicleID int       `json:"vehicle_id"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedBy int       `json:"created_by"`
}

func (h *Handler) GetAccessibleMenus(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	query := fmt.Sprintf(`
		SELECT m.id, m.module_id, m.code, m.name, m.path, m.parent_id, m.sort_order, m.enabled
		FROM adatrack_gps_master.tm_menus m
		JOIN %s.tm_role_menu_access rma ON m.id = rma.menu_id
		WHERE rma.role = $1 AND rma.can_view = true AND rma.enabled = true AND m.enabled = true
		ORDER BY m.sort_order
	`, schema)
	rows, err := tenant.NewReadRouter(claims.CompanyCode).Query(r.Context(), query, claims.Role)
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
}

func (h *Handler) GetRoleMenuAccess(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
func (h *Handler) UpdateRoleMenuAccess(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) ListDrivers(w http.ResponseWriter, r *http.Request) {
	claims := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	query := fmt.Sprintf(`SELECT id, name, phone, email, license_number, license_type, rfid_tag FROM %s.tm_drivers WHERE deleted_at IS NULL`, schema)
	rows, err := tenant.NewReadRouter(claims.CompanyCode).Query(r.Context(), query)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	defer rows.Close()
	var items []Driver
	for rows.Next() {
		var d Driver
		rows.Scan(&d.ID, &d.Name, &d.Phone, &d.Email, &d.LicenseNumber, &d.LicenseType, &d.RFIDTag)
		items = append(items, d)
	}
	h.writeJSON(w, http.StatusOK, items)
}

func (h *Handler) CreateDriver(w http.ResponseWriter, r *http.Request) {
	claims := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	var d Driver
	if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
		h.writeError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	query := fmt.Sprintf(`INSERT INTO %s.tm_drivers (name, phone, email, license_number, license_type, rfid_tag) VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`, schema)
	err := dbclient.Pool.QueryRow(r.Context(), query, d.Name, d.Phone, d.Email, d.LicenseNumber, d.LicenseType, d.RFIDTag).Scan(&d.ID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	h.writeJSON(w, http.StatusOK, d)
}

func (h *Handler) GetDriver(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
func (h *Handler) UpdateDriver(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}
func (h *Handler) SoftDeleteDriver(w http.ResponseWriter, r *http.Request) {
	claims := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	id := chi.URLParam(r, "id")
	query := fmt.Sprintf(`UPDATE %s.tm_drivers SET deleted_at = NOW() WHERE id = $1`, schema)
	_, err := dbclient.Pool.Exec(r.Context(), query, id)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
	query := fmt.Sprintf(`SELECT id, name, description FROM %s.tm_groups WHERE deleted_at IS NULL`, schema)
	rows, err := tenant.NewReadRouter(claims.CompanyCode).Query(r.Context(), query)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	defer rows.Close()
	var items []Group
	for rows.Next() {
		var g Group
		rows.Scan(&g.ID, &g.Name, &g.Description)
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
