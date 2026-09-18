package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"

	"backend/internal/auth"
	"backend/internal/config"
	"backend/internal/dbclient"
	"backend/internal/logger"
	"backend/internal/tenant"
	"backend/internal/models"
	"backend/internal/redclient"
	"backend/internal/storage"
)

type Handler struct {
	cfg       *config.Config
	store     *storage.S3Store
	Validator *validator.Validate
}

func NewHandler(cfg *config.Config, store *storage.S3Store) *Handler {
	return &Handler{
		cfg:       cfg,
		store:     store,
		Validator: validator.New(),
	}
}

type ErrorResponse struct {
	Status    string `json:"status"`
	ErrorCode string `json:"error_code"`
	Message   string `json:"message"`
	Timestamp string `json:"timestamp"`
}

func (h *Handler) writeError(w http.ResponseWriter, status int, errorCode, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(ErrorResponse{
		Status:    "error",
		ErrorCode: errorCode,
		Message:   msg,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
}

func (h *Handler) writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func (h *Handler) auditLog(ctx context.Context, companyCode, action, outcome string, userID int64, email, role, detail string) {
	if dbclient.Pool == nil {
		return
	}
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

// -------------------------------------------------------------------------
// VEHICLES CRUD
// -------------------------------------------------------------------------

type VehicleRequest struct {
	IMEI        string  `json:"imei" validate:"required"`
	PlateNumber string  `json:"plate_number" validate:"required"`
	Make        string  `json:"make"`
	Model       string  `json:"model"`
	DriverID    *int    `json:"driver_id,omitempty"`
}

func (h *Handler) CreateVehicle(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}
	if claims.Role != "Admin" && claims.Role != "SuperAdmin" && claims.Role != "Manager" {
		h.writeError(w, http.StatusForbidden, "FORBIDDEN", "Only Admin or Manager can create vehicles")
		return
	}

	var req VehicleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid request body")
		return
	}
	if err := h.Validator.Struct(req); err != nil {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}

	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)

	tx, err := dbclient.Pool.Begin(r.Context())
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Transaction failed")
		return
	}
	defer tx.Rollback(r.Context())

	var vehicleID int
	err = tx.QueryRow(r.Context(), fmt.Sprintf(`
		INSERT INTO %s.tm_vehicles (imei, plate_number, make, model, status) 
		VALUES ($1, $2, $3, $4, 'active') RETURNING id`, schema), req.IMEI, req.PlateNumber, req.Make, req.Model).Scan(&vehicleID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to insert vehicle")
		return
	}

	// Sync to master IMEI map
	_, err = tx.Exec(r.Context(), `
		INSERT INTO adatrack_gps_master.tm_vehicle_imei_map (imei, company_code, vehicle_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (imei) DO UPDATE SET company_code = EXCLUDED.company_code, vehicle_id = EXCLUDED.vehicle_id
	`, req.IMEI, claims.CompanyCode, vehicleID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to sync vehicle IMEI map")
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Commit failed")
		return
	}

	h.auditLog(r.Context(), claims.CompanyCode, "VEHICLE_CREATED", "success", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Vehicle %d created (IMEI: %s)", vehicleID, req.IMEI))
	h.writeJSON(w, http.StatusCreated, map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"id":           vehicleID,
			"imei":         req.IMEI,
			"plate_number": req.PlateNumber,
		},
	})
}

