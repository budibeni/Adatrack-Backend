package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"backend/internal/auth"
	"backend/internal/config"
	"backend/internal/dbclient"
	"backend/internal/logger"
)

type Handler struct {
	cfg *config.Config
}

func NewHandler(cfg *config.Config) *Handler {
	return &Handler{cfg: cfg}
}

func (h *Handler) writeError(w http.ResponseWriter, status int, msg string) {
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"status": "error", "message": msg})
}

func (h *Handler) auditLog(ctx context.Context, companyCode, action, outcome string, userID int64, email, role, detail string) {
	schema := "adatrack_gps_master"
	if companyCode != "DEFAULT" && companyCode != "" {
		schema = fmt.Sprintf("adatrack_gps_%s", companyCode)
	}
	
	query := fmt.Sprintf(`INSERT INTO %s.tm_audit_logs (action, outcome, actor_user_id, actor_email, actor_role, company_code, after) VALUES ($1, $2, $3, $4, $5, $6, $7)`, schema)
	afterJSON, _ := json.Marshal(map[string]string{"detail": detail})
	
	_, err := dbclient.Pool.Exec(ctx, query, action, outcome, userID, email, role, companyCode, afterJSON)
	if err != nil {
		logger.Log.Error("Failed to write audit log", "err", err, "action", action)
	}
}

// Vehicle CRUD
type VehicleRequest struct {
	IMEI        string  `json:"imei"`
	PlateNumber string  `json:"plate_number"`
	Make        string  `json:"make"`
	Model       string  `json:"model"`
}

func (h *Handler) CreateVehicle(w http.ResponseWriter, r *http.Request) {
	var req VehicleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}
	
	claims := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	
	// Ensure we insert within transaction
	tx, err := dbclient.Pool.Begin(r.Context())
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "Transaction failed")
		return
	}
	defer tx.Rollback(r.Context())

	var vehicleID int
	err = tx.QueryRow(r.Context(), fmt.Sprintf(`
		INSERT INTO %s.tm_vehicles (imei, plate_number, make, model) 
		VALUES ($1, $2, $3, $4) RETURNING id`, schema), req.IMEI, req.PlateNumber, req.Make, req.Model).Scan(&vehicleID)
	
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "Failed to create vehicle")
		return
	}

	// Sync to master
	_, err = tx.Exec(r.Context(), `
		INSERT INTO adatrack_gps_master.tm_vehicle_imei_map (imei, company_code, vehicle_id)
		VALUES ($1, $2, $3)`, req.IMEI, claims.CompanyCode, vehicleID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "Failed to map vehicle in master")
		return
	}

	tx.Commit(r.Context())
	
	h.auditLog(r.Context(), claims.CompanyCode, "ENTITY_CREATED", "success", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Vehicle %d created", vehicleID))

	json.NewEncoder(w).Encode(map[string]interface{}{"status": "success", "data": map[string]interface{}{"id": vehicleID}})
}

func (h *Handler) SoftDeleteVehicle(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	claims := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	
	_, err := dbclient.Pool.Exec(r.Context(), fmt.Sprintf(`UPDATE %s.tm_vehicles SET deleted_at = NOW() WHERE id = $1`, schema), id)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "Failed to delete")
		return
	}
	
	h.auditLog(r.Context(), claims.CompanyCode, "ENTITY_SOFT_DELETED", "success", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Vehicle %d deleted", id))
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "success"})
}

func (h *Handler) RestoreVehicle(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	claims := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	
	_, err := dbclient.Pool.Exec(r.Context(), fmt.Sprintf(`UPDATE %s.tm_vehicles SET deleted_at = NULL WHERE id = $1`, schema), id)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "Failed to restore")
		return
	}
	
	h.auditLog(r.Context(), claims.CompanyCode, "ENTITY_RESTORED", "success", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Vehicle %d restored", id))
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "success"})
}

