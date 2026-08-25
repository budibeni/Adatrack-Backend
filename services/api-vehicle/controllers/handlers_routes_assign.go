package controllers

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
)

// POST /routes/:id/assignments (Admin/Manager) — assign route ke vehicle+driver.
func routeAssignHandler(c *gin.Context) {
	routeID, ok := parseIDParam(c)
	if !ok {
		return
	}
	if !requireAdminOrManager(c) {
		return
	}
	var req struct {
		VehicleID    uint64 `json:"vehicle_id" binding:"required"`
		DriverUserID uint64 `json:"driver_user_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.VehicleID == 0 || req.DriverUserID == 0 {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "vehicle_id and driver_user_id are required")
		return
	}
	if !requireVehicleAccess(c, req.VehicleID) {
		return
	}
	db, err := companyDB(c)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM routes WHERE id=? AND is_active=TRUE`, routeID).Scan(&n); err != nil || n == 0 {
		writeError(c, http.StatusNotFound, "ROUTE_NOT_FOUND", "route not found or inactive")
		return
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM vehicles WHERE id=? AND deleted_at IS NULL`, req.VehicleID).Scan(&n); err != nil || n == 0 {
		writeError(c, http.StatusNotFound, "VEHICLE_NOT_FOUND", "vehicle not found")
		return
	}

	res, err := db.Exec(
		`INSERT INTO route_assignments (route_id, vehicle_id, driver_user_id, status)
		 VALUES (?, ?, ?, 'not_started')`,
		routeID, req.VehicleID, req.DriverUserID,
	)
	if err != nil {
		slog.Error("route assignment insert failed", "error", err, "route_id", routeID)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	newID, _ := res.LastInsertId()
	slog.Info("route assigned", "assignment_id", newID,
		"route_id", routeID, "vehicle_id", req.VehicleID, "driver", req.DriverUserID)
	writeSuccess(c, http.StatusCreated, gin.H{"id": newID, "route_id": routeID,
		"vehicle_id": req.VehicleID, "driver_user_id": req.DriverUserID})
}

// PATCH /routes/:id/assignments/:assignmentId (Admin/Manager) — transisi status.
func routeAssignmentStatusHandler(c *gin.Context) {
	routeID, ok := parseIDParam(c)
	if !ok {
		return
	}
	asgID, err := fmtParseUint(c.Param("assignmentId"))
	if err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "assignment id must be a number")
		return
	}
	if !requireAdminOrManager(c) {
		return
	}
	var req struct {
		Status string `json:"status" binding:"required,oneof=not_started in_progress completed delayed"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST",
			"status must be one of not_started|in_progress|completed|delayed")
		return
	}
	db, err := companyDB(c)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	q := `UPDATE route_assignments SET status = ?`
	switch req.Status {
	case "in_progress":
		q += `, started_at = COALESCE(started_at, NOW())`
	case "completed":
		q += `, completed_at = COALESCE(completed_at, NOW())`
	}
	q += ` WHERE id = ? AND route_id = ?`

	res, err := db.Exec(q, req.Status, asgID, routeID)
	if err != nil {
		slog.Error("route assignment status update failed", "error", err, "assignment_id", asgID)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		writeError(c, http.StatusNotFound, "ASSIGNMENT_NOT_FOUND", "assignment not found for this route")
		return
	}
	writeSuccess(c, http.StatusOK, gin.H{"id": asgID, "status": req.Status})
}

// DELETE /routes/:id/assignments/:assignmentId (Admin/Manager).
func routeUnassignHandler(c *gin.Context) {
	routeID, ok := parseIDParam(c)
	if !ok {
		return
	}
	asgID, err := fmtParseUint(c.Param("assignmentId"))
	if err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "assignment id must be a number")
		return
	}
	if !requireAdminOrManager(c) {
		return
	}
	db, err := companyDB(c)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	res, err := db.Exec(`DELETE FROM route_assignments WHERE id = ? AND route_id = ?`, asgID, routeID)
	if err != nil {
		slog.Error("route unassign failed", "error", err)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		writeError(c, http.StatusNotFound, "ASSIGNMENT_NOT_FOUND", "assignment not found for this route")
		return
	}
	writeSuccess(c, http.StatusOK, gin.H{"id": asgID})
}