func (h *Handler) ListVehicles(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)

	query := fmt.Sprintf(`
		SELECT id, imei, COALESCE(plate_number, ''), COALESCE(make, ''), COALESCE(model, ''), status, COALESCE(odometer_km, 0), COALESCE(engine_hours, 0)
		FROM %s.tm_vehicles WHERE deleted_at IS NULL ORDER BY id ASC
	`, schema)

	rows, err := tenant.NewReadRouter(claims.CompanyCode).Query(r.Context(), query)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to list vehicles")
		return
	}
	defer rows.Close()

	vehicles := make([]map[string]interface{}, 0)
	for rows.Next() {
		var id int
		var imei, plate, make, model, status string
		var odo, hrs float64
		if err := rows.Scan(&id, &imei, &plate, &make, &model, &status, &odo, &hrs); err == nil {
			vData := map[string]interface{}{
				"id":           id,
				"imei":         imei,
				"plate_number": plate,
				"make":         make,
				"model":        model,
				"status":       status,
				"odometer_km":  odo,
				"engine_hours": hrs,
			}
			redisKey := fmt.Sprintf("adatrack_gps:%s:vehicle:state:%s", claims.CompanyCode, imei)
			if val, err := redclient.Client.Get(r.Context(), redisKey).Result(); err == nil && val != "" {
				var state models.TelemetryPayload
				if err := json.Unmarshal([]byte(val), &state); err == nil {
					vData["lat"] = state.Latitude
					vData["lon"] = state.Longitude
					vData["speed"] = state.Speed
					vData["acc_status"] = state.ACCStatus
					vData["fuel_level"] = state.FuelLevel
					vData["fuel_volume"] = state.FuelVolume
					vData["fuel_temp_c"] = state.FuelTempC
					vData["satellites"] = state.Satellites
					vData["altitude"] = state.Altitude
					vData["gsm_signal"] = state.GSMSignal
				}
			}
			vehicles = append(vehicles, vData)
		}
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
		"data":   vehicles,
	})
}

func (h *Handler) GetVehicle(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}
	idStr := chi.URLParam(r, "id")
	id, _ := strconv.Atoi(idStr)
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)

	var v struct {
		ID          int     `json:"id"`
		IMEI        string  `json:"imei" validate:"required"`
		PlateNumber string  `json:"plate_number" validate:"required"`
		Make        string  `json:"make"`
		Model       string  `json:"model"`
		Status      string  `json:"status"`
		OdometerKM  float64 `json:"odometer_km"`
		EngineHours float64 `json:"engine_hours"`
		Lat         *float64 `json:"lat,omitempty"`
		Lon         *float64 `json:"lon,omitempty"`
		Speed       *float64 `json:"speed,omitempty"`
		ACCStatus   *int16   `json:"acc_status,omitempty"`
		FuelLevel   *float64 `json:"fuel_level,omitempty"`
		FuelVolume  *float64 `json:"fuel_volume,omitempty"`
		FuelTempC   *float64 `json:"fuel_temp_c,omitempty"`
		Satellites  *int     `json:"satellites,omitempty"`
		Altitude    *float64 `json:"altitude,omitempty"`
		GSMSignal   *int     `json:"gsm_signal,omitempty"`
	}

	err := tenant.NewReadRouter(claims.CompanyCode).QueryRow(r.Context(), fmt.Sprintf(`
		SELECT id, imei, COALESCE(plate_number, ''), COALESCE(make, ''), COALESCE(model, ''), status, COALESCE(odometer_km, 0), COALESCE(engine_hours, 0)
		FROM %s.tm_vehicles WHERE id = $1 AND deleted_at IS NULL
	`, schema), id).Scan(&v.ID, &v.IMEI, &v.PlateNumber, &v.Make, &v.Model, &v.Status, &v.OdometerKM, &v.EngineHours)
	if err != nil {
		h.writeError(w, http.StatusNotFound, "VEHICLE_NOT_FOUND", fmt.Sprintf("Vehicle %d not found", id))
		return
	}

	redisKey := fmt.Sprintf("adatrack_gps:%s:vehicle:state:%s", claims.CompanyCode, v.IMEI)
	if val, err := redclient.Client.Get(r.Context(), redisKey).Result(); err == nil && val != "" {
		var state models.TelemetryPayload
		if err := json.Unmarshal([]byte(val), &state); err == nil {
			v.Lat = &state.Latitude
			v.Lon = &state.Longitude
			v.Speed = &state.Speed
			v.ACCStatus = &state.ACCStatus
			v.FuelLevel = state.FuelLevel
			v.FuelVolume = state.FuelVolume
			v.FuelTempC = state.FuelTempC
			v.Satellites = &state.Satellites
			v.Altitude = &state.Altitude
			v.GSMSignal = &state.GSMSignal
		}
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
		"data":   v,
	})
}

