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

type RouteItem struct {
	ID                       int64           `json:"id"`
	Name                     string          `json:"name"`
	Waypoints                json.RawMessage `json:"waypoints"`
	DriverUserID             *int64          `json:"driver_user_id,omitempty"`
	VehicleID                *int64          `json:"vehicle_id,omitempty"`
	Status                   string          `json:"status"`
	DeviationThresholdMeters float64         `json:"deviation_threshold_meters"`
}

func (h *Handler) ListRoutes(w http.ResponseWriter, r *http.Request) {
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

	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM %s.tm_routes %s", schema, queryWhere)
	
	args = append(args, limit, offset)
	query := fmt.Sprintf(`
		SELECT id, name, waypoints, driver_user_id, vehicle_id, status, deviation_threshold_meters
		FROM %s.tm_routes
		%s
		ORDER BY id ASC LIMIT $%d OFFSET $%d
	`, schema, queryWhere, len(args)-1, len(args))

	var total int
	router := tenant.NewReadRouter(claims.CompanyCode)
	_ = router.QueryRow(r.Context(), countQuery, args[:len(args)-2]...).Scan(&total)

	rows, err := router.Query(r.Context(), query, args...)
	if err != nil {
		logger.Log.Error("Failed to query routes", "err", err)
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to retrieve routes")
		return
	}
	defer rows.Close()

	var routes []RouteItem
	for rows.Next() {
		var rt RouteItem
		var wpStr string
		if err := rows.Scan(&rt.ID, &rt.Name, &wpStr, &rt.DriverUserID, &rt.VehicleID, &rt.Status, &rt.DeviationThresholdMeters); err == nil {
			rt.Waypoints = json.RawMessage(wpStr)
			routes = append(routes, rt)
		} else {
			logger.Log.Error("Scan error", "err", err)
		}
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
		"data":   routes,
		"pagination": map[string]interface{}{
			"page":  page,
			"limit": limit,
			"total": total,
		},
	})
}

func (h *Handler) GetRoute(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}

	idStr := chi.URLParam(r, "id")
	routeID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_ID", "Route ID must be a valid integer")
		return
	}

	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)

	var rt RouteItem
	var wpStr string
	router := tenant.NewReadRouter(claims.CompanyCode)
	
	err = router.QueryRow(r.Context(), fmt.Sprintf(`
		SELECT id, name, waypoints, driver_user_id, vehicle_id, status, deviation_threshold_meters
		FROM %s.tm_routes
		WHERE id = $1 AND deleted_at IS NULL
	`, schema), routeID).Scan(&rt.ID, &rt.Name, &wpStr, &rt.DriverUserID, &rt.VehicleID, &rt.Status, &rt.DeviationThresholdMeters)
	
	if err != nil {
		h.writeError(w, http.StatusNotFound, "ROUTE_NOT_FOUND", fmt.Sprintf("Route with ID %d not found", routeID))
		return
	}
	rt.Waypoints = json.RawMessage(wpStr)

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
		"data":   rt,
	})
}

func (h *Handler) CreateRoute(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}

	var req RouteItem
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid JSON body")
		return
	}

	if req.Name == "" || len(req.Waypoints) == 0 {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Name and waypoints are required")
		return
	}

	if req.Status == "" {
		req.Status = "active"
	}
	if req.DeviationThresholdMeters == 0 {
		req.DeviationThresholdMeters = 100
	}

	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	router := dbclient.Pool

	var newID int64
	err := router.QueryRow(r.Context(), fmt.Sprintf(`
		INSERT INTO %s.tm_routes (name, waypoints, driver_user_id, vehicle_id, status, deviation_threshold_meters)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id
	`, schema), req.Name, string(req.Waypoints), req.DriverUserID, req.VehicleID, req.Status, req.DeviationThresholdMeters).Scan(&newID)
	
	if err != nil {
		logger.Log.Error("Failed to create route", "err", err)
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to create route")
		return
	}
	
	req.ID = newID
	h.auditLog(r.Context(), claims.CompanyCode, "CREATE_ROUTE", "success", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Created route %d", newID))

	h.writeJSON(w, http.StatusCreated, map[string]interface{}{
		"status": "success",
		"data":   req,
	})
}

