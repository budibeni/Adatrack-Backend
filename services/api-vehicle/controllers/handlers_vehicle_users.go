package controllers

import (
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
)

// GET /vehicles/:id/users — daftar user yang punya akses ke vehicle.
func vehicleUsersListHandler(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	if !requireVehicleAccess(c, id) {
		return
	}
	db, err := companyRead(c) // B4 HA: READ → replica
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	rows, err := db.Query(
		`SELECT uv.user_id, COALESCE(uca.role_override,''), uv.assigned_at
		 FROM user_vehicles uv LEFT JOIN user_company_access uca ON uca.user_id = uv.user_id
		 WHERE uv.vehicle_id = ? ORDER BY uv.user_id`, id)
	if err != nil {
		slog.Error("vehicle users query failed", "error", err, "vehicle_id", id)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	defer rows.Close()

	type assignment struct {
		UserID     uint64 `json:"user_id"`
		Role       string `json:"role,omitempty"`
		AssignedAt string `json:"assigned_at"`
	}
	items := []assignment{}
	for rows.Next() {
		var a assignment
		var at sql.NullTime
		var role sql.NullString
		if err := rows.Scan(&a.UserID, &role, &at); err == nil {
			a.AssignedAt = timeFmt(at)
			a.Role = role.String
			items = append(items, a)
		}
	}
	writeSuccess(c, http.StatusOK, items)
}

// POST /vehicles/:id/users (Admin) — assign user ke vehicle (user_vehicles).
func vehicleAssignUserHandler(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	if !isAdmin(c) {
		recordRBACDenial(c, "vehicle_assign", "not_admin")
		writeError(c, http.StatusForbidden, "FORBIDDEN", "admin role required")
		return
	}
	var req struct {
		UserID uint64 `json:"user_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.UserID == 0 {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "user_id is required")
		return
	}
	db, err := companyDB(c)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	var exists int
	if err := db.QueryRow(`SELECT COUNT(*) FROM vehicles WHERE id=? AND deleted_at IS NULL`, id).Scan(&exists); err != nil || exists == 0 {
		writeError(c, http.StatusNotFound, "VEHICLE_NOT_FOUND", "vehicle not found")
		return
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM user_company_access WHERE user_id=? AND is_active=TRUE`, req.UserID).Scan(&exists); err != nil || exists == 0 {
		writeError(c, http.StatusUnprocessableEntity, "USER_NOT_IN_COMPANY", "user has no access entry in this company")
		return
	}

	if _, err := db.Exec(`INSERT IGNORE INTO user_vehicles (user_id, vehicle_id) VALUES (?, ?)`, req.UserID, id); err != nil {
		slog.Error("user_vehicle insert failed", "error", err, "vehicle_id", id, "user_id", req.UserID)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	writeSuccess(c, http.StatusCreated, gin.H{"vehicle_id": id, "user_id": req.UserID})
}

// DELETE /vehicles/:id/users/:userId (Admin).
func vehicleUnassignUserHandler(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	userID, err := fmtParseUint(c.Param("userId"))
	if err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "user id must be a number")
		return
	}
	if !isAdmin(c) {
		recordRBACDenial(c, "vehicle_unassign", "not_admin")
		writeError(c, http.StatusForbidden, "FORBIDDEN", "admin role required")
		return
	}
	db, err := companyDB(c)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	if _, err := db.Exec(`DELETE FROM user_vehicles WHERE vehicle_id = ? AND user_id = ?`, id, userID); err != nil {
		slog.Error("user_vehicle delete failed", "error", err)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	writeSuccess(c, http.StatusOK, gin.H{"vehicle_id": id, "user_id": userID})
}