func (h *Handler) UpdateVehicle(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}
	if claims.Role != "Admin" && claims.Role != "SuperAdmin" && claims.Role != "Manager" {
		h.writeError(w, http.StatusForbidden, "FORBIDDEN", "Only Admin or Manager can update vehicles")
		return
	}

	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)

	var req VehicleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid JSON body")
		return
	}

	_, err := dbclient.Pool.Exec(r.Context(), fmt.Sprintf(`
		UPDATE %s.tm_vehicles 
		SET plate_number = COALESCE(NULLIF($1, ''), plate_number),
		    make = COALESCE(NULLIF($2, ''), make),
		    model = COALESCE(NULLIF($3, ''), model)
		WHERE id = $4 AND deleted_at IS NULL
	`, schema), req.PlateNumber, req.Make, req.Model, id)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to update vehicle")
		return
	}

	h.auditLog(r.Context(), claims.CompanyCode, "VEHICLE_UPDATED", "success", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Vehicle %d updated", id))
	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success"})
}

func (h *Handler) SoftDeleteVehicle(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}
	if claims.Role != "Admin" && claims.Role != "SuperAdmin" && claims.Role != "Manager" {
		h.writeError(w, http.StatusForbidden, "FORBIDDEN", "Only Admin or Manager can delete vehicles")
		return
	}

	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)

	_, err := dbclient.Pool.Exec(r.Context(), fmt.Sprintf(`UPDATE %s.tm_vehicles SET deleted_at = NOW() WHERE id = $1`, schema), id)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to soft delete vehicle")
		return
	}

	h.auditLog(r.Context(), claims.CompanyCode, "ENTITY_SOFT_DELETED", "success", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Vehicle %d soft-deleted", id))
	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success"})
}

func (h *Handler) RestoreVehicle(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}
	if claims.Role != "Admin" && claims.Role != "SuperAdmin" && claims.Role != "Manager" {
		h.writeError(w, http.StatusForbidden, "FORBIDDEN", "Only Admin or Manager can restore vehicles")
		return
	}

	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)

	_, err := dbclient.Pool.Exec(r.Context(), fmt.Sprintf(`UPDATE %s.tm_vehicles SET deleted_at = NULL WHERE id = $1`, schema), id)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to restore vehicle")
		return
	}

	h.auditLog(r.Context(), claims.CompanyCode, "ENTITY_RESTORED", "success", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Vehicle %d restored", id))
	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success"})
}

// -------------------------------------------------------------------------
// GEOFENCES CRUD
// -------------------------------------------------------------------------

type GeofenceRequest struct {
	Name           string          `json:"name" validate:"required"`
	AreaType       string          `json:"area_type" validate:"required"` // circle or polygon
	Coordinates    json.RawMessage `json:"coordinates"`
	RadiusMeters   *float64        `json:"radius_meters,omitempty"`
	BoundaryPoints json.RawMessage `json:"boundary_points,omitempty"`
	VehicleIDs     []int           `json:"vehicle_ids,omitempty"`
}

func (h *Handler) CreateGeofence(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}
	if claims.Role != "Admin" && claims.Role != "SuperAdmin" && claims.Role != "Manager" {
		h.writeError(w, http.StatusForbidden, "FORBIDDEN", "Only Admin or Manager can manage geofences")
		return
	}

	var req GeofenceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid JSON body")
		return
	}
	if err := h.Validator.Struct(req); err != nil {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}
	if len(req.Coordinates) == 0 {
		req.Coordinates = json.RawMessage("{}")
	}

	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)

	var id int
	err := dbclient.Pool.QueryRow(r.Context(), fmt.Sprintf(`
		INSERT INTO %s.tm_geofences (name, area_type, coordinates, radius_meters, boundary_points, created_by)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id
	`, schema), req.Name, req.AreaType, req.Coordinates, req.RadiusMeters, req.BoundaryPoints, claims.UserID).Scan(&id)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to create geofence")
		return
	}

	// Map assigned vehicles
	for _, vid := range req.VehicleIDs {
		dbclient.Pool.Exec(r.Context(), fmt.Sprintf(`
			INSERT INTO %s.tm_geofence_vehicles (geofence_id, vehicle_id)
			VALUES ($1, $2) ON CONFLICT DO NOTHING
		`, schema), id, vid)
	}

	h.auditLog(r.Context(), claims.CompanyCode, "GEOFENCE_CREATED", "success", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Geofence %d created: %s", id, req.Name))
	h.writeJSON(w, http.StatusCreated, map[string]interface{}{
		"status": "success",
		"data":   map[string]interface{}{"id": id, "name": req.Name, "area_type": req.AreaType},
	})
}