func (h *Handler) UpdateRoute(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}

	idStr := chi.URLParam(r, "id")
	routeID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_ID", "Route ID must be a valid integer")
		return
	}

	var req RouteItem
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid JSON body")
		return
	}

	if req.Name == "" || len(req.Waypoints) == 0 {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Name and waypoints are required")
		return
	}

	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	router := dbclient.Pool

	res, err := router.Exec(r.Context(), fmt.Sprintf(`
		UPDATE %s.tm_routes SET name = $1, waypoints = $2, driver_user_id = $3, vehicle_id = $4, status = $5, deviation_threshold_meters = $6
		WHERE id = $7 AND deleted_at IS NULL
	`, schema), req.Name, string(req.Waypoints), req.DriverUserID, req.VehicleID, req.Status, req.DeviationThresholdMeters, routeID)
	
	if err != nil {
		logger.Log.Error("Failed to update route", "err", err)
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to update route")
		return
	}

	if res.RowsAffected() == 0 {
		h.writeError(w, http.StatusNotFound, "ROUTE_NOT_FOUND", "Route not found")
		return
	}

	req.ID = routeID
	h.auditLog(r.Context(), claims.CompanyCode, "UPDATE_ROUTE", "success", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Updated route %d", routeID))

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
		"data":   req,
	})
}

func (h *Handler) DeleteRoute(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}

	idStr := chi.URLParam(r, "id")
	routeID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_ID", "Route ID must be a valid integer")
		return
	}

	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	router := dbclient.Pool

	res, err := router.Exec(r.Context(), fmt.Sprintf(`
		UPDATE %s.tm_routes SET deleted_at = CURRENT_TIMESTAMP WHERE id = $1 AND deleted_at IS NULL
	`, schema), routeID)
	
	if err != nil {
		logger.Log.Error("Failed to delete route", "err", err)
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to delete route")
		return
	}

	if res.RowsAffected() == 0 {
		h.writeError(w, http.StatusNotFound, "ROUTE_NOT_FOUND", "Route not found")
		return
	}

	h.auditLog(r.Context(), claims.CompanyCode, "DELETE_ROUTE", "success", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Deleted route %d", routeID))

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
		"message": "Route deleted successfully",
	})
}

func (h *Handler) AssignRoute(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}

	idStr := chi.URLParam(r, "id")
	routeID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_ID", "Route ID must be a valid integer")
		return
	}

	var req struct {
		VehicleID    int64  `json:"vehicle_id"`
		DriverUserID *int64 `json:"driver_user_id,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid JSON body")
		return
	}

	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	router := dbclient.Pool

	// Update route
	_, err = router.Exec(r.Context(), fmt.Sprintf(`
		UPDATE %s.tm_routes SET vehicle_id = $1, driver_user_id = $2 WHERE id = $3 AND deleted_at IS NULL
	`, schema), req.VehicleID, req.DriverUserID, routeID)
	
	if err != nil {
		logger.Log.Error("Failed to update route assignment", "err", err)
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to update route assignment")
		return
	}
	
	// Create assignment log
	_, err = router.Exec(r.Context(), fmt.Sprintf(`
		INSERT INTO %s.th_route_assignments (route_id, vehicle_id, driver_user_id, status) VALUES ($1, $2, $3, 'assigned')
	`, schema), routeID, req.VehicleID, req.DriverUserID)
	
	if err != nil {
		logger.Log.Error("Failed to create route assignment log", "err", err)
	}

	h.auditLog(r.Context(), claims.CompanyCode, "ASSIGN_ROUTE", "success", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Assigned route %d to vehicle %d", routeID, req.VehicleID))

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
		"message": "Route assigned successfully",
	})
}
