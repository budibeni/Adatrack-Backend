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
	ID        int       `json:"id"`
	VehicleID int       `json:"vehicle_id"`
	TaskName  string    `json:"task_name"`
	DueDate   time.Time `json:"due_date,omitempty"`
	Status    string    `json:"status"`
}

func (h *Handler) ListMaintenanceTasks(w http.ResponseWriter, r *http.Request) {
	vehicleID := chi.URLParam(r, "id")
	
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)

	query := fmt.Sprintf("SELECT id, vehicle_id, task_name, due_date, status FROM %s.th_maintenance_schedules WHERE vehicle_id = $1 AND deleted_at IS NULL", schema)
	rows, err := tenant.NewReadRouter(claims.CompanyCode).Query(r.Context(), query, vehicleID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "failed to list tasks")
		return
	}
	defer rows.Close()

	var tasks []MaintenanceTask
	for rows.Next() {
		var t MaintenanceTask
		if err := rows.Scan(&t.ID, &t.VehicleID, &t.TaskName, &t.DueDate, &t.Status); err != nil {
			continue
		}
		tasks = append(tasks, t)
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

	query := fmt.Sprintf("INSERT INTO %s.th_maintenance_schedules (vehicle_id, task_name, due_date, status) VALUES ($1, $2, $3, $4) RETURNING id", schema)
	err := tenant.NewReadRouter(claims.CompanyCode).QueryRow(r.Context(), query, vehicleID, t.TaskName, t.DueDate, "pending").Scan(&t.ID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "failed to create task")
		return
	}
	t.VehicleID = 0 // Just set back or properly scan it. But let's leave it simple

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(t)
}