func (h *Handler) ListGeofences(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)

	rows, err := tenant.NewReadRouter(claims.CompanyCode).Query(r.Context(), fmt.Sprintf(`
		SELECT id, name, area_type, coordinates, radius_meters, boundary_points, created_by 
		FROM %s.tm_geofences WHERE deleted_at IS NULL ORDER BY id ASC
	`, schema))
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to list geofences")
		return
	}
	defer rows.Close()

	geofences := make([]map[string]interface{}, 0)
	for rows.Next() {
		var id, createdBy int
		var name, areaType string
		var coords, bounds json.RawMessage
		var radius *float64
		if err := rows.Scan(&id, &name, &areaType, &coords, &radius, &bounds, &createdBy); err == nil {
			geofences = append(geofences, map[string]interface{}{
				"id":              id,
				"name":            name,
				"area_type":       areaType,
				"coordinates":     coords,
				"radius_meters":   radius,
				"boundary_points": bounds,
				"created_by":      createdBy,
			})
		}
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
		"data":   geofences,
	})
}

func (h *Handler) GetGeofence(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)

	var g struct {
		ID             int             `json:"id"`
		Name           string          `json:"name" validate:"required"`
		AreaType       string          `json:"area_type" validate:"required"`
		Coordinates    json.RawMessage `json:"coordinates"`
		RadiusMeters   *float64        `json:"radius_meters"`
		BoundaryPoints json.RawMessage `json:"boundary_points"`
		CreatedBy      int             `json:"created_by"`
	}

	err := tenant.NewReadRouter(claims.CompanyCode).QueryRow(r.Context(), fmt.Sprintf(`
		SELECT id, name, area_type, coordinates, radius_meters, boundary_points, created_by 
		FROM %s.tm_geofences WHERE id = $1 AND deleted_at IS NULL
	`, schema), id).Scan(&g.ID, &g.Name, &g.AreaType, &g.Coordinates, &g.RadiusMeters, &g.BoundaryPoints, &g.CreatedBy)
	if err != nil {
		h.writeError(w, http.StatusNotFound, "GEOFENCE_NOT_FOUND", "Geofence not found")
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
		"data":   g,
	})
}

func (h *Handler) UpdateGeofence(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)

	var req GeofenceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid JSON body")
		return
	}

	_, err := dbclient.Pool.Exec(r.Context(), fmt.Sprintf(`
		UPDATE %s.tm_geofences
		SET name = COALESCE(NULLIF($1, ''), name),
		    radius_meters = COALESCE($2, radius_meters)
		WHERE id = $3 AND deleted_at IS NULL
	`, schema), req.Name, req.RadiusMeters, id)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to update geofence")
		return
	}

	h.auditLog(r.Context(), claims.CompanyCode, "GEOFENCE_UPDATED", "success", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Geofence %d updated", id))
	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success"})
}

func (h *Handler) SoftDeleteGeofence(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)

	_, err := dbclient.Pool.Exec(r.Context(), fmt.Sprintf(`UPDATE %s.tm_geofences SET deleted_at = NOW() WHERE id = $1`, schema), id)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to soft delete geofence")
		return
	}

	h.auditLog(r.Context(), claims.CompanyCode, "ENTITY_SOFT_DELETED", "success", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Geofence %d deleted", id))
	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success"})
}

func (h *Handler) RestoreGeofence(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)

	_, err := dbclient.Pool.Exec(r.Context(), fmt.Sprintf(`UPDATE %s.tm_geofences SET deleted_at = NULL WHERE id = $1`, schema), id)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to restore geofence")
		return
	}

	h.auditLog(r.Context(), claims.CompanyCode, "ENTITY_RESTORED", "success", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Geofence %d restored", id))
	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success"})
}

// -------------------------------------------------------------------------
// ROUTES CRUD & ASSIGNMENTS
// -------------------------------------------------------------------------

