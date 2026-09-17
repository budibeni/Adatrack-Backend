package api

import (
	"encoding/json"
	"net/http"

	"backend/internal/auth"
	"github.com/go-chi/chi/v5"
)

type MaintenanceTask struct {
	ID               string   `json:"id"`
	VehicleID        int      `json:"vehicle_id"`
	TaskName         string   `json:"task_name"`
	Description      string   `json:"description,omitempty"`
	IntervalKM       *float64 `json:"interval_km,omitempty"`
	IntervalHours    *float64 `json:"interval_hours,omitempty"`
	LastServiceKM    *float64 `json:"last_service_km,omitempty"`
	LastServiceHours *float64 `json:"last_service_hours,omitempty"`
}

func (h *Handler) ListMaintenanceTasks(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	vehicleID := chi.URLParam(r, "id")

	rows, err := h.db.Query(r.Context(), `
		SELECT id, vehicle_id, task_name, description, interval_km, interval_hours, last_service_km, last_service_hours
		FROM tm_maintenance_tasks
		WHERE company_code = $1 AND vehicle_id = $2 AND deleted_at IS NULL
	`, claims.CompanyCode, vehicleID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var tasks []MaintenanceTask
	for rows.Next() {
		var t MaintenanceTask
		err := rows.Scan(&t.ID, &t.VehicleID, &t.TaskName, &t.Description, &t.IntervalKM, &t.IntervalHours, &t.LastServiceKM, &t.LastServiceHours)
		if err != nil { continue }
		tasks = append(tasks, t)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tasks)
}

func (h *Handler) CreateMaintenanceTask(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	vehicleID := chi.URLParam(r, "id")

	var t MaintenanceTask
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	var id string
	err := h.db.QueryRow(r.Context(), `
		INSERT INTO tm_maintenance_tasks (company_code, vehicle_id, task_name, description, interval_km, interval_hours)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id
	`, claims.CompanyCode, vehicleID, t.TaskName, t.Description, t.IntervalKM, t.IntervalHours).Scan(&id)
	
	if err != nil {
		http.Error(w, "failed to create task", http.StatusInternalServerError)
		return
	}
	
	t.ID = id
	t.VehicleID = 0 // Just for response simplicity
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(t)
}
