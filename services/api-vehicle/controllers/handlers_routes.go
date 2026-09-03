package controllers

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"

	"ajb_gps/api-vehicle/models"

	"github.com/gin-gonic/gin"
)

// routeCols = kolom routes (migration 011).
const routeCols = `id, name, waypoints, estimated_duration_sec, created_by, is_active, created_at`

func scanRouteItem(rows *sql.Rows) (models.RouteItem, error) {
	var (
		it  models.RouteItem
		wb  []byte
		est sql.NullInt64
	)
	err := rows.Scan(&it.ID, &it.Name, &wb, &est, &it.CreatedBy, &it.IsActive, &it.CreatedAt)
	if err != nil {
		return it, err
	}
	if est.Valid {
		e := int(est.Int64)
		it.EstimatedDurationSec = &e
	}
	if len(wb) > 0 {
		_ = json.Unmarshal(wb, &it.Waypoints)
	}
	return it, nil
}

// loadAssignments reads route_assignments for a route.
func loadAssignments(db *sql.DB, routeID uint64) ([]models.AssignmentItem, error) {
	rows, err := db.Query(
		`SELECT id, route_id, vehicle_id, driver_user_id, status,
		        started_at, completed_at, deviation_meters, created_at
		 FROM route_assignments WHERE route_id = ? ORDER BY id`, routeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []models.AssignmentItem{}
	for rows.Next() {
		var (
			a       models.AssignmentItem
			started sql.NullTime
			done    sql.NullTime
			dev     sql.NullFloat64
		)
		if err := rows.Scan(&a.ID, &a.RouteID, &a.VehicleID, &a.DriverUserID, &a.Status,
			&started, &done, &dev, &a.CreatedAt); err != nil {
			return nil, err
		}
		a.StartedAt = nullableTimeP(started)
		a.CompletedAt = nullableTimeP(done)
		a.DeviationMeters = nullableFloat(dev)
		items = append(items, a)
	}
	return items, rows.Err()
}

// GET /routes — list + assignments. Non-admin hanya melihat route yang punya
// assignment ke vehicle yang bisa diaksesnya.
func routesListHandler(c *gin.Context) {
	db, err := companyRead(c) // B4 HA: READ → replica
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	rows, err := db.Query(`SELECT ` + routeCols + ` FROM routes ORDER BY id`)
	if err != nil {
		slog.Error("routes list failed", "error", err)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	defer rows.Close()

	all := make([]models.RouteItem, 0, 16)
	for rows.Next() {
		it, err := scanRouteItem(rows)
		if err != nil {
			slog.Error("route scan failed", "error", err)
			writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
			return
		}
		all = append(all, it)
	}
	if err := rows.Err(); err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	items := make([]models.RouteItem, 0, len(all))
	for _, r := range all {
		asg, err := loadAssignments(db, r.ID)
		if err != nil {
			continue // log-less degradation; detail endpoint akan error jelas
		}
		r.Assignments = asg
		if isAdmin(c) {
			items = append(items, r)
			continue
		}
		allowed := accessibleVehicleIDs(c)
		for _, a := range asg {
			if _, ok := allowed[a.VehicleID]; ok {
				items = append(items, r)
				break
			}
		}
	}
	writeSuccess(c, http.StatusOK, items)
}

// POST /routes (Admin/Manager) — buat route; created_by = company user id.
func routesCreateHandler(c *gin.Context) {
	if !requireAdminOrManager(c) {
		return
	}
	var req models.CreateRouteRequest
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Waypoints) < 2 {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST",
			"name and at least 2 waypoints ({lat,lon}) are required")
		return
	}
	db, err := companyDB(c)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	u, _ := loadAuthUser(c)

	wps, _ := json.Marshal(req.Waypoints)
	res, err := db.Exec(
		`INSERT INTO routes (name, waypoints, estimated_duration_sec, created_by, is_active)
		 VALUES (?, ?, ?, ?, TRUE)`,
		req.Name, wps, req.EstimatedDurationSec, u.CompanyUserID,
	)
	if err != nil {
		slog.Error("route create failed", "error", err)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	newID, _ := res.LastInsertId()
	slog.Info("route created", "company", u.CompanyCode, "route_id", newID, "by_user", u.CompanyUserID)
	writeSuccess(c, http.StatusCreated, gin.H{"id": newID, "name": req.Name})
}

// GET /routes/:id — detail + assignments.
func routesDetailHandler(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	db, err := companyRead(c) // B4 HA: READ → replica
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	rows, err := db.Query(`SELECT `+routeCols+` FROM routes WHERE id = ?`, id)
	if err != nil {
		slog.Error("route detail query failed", "error", err, "route_id", id)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	defer rows.Close()
	if !rows.Next() {
		writeError(c, http.StatusNotFound, "ROUTE_NOT_FOUND", "route not found")
		return
	}
	item, err := scanRouteItem(rows)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	item.Assignments, err = loadAssignments(db, item.ID)
	if err != nil {
		slog.Error("assignments load failed", "error", err, "route_id", item.ID)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	writeSuccess(c, http.StatusOK, item)
}