type RouteRequest struct {
	Name                     string          `json:"name"`
	Waypoints                json.RawMessage `json:"waypoints"`
	DriverUserID             *int            `json:"driver_user_id,omitempty"`
	VehicleID                *int            `json:"vehicle_id,omitempty"`
	DeviationThresholdMeters float64         `json:"deviation_threshold_meters"`
}

func (h *Handler) CreateRoute(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}
	if claims.Role != "Admin" && claims.Role != "SuperAdmin" && claims.Role != "Manager" {
		h.writeError(w, http.StatusForbidden, "FORBIDDEN", "Only Admin or Manager can manage routes")
		return
	}

	var req RouteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid JSON body")
		return
	}
	if req.Name == "" || len(req.Waypoints) == 0 {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "name and waypoints are required")
		return
	}
	if req.DeviationThresholdMeters <= 0 {
		req.DeviationThresholdMeters = 200 // default 200m
	}

	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)

	var id int
	err := dbclient.Pool.QueryRow(r.Context(), fmt.Sprintf(`
		INSERT INTO %s.tm_routes (name, waypoints, driver_user_id, vehicle_id, status, deviation_threshold_meters)
		VALUES ($1, $2, $3, $4, 'active', $5) RETURNING id
	`, schema), req.Name, req.Waypoints, req.DriverUserID, req.VehicleID, req.DeviationThresholdMeters).Scan(&id)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to create route")
		return
	}

	// If vehicle is assigned, create entry in th_route_assignments
	if req.VehicleID != nil && *req.VehicleID > 0 {
		dbclient.Pool.Exec(r.Context(), fmt.Sprintf(`
			INSERT INTO %s.th_route_assignments (route_id, vehicle_id, driver_user_id, status)
			VALUES ($1, $2, $3, 'assigned')
		`, schema), id, *req.VehicleID, req.DriverUserID)
	}

	h.auditLog(r.Context(), claims.CompanyCode, "ROUTE_CREATED", "success", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Route %d created: %s", id, req.Name))
	h.writeJSON(w, http.StatusCreated, map[string]interface{}{
		"status": "success",
		"data":   map[string]interface{}{"id": id, "name": req.Name},
	})
}

func (h *Handler) ListRoutes(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)

	rows, err := tenant.NewReadRouter(claims.CompanyCode).Query(r.Context(), fmt.Sprintf(`
		SELECT id, name, waypoints, driver_user_id, vehicle_id, status, deviation_threshold_meters
		FROM %s.tm_routes WHERE deleted_at IS NULL ORDER BY id ASC
	`, schema))
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to list routes")
		return
	}
	defer rows.Close()

	routes := make([]map[string]interface{}, 0)
	for rows.Next() {
		var id int
		var name, status string
		var waypoints json.RawMessage
		var driverID, vehicleID *int
		var threshold float64
		if err := rows.Scan(&id, &name, &waypoints, &driverID, &vehicleID, &status, &threshold); err == nil {
			routes = append(routes, map[string]interface{}{
				"id":                         id,
				"name":                       name,
				"waypoints":                  waypoints,
				"driver_user_id":             driverID,
				"vehicle_id":                 vehicleID,
				"status":                     status,
				"deviation_threshold_meters": threshold,
			})
		}
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
		"data":   routes,
	})
}

func (h *Handler) GetRoute(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)

	var route struct {
		ID                       int             `json:"id"`
		Name                     string          `json:"name"`
		Waypoints                json.RawMessage `json:"waypoints"`
		DriverUserID             *int            `json:"driver_user_id"`
		VehicleID                *int            `json:"vehicle_id"`
		Status                   string          `json:"status"`
		DeviationThresholdMeters float64         `json:"deviation_threshold_meters"`
	}

	err := tenant.NewReadRouter(claims.CompanyCode).QueryRow(r.Context(), fmt.Sprintf(`
		SELECT id, name, waypoints, driver_user_id, vehicle_id, status, deviation_threshold_meters
		FROM %s.tm_routes WHERE id = $1 AND deleted_at IS NULL
	`, schema), id).Scan(&route.ID, &route.Name, &route.Waypoints, &route.DriverUserID, &route.VehicleID, &route.Status, &route.DeviationThresholdMeters)
	if err != nil {
		h.writeError(w, http.StatusNotFound, "ROUTE_NOT_FOUND", "Route not found")
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
		"data":   route,
	})
}

