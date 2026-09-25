package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"backend/internal/auth"
	"backend/internal/tenant"
)

type MaintenanceTask struct {
	ID               string    `json:"id"`
	VehicleID        int       `json:"vehicle_id"`
	TaskName         string    `json:"task_name"`
	Description      string    `json:"description,omitempty"`
	IntervalKM       float64   `json:"interval_km,omitempty"`
	IntervalHours    float64   `json:"interval_hours,omitempty"`
	LastServiceKM    float64   `json:"last_service_km,omitempty"`
	LastServiceHours float64   `json:"last_service_hours,omitempty"`
	LastServiceDate  time.Time `json:"last_service_date,omitempty"`
	IsActive         bool      `json:"is_active"`
}

func (h *Handler) ListMaintenanceTasks(w http.ResponseWriter, r *http.Request) {
	vehicleID := chi.URLParam(r, "id")
	
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)

	query := fmt.Sprintf(`
		SELECT id, vehicle_id, task_name, description, interval_km, interval_hours, last_service_km, last_service_hours, last_service_date, is_active 
		FROM %s.tm_maintenance_tasks 
		WHERE vehicle_id = $1 AND deleted_at IS NULL
	`, schema)
	rows, err := tenant.NewReadRouter(claims.CompanyCode).Query(r.Context(), query, vehicleID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "failed to list tasks")
		return
	}
	defer rows.Close()

	var tasks []MaintenanceTask
	for rows.Next() {
		var t MaintenanceTask
		var lastDate *time.Time
		var desc *string
		var intKM, intHours, lastKM, lastHours *float64
		if err := rows.Scan(&t.ID, &t.VehicleID, &t.TaskName, &desc, &intKM, &intHours, &lastKM, &lastHours, &lastDate, &t.IsActive); err != nil {
			continue
		}
		if desc != nil { t.Description = *desc }
		if intKM != nil { t.IntervalKM = *intKM }
		if intHours != nil { t.IntervalHours = *intHours }
		if lastKM != nil { t.LastServiceKM = *lastKM }
		if lastHours != nil { t.LastServiceHours = *lastHours }
		if lastDate != nil { t.LastServiceDate = *lastDate }
		tasks = append(tasks, t)
	}

	if tasks == nil {
		tasks = []MaintenanceTask{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tasks)
}

func (h *Handler) CreateMaintenanceTask(w http.ResponseWriter, r *http.Request) {
	vehicleID := chi.URLParam(r, "id")
	
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)

	var t MaintenanceTask
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		h.writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid body")
		return
	}

	query := fmt.Sprintf(`
		INSERT INTO %s.tm_maintenance_tasks (company_code, vehicle_id, task_name, description, interval_km, interval_hours, last_service_km, last_service_hours, last_service_date) 
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id
	`, schema)
	
	err := tenant.NewReadRouter(claims.CompanyCode).QueryRow(r.Context(), query, 
		claims.CompanyCode, vehicleID, t.TaskName, t.Description, t.IntervalKM, t.IntervalHours, t.LastServiceKM, t.LastServiceHours, time.Now()).Scan(&t.ID)
	
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "failed to create task")
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(t)
}
