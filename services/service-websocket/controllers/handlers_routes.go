package controllers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ajb_gps/service-websocket/models"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// Routes endpoints — RBAC scoped (user_vehicles + ownership). ADMIN unrestricted.
// Skema company migration 011 (routes) + 012 (route_assignments).
//   routes:  id, name, waypoints JSON, estimated_duration_sec, created_by,
//            is_active, created_at, updated_at
//   route_assignments: id, route_id, vehicle_id, driver_user_id, status,
//            started_at, completed_at, deviation_meters, created_at
// ---------------------------------------------------------------------------

type routeRow struct {
	ID                   uint64
	Name                 string
	Waypoints            []byte
	EstimatedDurationSec sql.NullInt64
	CreatedBy            uint64
	IsActive             bool
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

func nullableIntPtr(n sql.NullInt64) *int {
	if !n.Valid {
		return nil
	}
	v := int(n.Int64)
	return &v
}

// fetchRouteByID loads a single, active route.
func fetchRouteByID(db *sql.DB, id uint64) (*routeRow, error) {
	var r routeRow
	err := db.QueryRow(`SELECT id, name, waypoints, estimated_duration_sec, created_by, is_active, created_at, updated_at
FROM routes WHERE id = ? AND is_active = TRUE`, id).
		Scan(&r.ID, &r.Name, &r.Waypoints, &r.EstimatedDurationSec, &r.CreatedBy,
			&r.IsActive, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// loadAssignments loads all active route_assignments of a route with vehicle imei.
func loadAssignments(db *sql.DB, routeID uint64) []models.RouteAssignmentItem {
	out := make([]models.RouteAssignmentItem, 0)
	rows, err := db.Query(`SELECT ra.id, ra.route_id, ra.vehicle_id, ra.driver_user_id, ra.status,
ra.started_at, ra.completed_at, ra.deviation_meters, COALESCE(v.imei,'')
FROM route_assignments ra
LEFT JOIN vehicles v ON v.id = ra.vehicle_id AND v.deleted_at IS NULL
WHERE ra.route_id = ? ORDER BY ra.id`, routeID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		item := models.RouteAssignmentItem{}
		var driver sql.NullInt64
		var started, completed sql.NullTime
		var status sql.NullString
		if err := rows.Scan(&item.ID, &item.RouteID, &item.VehicleID, &driver, &status,
			&started, &completed, &item.DeviationMeters, &item.IMEI); err != nil {
			continue
		}
		item.DriverUserID = nullableUint(driver)
		item.Status = status.String
		item.StartedAt = nullableTimeP(started)
		item.CompletedAt = nullableTimeP(completed)
		out = append(out, item)
	}
	return out
}

func routeToItem(db *sql.DB, r *routeRow) models.RouteItem {
	item := models.RouteItem{
		ID:          r.ID,
		Name:        r.Name,
		CreatedBy:   r.CreatedBy,
		IsActive:    r.IsActive,
		CreatedAt:   r.CreatedAt,
		UpdatedAt:   r.UpdatedAt,
		Waypoints:   []models.RouteWaypoint{},
		Assignments: []models.RouteAssignmentItem{},
	}
	item.EstimatedDurationSec = nullableIntPtr(r.EstimatedDurationSec)
	if len(r.Waypoints) > 0 {
		_ = json.Unmarshal(r.Waypoints, &item.Waypoints)
	}
	if db != nil {
		item.Assignments = loadAssignments(db, r.ID)
	}
	if item.Waypoints == nil {
		item.Waypoints = []models.RouteWaypoint{}
	}
	return item
}

// routeAccessible reports whether the caller may see/touch a route: admin → all;
// non-admin → any assignment whose vehicle is accessible, or driver is caller.
func routeAccessible(c *gin.Context, db *sql.DB, r *routeRow) bool {
	if isAdmin(c) {
		return true
	}
	u, _ := loadAuthUser(c)
	allowed := accessibleVehicleIDsFromCtx(c)
	argsv := []interface{}{r.ID}
	cond := "1=0"
	if len(allowed) > 0 {
		cond = "ra.vehicle_id IN (" + placeholders(len(allowed)) + ")"
		for id := range allowed {
			argsv = append(argsv, id)
		}
	}
	var n int
	// driver access: ra.driver_user_id = master user id (no company access mapping yet)
	if err := db.QueryRow(`SELECT COUNT(*) FROM route_assignments ra WHERE ra.route_id = ? AND (`+cond+` OR ra.driver_user_id = ?)`,
		append(argsv, u.CompanyUserID)...).Scan(&n); err != nil {
		return false
	}
	return n > 0
}

// routesListHandler handles GET /api/v1/routes.
func routesListHandler(c *gin.Context) {
	db, err := companyRead(c) // B4 HA: READ → replica
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	allowed := accessibleVehicleIDsFromCtx(c)
	u, _ := loadAuthUser(c)

	var (
		cond string
		args []interface{}
	)
	if !isAdmin(c) {
		if len(allowed) == 0 {
			writeSuccess(c, http.StatusOK, []models.RouteItem{})
			return
		}
		cond = `r.id IN (SELECT ra.route_id FROM route_assignments ra
WHERE ra.vehicle_id IN (` + placeholders(len(allowed)) + `)
OR ra.driver_user_id = ?)`
		for id := range allowed {
			args = append(args, id)
		}
		args = append(args, u.CompanyUserID)
	}

	q := `SELECT r.id, r.name, r.waypoints, r.estimated_duration_sec, r.created_by,
r.is_active, r.created_at, r.updated_at
FROM routes r WHERE r.is_active = TRUE`
	if cond != "" {
		q += ` AND ` + cond
	}
	q += ` ORDER BY r.id DESC`

	rows, err := db.Query(q, args...)
	if err != nil {
		slog.Error("routes list query failed", "error", err)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	defer rows.Close()

	items := make([]models.RouteItem, 0)
	for rows.Next() {
		var r routeRow
		if err := rows.Scan(&r.ID, &r.Name, &r.Waypoints, &r.EstimatedDurationSec,
			&r.CreatedBy, &r.IsActive, &r.CreatedAt, &r.UpdatedAt); err != nil {
			slog.Error("route scan failed", "error", err)
			writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
			return
		}
		items = append(items, routeToItem(db, &r))
	}
	if err := rows.Err(); err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	writeSuccess(c, http.StatusOK, items)
}

// routesCreateHandler handles POST /api/v1/routes (Admin-only).
func routesCreateHandler(c *gin.Context) {
	if !isAdmin(c) {
		writeError(c, http.StatusForbidden, "FORBIDDEN", "only admins can manage routes")
		return
	}
	u, _ := loadAuthUser(c)
	db, err := companyDB(c)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	var req models.RouteCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid JSON body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Waypoints) < 2 {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "name and at least 2 waypoints are required")
		return
	}

	wpJSON, err := json.Marshal(req.Waypoints)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	createdBy := u.CompanyUserID
	if createdBy == 0 {
		createdBy = u.ID
	}
	tx, err := db.BeginTx(c.Request.Context(), nil)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.Exec(`INSERT INTO routes (name, waypoints, estimated_duration_sec, created_by)
VALUES (?, ?, ?, ?)`, req.Name, wpJSON, req.EstimatedDurationSec, createdBy)
	if err != nil {
		slog.Error("route create failed", "error", err)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	rid, _ := res.LastInsertId()

	for _, vid := range req.VehicleIDs {
		if _, err := tx.Exec(`INSERT INTO route_assignments (route_id, vehicle_id, driver_user_id)
VALUES (?, ?, ?)`, rid, vid, req.DriverUserID); err != nil {
			slog.Error("route assignment failed", "error", err, "route_id", rid, "vehicle_id", vid)
			writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
			return
		}
	}
	if err := tx.Commit(); err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	r, err := fetchRouteByID(db, uint64(rid))
	if err != nil {
		writeSuccess(c, http.StatusCreated, models.RouteItem{ID: uint64(rid), Name: req.Name, Waypoints: req.Waypoints})
		return
	}
	writeSuccess(c, http.StatusCreated, routeToItem(db, r))
}

// routeDetailHandler handles GET /api/v1/routes/:id.
func routeDetailHandler(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "route id must be a number")
		return
	}
	db, err := companyRead(c) // B4 HA: READ → replica
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	r, err := fetchRouteByID(db, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(c, http.StatusNotFound, "ROUTE_NOT_FOUND", "route not found")
			return
		}
		slog.Error("route detail query failed", "error", err)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	if !routeAccessible(c, db, r) {
		writeError(c, http.StatusForbidden, "FORBIDDEN", "you do not have access to this route")
		return
	}
	writeSuccess(c, http.StatusOK, routeToItem(db, r))
}

// routeTrackHandler GET /api/v1/routes/:id/track — status live + deviation.
func routeTrackHandler(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "route id must be a number")
		return
	}
	db, err := companyRead(c) // B4 HA: track playback = read-only
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	r, err := fetchRouteByID(db, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(c, http.StatusNotFound, "ROUTE_NOT_FOUND", "route not found")
			return
		}
		slog.Error("route track query failed", "error", err)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	if !routeAccessible(c, db, r) {
		writeError(c, http.StatusForbidden, "FORBIDDEN", "you do not have access to this route")
		return
	}
	assignments := loadAssignments(db, id)
	writeSuccess(c, http.StatusOK, gin.H{
		"route_id":    id,
		"name":        r.Name,
		"assignments": assignments,
	})
}