func (h *Handler) SoftDeleteRoute(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)

	_, err := dbclient.Pool.Exec(r.Context(), fmt.Sprintf(`UPDATE %s.tm_routes SET deleted_at = NOW() WHERE id = $1`, schema), id)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to soft delete route")
		return
	}

	h.auditLog(r.Context(), claims.CompanyCode, "ENTITY_SOFT_DELETED", "success", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Route %d deleted", id))
	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success"})
}

func (h *Handler) RestoreRoute(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)

	_, err := dbclient.Pool.Exec(r.Context(), fmt.Sprintf(`UPDATE %s.tm_routes SET deleted_at = NULL WHERE id = $1`, schema), id)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to restore route")
		return
	}

	h.auditLog(r.Context(), claims.CompanyCode, "ENTITY_RESTORED", "success", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Route %d restored", id))
	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success"})
}

func (h *Handler) AssignRoute(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}
	routeID, _ := strconv.Atoi(chi.URLParam(r, "id"))
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)

	var req struct {
		VehicleID    int  `json:"vehicle_id"`
		DriverUserID *int `json:"driver_user_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.VehicleID <= 0 {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "vehicle_id is required")
		return
	}

	var assignID int64
	err := dbclient.Pool.QueryRow(r.Context(), fmt.Sprintf(`
		INSERT INTO %s.th_route_assignments (route_id, vehicle_id, driver_user_id, status)
		VALUES ($1, $2, $3, 'assigned') RETURNING id
	`, schema), routeID, req.VehicleID, req.DriverUserID).Scan(&assignID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to assign route")
		return
	}

	h.writeJSON(w, http.StatusCreated, map[string]interface{}{
		"status": "success",
		"data":   map[string]interface{}{"assignment_id": assignID, "status": "assigned"},
	})
}

func (h *Handler) UpdateRouteStatus(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}
	routeID, _ := strconv.Atoi(chi.URLParam(r, "id"))
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)

	var req struct {
		Status string `json:"status"` // assigned, in_progress, completed, cancelled
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Status == "" {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "status is required")
		return
	}

	_, err := dbclient.Pool.Exec(r.Context(), fmt.Sprintf(`
		UPDATE %s.th_route_assignments SET status = $1 WHERE route_id = $2
	`, schema), req.Status, routeID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to update assignment status")
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success"})
}

// -------------------------------------------------------------------------
// SPEED CONFIGS CRUD
// -------------------------------------------------------------------------

type SpeedConfigRequest struct {
	VehicleID          *int    `json:"vehicle_id,omitempty"` // null = global
	MaxSpeedKMH        float64 `json:"max_speed_kmh"`
	GraceMarginPercent float64 `json:"grace_margin_percent"`
	AlertSeverity      string  `json:"alert_severity"`
	Enabled            bool    `json:"enabled"`
}

func (h *Handler) CreateSpeedConfig(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}
	var req SpeedConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.MaxSpeedKMH <= 0 {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Valid max_speed_kmh is required")
		return
	}
	if req.AlertSeverity == "" {
		req.AlertSeverity = "medium"
	}

	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	var id int
	err := dbclient.Pool.QueryRow(r.Context(), fmt.Sprintf(`
		INSERT INTO %s.tm_speed_configs (vehicle_id, max_speed_kmh, grace_margin_percent, alert_severity, enabled)
		VALUES ($1, $2, $3, $4, $5) RETURNING id
	`, schema), req.VehicleID, req.MaxSpeedKMH, req.GraceMarginPercent, req.AlertSeverity, req.Enabled).Scan(&id)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to create speed config")
		return
	}

	h.writeJSON(w, http.StatusCreated, map[string]interface{}{
		"status": "success",
		"data":   map[string]interface{}{"id": id},
	})
}

func (h *Handler) ListSpeedConfigs(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)

	rows, err := tenant.NewReadRouter(claims.CompanyCode).Query(r.Context(), fmt.Sprintf(`
		SELECT id, vehicle_id, max_speed_kmh, grace_margin_percent, alert_severity, enabled
		FROM %s.tm_speed_configs WHERE deleted_at IS NULL ORDER BY id ASC
	`, schema))
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to list speed configs")
		return
	}
	defer rows.Close()

	list := make([]map[string]interface{}, 0)
	for rows.Next() {
		var id int
		var vehicleID *int
		var maxSpeed, grace float64
		var severity string
		var enabled bool
		if err := rows.Scan(&id, &vehicleID, &maxSpeed, &grace, &severity, &enabled); err == nil {
			list = append(list, map[string]interface{}{
				"id":                   id,
				"vehicle_id":           vehicleID,
				"max_speed_kmh":        maxSpeed,
				"grace_margin_percent": grace,
				"alert_severity":       severity,
				"enabled":              enabled,
			})
		}
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
		"data":   list,
	})
}

func (h *Handler) SoftDeleteSpeedConfig(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)

	_, err := dbclient.Pool.Exec(r.Context(), fmt.Sprintf(`UPDATE %s.tm_speed_configs SET deleted_at = NOW() WHERE id = $1`, schema), id)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to delete speed config")
		return
	}
	h.auditLog(r.Context(), claims.CompanyCode, "SPEED_CONFIG_DELETED", "success", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Speed config %d soft deleted", id))
	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success"})
}

func (h *Handler) RestoreSpeedConfig(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)

	_, err := dbclient.Pool.Exec(r.Context(), fmt.Sprintf(`UPDATE %s.tm_speed_configs SET deleted_at = NULL WHERE id = $1`, schema), id)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to restore speed config")
		return
	}
	h.auditLog(r.Context(), claims.CompanyCode, "SPEED_CONFIG_RESTORED", "success", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Speed config %d restored", id))
	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success"})
}

// -------------------------------------------------------------------------
// NOTIFICATION PREFERENCES
// -------------------------------------------------------------------------

type NotificationPrefRequest struct {
	AlertType   string `json:"alert_type"`
	Channel     string `json:"channel"`
	Enabled     bool   `json:"enabled"`
	MinSeverity string `json:"min_severity"`
}

func (h *Handler) GetNotificationPreferences(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)

	rows, err := tenant.NewReadRouter(claims.CompanyCode).Query(r.Context(), fmt.Sprintf(`
		SELECT id, alert_type, channel, enabled, min_severity 
		FROM %s.tm_notification_preferences WHERE user_id = $1
	`, schema), claims.UserID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to retrieve preferences")
		return
	}
	defer rows.Close()

	prefs := make([]map[string]interface{}, 0)
	for rows.Next() {
		var id int
		var alertType, channel, minSev string
		var enabled bool
		if err := rows.Scan(&id, &alertType, &channel, &enabled, &minSev); err == nil {
			prefs = append(prefs, map[string]interface{}{
				"id":           id,
				"alert_type":   alertType,
				"channel":      channel,
				"enabled":      enabled,
				"min_severity": minSev,
			})
		}
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
		"data":   prefs,
	})
}

func (h *Handler) SetNotificationPreference(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}
	var req NotificationPrefRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.AlertType == "" || req.Channel == "" {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "alert_type and channel are required")
		return
	}
	if req.MinSeverity == "" {
		req.MinSeverity = "low"
	}

	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)

	// Upsert preference
	_, err := dbclient.Pool.Exec(r.Context(), fmt.Sprintf(`
		INSERT INTO %s.tm_notification_preferences (user_id, alert_type, channel, enabled, min_severity)
		VALUES ($1, $2, $3, $4, $5)
	`, schema), claims.UserID, req.AlertType, req.Channel, req.Enabled, req.MinSeverity)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to set preference")
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success"})
}

// -------------------------------------------------------------------------
// ALERTS LIFECYCLE (ACK / RESOLVE)
// -------------------------------------------------------------------------

func (h *Handler) ListAlerts(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)

	statusFilter := r.URL.Query().Get("status")
	var query string
	var args []interface{}

	if statusFilter != "" {
		query = fmt.Sprintf(`
			SELECT id, type, severity, vehicle_id, lat, lon, metadata, status, created_at 
			FROM %s.th_alerts WHERE status = $1 ORDER BY created_at DESC LIMIT 100
		`, schema)
		args = append(args, statusFilter)
	} else {
		query = fmt.Sprintf(`
			SELECT id, type, severity, vehicle_id, lat, lon, metadata, status, created_at 
			FROM %s.th_alerts ORDER BY created_at DESC LIMIT 100
		`, schema)
	}

	rows, err := tenant.NewReadRouter(claims.CompanyCode).Query(r.Context(), query, args...)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to list alerts")
		return
	}
	defer rows.Close()

	alerts := make([]map[string]interface{}, 0)
	for rows.Next() {
		var id int64
		var alertType, severity, status string
		var vehicleID int
		var lat, lon *float64
		var meta json.RawMessage
		var createdAt time.Time

		if err := rows.Scan(&id, &alertType, &severity, &vehicleID, &lat, &lon, &meta, &status, &createdAt); err == nil {
			alerts = append(alerts, map[string]interface{}{
				"id":         id,
				"type":       alertType,
				"severity":   severity,
				"vehicle_id": vehicleID,
				"lat":        lat,
				"lon":        lon,
				"metadata":   meta,
				"status":     status,
				"created_at": createdAt,
			})
		}
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
		"data":   alerts,
	})
}

func (h *Handler) AcknowledgeAlert(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)

	_, err := dbclient.Pool.Exec(r.Context(), fmt.Sprintf(`
		UPDATE %s.th_alerts 
		SET status = 'acknowledged', acknowledged_by = $1
		WHERE id = $2 AND status = 'open'
	`, schema), claims.UserID, id)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to acknowledge alert")
		return
	}

	h.auditLog(r.Context(), claims.CompanyCode, "ALERT_ACKNOWLEDGED", "success", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Alert %d acknowledged", id))
	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success"})
}

func (h *Handler) ResolveAlert(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)

	_, err := dbclient.Pool.Exec(r.Context(), fmt.Sprintf(`
		UPDATE %s.th_alerts 
		SET status = 'resolved', resolved_at = NOW()
		WHERE id = $1
	`, schema), id)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to resolve alert")
		return
	}

	h.auditLog(r.Context(), claims.CompanyCode, "ALERT_RESOLVED", "success", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Alert %d resolved", id))
	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success"})
}

// -------------------------------------------------------------------------
// FUEL CONFIGS & HISTORY
// -------------------------------------------------------------------------

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
		h.writeError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid JSON body")
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
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to create fuel config")
		return
	}

	h.auditLog(r.Context(), claims.CompanyCode, "ENTITY_CREATED", "success", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Fuel config %d created", id))
	h.writeJSON(w, http.StatusCreated, map[string]interface{}{"status": "success", "data": map[string]interface{}{"id": id}})
}

func (h *Handler) UpdateFuelConfig(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	var req FuelConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid JSON body")
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
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to update fuel config")
		return
	}

	h.auditLog(r.Context(), claims.CompanyCode, "ENTITY_UPDATED", "success", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Fuel config %d updated", id))
	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success"})
}

func (h *Handler) GetFuelHistory(w http.ResponseWriter, r *http.Request) {
	vid, _ := strconv.Atoi(chi.URLParam(r, "id"))
	claims := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)

	start := r.URL.Query().Get("start")
	end := r.URL.Query().Get("end")

	query := fmt.Sprintf(`
		SELECT id, vehicle_id, fuel_level, volume_liters, temperature_c, lat, lon, timestamp 
		FROM %s.th_fuel_logs 
		WHERE vehicle_id = $1 AND timestamp >= $2 AND timestamp <= $3
		ORDER BY timestamp DESC LIMIT 1000
	`, schema)

	rows, err := tenant.NewReadRouter(claims.CompanyCode).Query(r.Context(), query, vid, start, end)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to fetch fuel logs")
		return
	}
	defer rows.Close()

	var logs []map[string]interface{}
	for rows.Next() {
		var id, vID int
		var level, vol, temp, lat, lon *float64
		var ts time.Time
		if err := rows.Scan(&id, &vID, &level, &vol, &temp, &lat, &lon, &ts); err == nil {
			logs = append(logs, map[string]interface{}{
				"id": id, "vehicle_id": vID, "fuel_level": level, "volume_liters": vol, "temperature_c": temp, "lat": lat, "lon": lon, "timestamp": ts,
			})
		}
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "data": logs})
}
