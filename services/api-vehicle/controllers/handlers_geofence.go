package controllers

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
)

// POST /geofences/:id/vehicles (Admin/Manager) — tautkan vehicle ke geofence
// (geofence_vehicles, migration 006). RBAC: vehicle harus bisa diakses caller.
func geofenceVehiclesAddHandler(c *gin.Context) {
	if !requireAdminOrManager(c) {
		return
	}
	geofenceID, ok := parseIDParam(c)
	if !ok {
		return
	}
	var req struct {
		VehicleID uint64 `json:"vehicle_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.VehicleID == 0 {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "vehicle_id is required")
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

	// Validasi keberadaan kedua sisi (FK ada di DB; pesan error lebih jelas).
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM geofences WHERE id=? AND is_active=TRUE`, geofenceID).Scan(&n); err != nil || n == 0 {
		writeError(c, http.StatusNotFound, "GEOFENCE_NOT_FOUND", "geofence not found or inactive")
		return
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM vehicles WHERE id=? AND deleted_at IS NULL`, req.VehicleID).Scan(&n); err != nil || n == 0 {
		writeError(c, http.StatusNotFound, "VEHICLE_NOT_FOUND", "vehicle not found")
		return
	}

	if _, err := db.Exec(
		`INSERT IGNORE INTO geofence_vehicles (geofence_id, vehicle_id, is_enabled) VALUES (?, ?, TRUE)`,
		geofenceID, req.VehicleID,
	); err != nil {
		slog.Error("geofence vehicle add failed", "error", err, "geofence_id", geofenceID)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	writeSuccess(c, http.StatusCreated, gin.H{"geofence_id": geofenceID, "vehicle_id": req.VehicleID})
}

// DELETE /geofences/:id/vehicles/:vehicleId (Admin/Manager).
func geofenceVehiclesRemoveHandler(c *gin.Context) {
	if !requireAdminOrManager(c) {
		return
	}
	geofenceID, ok := parseIDParam(c)
	if !ok {
		return
	}
	vehicleID, err := fmtParseUint(c.Param("vehicleId"))
	if err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "vehicle id must be a number")
		return
	}
	db, err := companyDB(c)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	res, err := db.Exec(
		`DELETE FROM geofence_vehicles WHERE geofence_id = ? AND vehicle_id = ?`,
		geofenceID, vehicleID,
	)
	if err != nil {
		slog.Error("geofence vehicle remove failed", "error", err)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		writeError(c, http.StatusNotFound, "LINK_NOT_FOUND", "geofence-vehicle link not found")
		return
	}
	writeSuccess(c, http.StatusOK, gin.H{"geofence_id": geofenceID, "vehicle_id": vehicleID})
}

// GET /geofences/:id/vehicles — daftar vehicle tertaut.
func geofenceVehiclesListHandler(c *gin.Context) {
	geofenceID, ok := parseIDParam(c)
	if !ok {
		return
	}
	db, err := companyRead(c) // B4 HA: READ → replica
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	rows, err := db.Query(
		`SELECT gv.vehicle_id FROM geofence_vehicles gv WHERE gv.geofence_id = ? AND gv.is_enabled = TRUE ORDER BY gv.vehicle_id`,
		geofenceID)
	if err != nil {
		slog.Error("geofence vehicles list failed", "error", err)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	defer rows.Close()

	vehicles := []uint64{}
	for rows.Next() {
		var v uint64
		if err := rows.Scan(&v); err == nil {
			// Non-admin hanya melihat vehicle yang boleh diaksesnya.
			if canAccessVehicle(c, v) {
				vehicles = append(vehicles, v)
			}
		}
	}
	writeSuccess(c, http.StatusOK, gin.H{"geofence_id": geofenceID, "vehicles": vehicles})
}