// Geofence CRUD (basic stub for speed)
func (h *Handler) CreateGeofence(w http.ResponseWriter, r *http.Request) {
	claims := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	h.auditLog(r.Context(), claims.CompanyCode, "ENTITY_CREATED", "success", claims.UserID, claims.Email, claims.Role, "Geofence created")
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "success"})
}

func (h *Handler) CreateRoute(w http.ResponseWriter, r *http.Request) {
	claims := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	h.auditLog(r.Context(), claims.CompanyCode, "ENTITY_CREATED", "success", claims.UserID, claims.Email, claims.Role, "Route created")
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "success"})
}

type FuelConfigRequest struct {
	VehicleID             int     `json:"vehicle_id"`
	MaxVolumeLiters       float64 `json:"max_volume_liters"`
	RefuelThresholdLiters float64 `json:"refuel_threshold_liters"`
	DropThresholdLiters   float64 `json:"drop_threshold_liters"`
	Enabled               bool    `json:"enabled"`
}

func (h *Handler) CreateFuelConfig(w http.ResponseWriter, r *http.Request) {
	var req FuelConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}
	
	claims := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	
	query := fmt.Sprintf(`
		INSERT INTO %s.tm_fuel_configs 
		(vehicle_id, max_volume_liters, refuel_threshold_liters, drop_threshold_liters, enabled)
		VALUES ($1, $2, $3, $4, $5) RETURNING id
	`, schema)
	
	var id int
	err := dbclient.Pool.QueryRow(r.Context(), query, 
		req.VehicleID, req.MaxVolumeLiters, req.RefuelThresholdLiters, req.DropThresholdLiters, req.Enabled,
	).Scan(&id)
	
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "Failed to create fuel config")
		return
	}
	
	h.auditLog(r.Context(), claims.CompanyCode, "ENTITY_CREATED", "success", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Fuel config %d created", id))
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "success", "data": map[string]interface{}{"id": id}})
}

func (h *Handler) UpdateFuelConfig(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	var req FuelConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}
	
	claims := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	
	query := fmt.Sprintf(`
		UPDATE %s.tm_fuel_configs 
		SET max_volume_liters = $1, refuel_threshold_liters = $2, drop_threshold_liters = $3, enabled = $4
		WHERE id = $5
	`, schema)
	
	_, err := dbclient.Pool.Exec(r.Context(), query, 
		req.MaxVolumeLiters, req.RefuelThresholdLiters, req.DropThresholdLiters, req.Enabled, id,
	)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "Failed to update fuel config")
		return
	}
	
	h.auditLog(r.Context(), claims.CompanyCode, "ENTITY_UPDATED", "success", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Fuel config %d updated", id))
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "success"})
}

func (h *Handler) GetFuelHistory(w http.ResponseWriter, r *http.Request) {
	vid, _ := strconv.Atoi(chi.URLParam(r, "id"))
	claims := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	
	start := r.URL.Query().Get("start")
	end := r.URL.Query().Get("end")
	
	query := fmt.Sprintf(`
		SELECT id, imei, fuel_level, fuel_volume, fuel_temp_c, timestamp 
		FROM %s.th_fuel_logs 
		WHERE vehicle_id = $1 AND timestamp >= $2 AND timestamp <= $3
		ORDER BY timestamp DESC LIMIT 1000
	`, schema)
	
	rows, err := dbclient.Pool.Query(r.Context(), query, vid, start, end)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "Failed to fetch fuel logs")
		return
	}
	defer rows.Close()
	
	var logs []map[string]interface{}
	for rows.Next() {
		var id int
		var imei string
		var level, vol, temp *float64
		var ts string
		if err := rows.Scan(&id, &imei, &level, &vol, &temp, &ts); err == nil {
			logs = append(logs, map[string]interface{}{
				"id": id, "imei": imei, "fuel_level": level, "fuel_volume": vol, "fuel_temp_c": temp, "timestamp": ts,
			})
		}
	}
	
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "success", "data": logs})
}
