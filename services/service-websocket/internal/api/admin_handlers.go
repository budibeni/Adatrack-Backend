package api

import (
	"context"
	"net/http"
	"time"

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

	rows, err := dbclient.Pool.Query(ctx, "SELECT code, name, COALESCE(legal_name, ''), business_type FROM adatrack_gps_master.tm_companies WHERE deleted_at IS NULL ORDER BY created_at DESC")
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

	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "data": companies})
}

type UserInfo struct {
	ID          int    `json:"id"`
	Email       string `json:"email"`
	GlobalRole  string `json:"global_role"`
	IsActive    bool   `json:"is_active"`
	CreatedAt   string `json:"created_at"`
}

func (h *Handler) ListUsers(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	rows, err := dbclient.Pool.Query(ctx, "SELECT id, email, global_role, is_active, created_at FROM adatrack_gps_master.tm_users WHERE deleted_at IS NULL ORDER BY created_at DESC")
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to fetch users")
		return
	}
	defer rows.Close()

	users := []UserInfo{}
	for rows.Next() {
		var u UserInfo
		var t time.Time
		if err := rows.Scan(&u.ID, &u.Email, &u.GlobalRole, &u.IsActive, &t); err == nil {
			u.CreatedAt = t.Format(time.RFC3339)
			users = append(users, u)
		}
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "data": users})
}