// routeDeleteHandler handles DELETE /api/v1/routes/:id (soft: is_active = false).
func routeDeleteHandler(c *gin.Context) {
	if !isAdmin(c) {
		writeError(c, http.StatusForbidden, "FORBIDDEN", "only admins can manage routes")
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "route id must be a number")
		return
	}
	db, err := companyDB(c)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	res, err := db.Exec(`UPDATE routes SET is_active = FALSE WHERE id = ? AND is_active = TRUE`, id)
	if err != nil {
		slog.Error("route delete failed", "error", err)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		writeError(c, http.StatusNotFound, "ROUTE_NOT_FOUND", "route not found")
		return
	}
	writeSuccess(c, http.StatusOK, gin.H{"id": id, "deleted": true})
}

// fetchByID is a small alias used by create (keeps signatures tidy).
func fetchByID(db *sql.DB, id uint64) (*routeRow, error) {
	return fetchRouteByID(db, id)
}

// routesUpdateHandler handles PATCH /api/v1/routes/:id (name / waypoints /
// estimated_duration_sec) — Admin-only (B3 akan memperluas status assignment).
func routesUpdateHandler(c *gin.Context) {
	if !isAdmin(c) {
		writeError(c, http.StatusForbidden, "FORBIDDEN", "only admins can manage routes")
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "route id must be a number")
		return
	}
	db, err := companyDB(c)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	var req struct {
		Name                 *string                `json:"name"`
		Waypoints            []models.RouteWaypoint `json:"waypoints"`
		EstimatedDurationSec *int                   `json:"estimated_duration_sec"`
		VehicleIDs           []uint64               `json:"vehicle_ids"` // append assignments
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid JSON body")
		return
	}

	if _, err := fetchRouteByID(db, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(c, http.StatusNotFound, "ROUTE_NOT_FOUND", "route not found")
			return
		}
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	sets := []string{}
	args := []interface{}{}
	if req.Name != nil {
		if strings.TrimSpace(*req.Name) == "" {
			writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "name must not be empty")
			return
		}
		sets = append(sets, "name = ?")
		args = append(args, *req.Name)
	}
	if req.Waypoints != nil {
		if len(req.Waypoints) < 2 {
			writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "at least 2 waypoints required")
			return
		}
		wpJSON, err := json.Marshal(req.Waypoints)
		if err != nil {
			writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
			return
		}
		sets = append(sets, "waypoints = ?")
		args = append(args, wpJSON)
	}
	if req.EstimatedDurationSec != nil {
		sets = append(sets, "estimated_duration_sec = ?")
		args = append(args, *req.EstimatedDurationSec)
	}
	if len(req.VehicleIDs) > 0 {
		for _, vid := range req.VehicleIDs {
			if _, err := db.Exec(`INSERT IGNORE INTO route_assignments (route_id, vehicle_id) VALUES (?, ?)`, id, vid); err != nil {
				writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
				return
			}
		}
	}
	if len(sets) == 0 {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "no fields to update")
		return
	}

	args = append(args, id)
	q := `UPDATE routes SET ` + joinEq(sets) + ` WHERE id = ? AND is_active = TRUE`
	res, err := db.Exec(q, args...)
	if err != nil {
		slog.Error("route update failed", "error", err)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		writeError(c, http.StatusNotFound, "ROUTE_NOT_FOUND", "route not found")
		return
	}

	updated, err := fetchRouteByID(db, id)
	if err != nil {
		writeSuccess(c, http.StatusOK, gin.H{"id": id, "updated": true})
		return
	}
	writeSuccess(c, http.StatusOK, routeToItem(db, updated))
}

// joinEq joins ["a = ?","b = ?"] with ", ".
func joinEq(parts []string) string {
	return strings.Join(parts, ", ")
}
