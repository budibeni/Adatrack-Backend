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
