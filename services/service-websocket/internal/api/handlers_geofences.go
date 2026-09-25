package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"backend/internal/auth"
	"backend/internal/logger"
	"backend/internal/dbclient"
	"backend/internal/tenant"
)

type GeofenceItem struct {
	ID             int64           `json:"id"`
	Name           string          `json:"name"`
	AreaType       string          `json:"area_type"`
	Coordinates    json.RawMessage `json:"coordinates"`
	RadiusMeters   *float64        `json:"radius_meters,omitempty"`
	BoundaryPoints json.RawMessage `json:"boundary_points,omitempty"`
	CreatedBy      *int64          `json:"created_by,omitempty"`
}

func (h *Handler) ListGeofences(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)

	search := r.URL.Query().Get("search")
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 200 {
		limit = 50
	}
	offset := (page - 1) * limit

	var args []interface{}
	queryWhere := "WHERE deleted_at IS NULL"
	
	if search != "" {
		args = append(args, "%"+search+"%")
		queryWhere += fmt.Sprintf(" AND name ILIKE $%d", len(args))
	}

	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM %s.tm_geofences %s", schema, queryWhere)
	
	args = append(args, limit, offset)
	query := fmt.Sprintf(`
		SELECT id, name, area_type, coordinates, radius_meters, boundary_points, created_by
		FROM %s.tm_geofences
		%s
		ORDER BY id ASC LIMIT $%d OFFSET $%d
	`, schema, queryWhere, len(args)-1, len(args))

	var total int
	router := tenant.NewReadRouter(claims.CompanyCode)
	_ = router.QueryRow(r.Context(), countQuery, args[:len(args)-2]...).Scan(&total)

	rows, err := router.Query(r.Context(), query, args...)
	if err != nil {
		logger.Log.Error("Failed to query geofences", "err", err)
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to retrieve geofences")
		return
	}
	defer rows.Close()

	var geofences []GeofenceItem
	for rows.Next() {
		var gf GeofenceItem
		var coordsStr string
		var boundaryStr *string
		if err := rows.Scan(&gf.ID, &gf.Name, &gf.AreaType, &coordsStr, &gf.RadiusMeters, &boundaryStr, &gf.CreatedBy); err == nil {
			gf.Coordinates = json.RawMessage(coordsStr)
			if boundaryStr != nil {
				gf.BoundaryPoints = json.RawMessage(*boundaryStr)
			}
			geofences = append(geofences, gf)
		} else {
			logger.Log.Error("Scan error", "err", err)
		}
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
		"data":   geofences,
		"pagination": map[string]interface{}{
			"page":  page,
			"limit": limit,
			"total": total,
		},
	})
}

func (h *Handler) GetGeofence(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}

	idStr := chi.URLParam(r, "id")
	gfID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_ID", "Geofence ID must be a valid integer")
		return
	}

	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)

	var gf GeofenceItem
	var coordsStr string
	var boundaryStr *string
	router := tenant.NewReadRouter(claims.CompanyCode)
	
	err = router.QueryRow(r.Context(), fmt.Sprintf(`
		SELECT id, name, area_type, coordinates, radius_meters, boundary_points, created_by
		FROM %s.tm_geofences
		WHERE id = $1 AND deleted_at IS NULL
	`, schema), gfID).Scan(&gf.ID, &gf.Name, &gf.AreaType, &coordsStr, &gf.RadiusMeters, &boundaryStr, &gf.CreatedBy)
	
	if err != nil {
		h.writeError(w, http.StatusNotFound, "GEOFENCE_NOT_FOUND", fmt.Sprintf("Geofence with ID %d not found", gfID))
		return
	}
	gf.Coordinates = json.RawMessage(coordsStr)
	if boundaryStr != nil {
		gf.BoundaryPoints = json.RawMessage(*boundaryStr)
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
		"data":   gf,
	})
}

func (h *Handler) CreateGeofence(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}

	var req GeofenceItem
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid JSON body")
		return
	}

	if req.Name == "" || req.AreaType == "" || len(req.Coordinates) == 0 {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Name, area_type and coordinates are required")
		return
	}

	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	router := dbclient.Pool

	var boundaryStr *string
	if len(req.BoundaryPoints) > 0 {
		b := string(req.BoundaryPoints)
		boundaryStr = &b
	}

	var newID int64
	err := router.QueryRow(r.Context(), fmt.Sprintf(`
		INSERT INTO %s.tm_geofences (name, area_type, coordinates, radius_meters, boundary_points, created_by)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id
	`, schema), req.Name, req.AreaType, string(req.Coordinates), req.RadiusMeters, boundaryStr, claims.UserID).Scan(&newID)
	
	if err != nil {
		logger.Log.Error("Failed to create geofence", "err", err)
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to create geofence")
		return
	}
	
	req.ID = newID
	req.CreatedBy = &claims.UserID
	h.auditLog(r.Context(), claims.CompanyCode, "CREATE_GEOFENCE", "success", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Created geofence %d", newID))

	h.writeJSON(w, http.StatusCreated, map[string]interface{}{
		"status": "success",
		"data":   req,
	})
}

func (h *Handler) UpdateGeofence(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}

	idStr := chi.URLParam(r, "id")
	gfID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_ID", "Geofence ID must be a valid integer")
		return
	}

	var req GeofenceItem
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid JSON body")
		return
	}

	if req.Name == "" || req.AreaType == "" || len(req.Coordinates) == 0 {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Name, area_type and coordinates are required")
		return
	}

	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	router := dbclient.Pool

	var boundaryStr *string
	if len(req.BoundaryPoints) > 0 {
		b := string(req.BoundaryPoints)
		boundaryStr = &b
	}

	res, err := router.Exec(r.Context(), fmt.Sprintf(`
		UPDATE %s.tm_geofences SET name = $1, area_type = $2, coordinates = $3, radius_meters = $4, boundary_points = $5
		WHERE id = $6 AND deleted_at IS NULL
	`, schema), req.Name, req.AreaType, string(req.Coordinates), req.RadiusMeters, boundaryStr, gfID)
	
	if err != nil {
		logger.Log.Error("Failed to update geofence", "err", err)
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to update geofence")
		return
	}

	if res.RowsAffected() == 0 {
		h.writeError(w, http.StatusNotFound, "GEOFENCE_NOT_FOUND", "Geofence not found")
		return
	}

	req.ID = gfID
	h.auditLog(r.Context(), claims.CompanyCode, "UPDATE_GEOFENCE", "success", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Updated geofence %d", gfID))

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
		"data":   req,
	})
}

func (h *Handler) DeleteGeofence(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}

	idStr := chi.URLParam(r, "id")
	gfID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_ID", "Geofence ID must be a valid integer")
		return
	}

	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	router := dbclient.Pool

	res, err := router.Exec(r.Context(), fmt.Sprintf(`
		UPDATE %s.tm_geofences SET deleted_at = CURRENT_TIMESTAMP WHERE id = $1 AND deleted_at IS NULL
	`, schema), gfID)
	
	if err != nil {
		logger.Log.Error("Failed to delete geofence", "err", err)
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to delete geofence")
		return
	}

	if res.RowsAffected() == 0 {
		h.writeError(w, http.StatusNotFound, "GEOFENCE_NOT_FOUND", "Geofence not found")
		return
	}

	h.auditLog(r.Context(), claims.CompanyCode, "DELETE_GEOFENCE", "success", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Deleted geofence %d", gfID))

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
		"message": "Geofence deleted successfully",
	})
}

func (h *Handler) AssignGeofence(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}

	idStr := chi.URLParam(r, "id")
	gfID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_ID", "Geofence ID must be a valid integer")
		return
	}

	var req struct {
		VehicleID int64 `json:"vehicle_id"`
		Enabled   bool  `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid JSON body")
		return
	}
	
	// Default enabled to true if not explicitly set (json unmarshal sets false if missing, so we'll just use it directly, but maybe check if it's there)
	// Actually we can just require passing enabled boolean.

	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	router := dbclient.Pool

	// Upsert assignment
	_, err = router.Exec(r.Context(), fmt.Sprintf(`
		INSERT INTO %s.tm_geofence_vehicles (geofence_id, vehicle_id, enabled) VALUES ($1, $2, $3)
		ON CONFLICT (geofence_id, vehicle_id) DO UPDATE SET enabled = EXCLUDED.enabled
	`, schema), gfID, req.VehicleID, req.Enabled)
	
	if err != nil {
		logger.Log.Error("Failed to assign geofence", "err", err)
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to assign geofence")
		return
	}

	h.auditLog(r.Context(), claims.CompanyCode, "ASSIGN_GEOFENCE", "success", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Assigned geofence %d to vehicle %d (enabled: %v)", gfID, req.VehicleID, req.Enabled))

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
		"message": "Geofence assignment updated",
	})
}
