package controllers

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
)

// speedCols = kolom list speed_configs (migration 007).
const speedCols = `id, vehicle_id, speed_limit_kmh, COALESCE(grace_margin_kmh,0), is_active, created_at`

// GET /speed-configs — semua user terautentikasi boleh melihat.
func speedConfigsListHandler(c *gin.Context) {
	db, err := companyRead(c) // B4 HA: READ → replica
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	rows, err := db.Query(`SELECT ` + speedCols + ` FROM speed_configs ORDER BY (vehicle_id IS NULL) DESC, id`)
	if err != nil {
		slog.Error("speed configs list failed", "error", err)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	defer rows.Close()

	items := make([]gin.H, 0)
	for rows.Next() {
		var (
			id      uint64
			vid     *int64
			limit   float64
			margin  float64
			active  bool
			created any
		)
		if err := rows.Scan(&id, &vid, &limit, &margin, &active, &created); err != nil {
			slog.Error("speed config scan failed", "error", err)
			writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
			return
		}
		items = append(items, gin.H{
			"id":               id,
			"vehicle_id":       vid,
			"speed_limit_kmh":  limit,
			"grace_margin_kmh": margin,
			"is_active":        active,
			"created_at":       created,
		})
	}
	if err := rows.Err(); err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	writeSuccess(c, http.StatusOK, items)
}

// POST /speed-configs (Admin). vehicle_id dihilangkan = konfigurasi global.
func speedConfigsCreateHandler(c *gin.Context) {
	if !requireRole(c, "Admin") {
		return
	}
	var req struct {
		VehicleID      *uint64 `json:"vehicle_id,omitempty"`
		SpeedLimitKMH  float64 `json:"speed_limit_kmh"`
		GraceMarginKMH float64 `json:"grace_margin_kmh"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.SpeedLimitKMH <= 0 {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "speed_limit_kmh must be > 0")
		return
	}
	db, err := companyDB(c)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	var vidArg interface{}
	if req.VehicleID != nil && *req.VehicleID > 0 {
		// Validasi vehicle ada.
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM vehicles WHERE id=? AND deleted_at IS NULL`, *req.VehicleID).Scan(&n); err != nil || n == 0 {
			writeError(c, http.StatusNotFound, "VEHICLE_NOT_FOUND", "vehicle not found")
			return
		}
		vidArg = *req.VehicleID
	}
	res, err := db.Exec(
		`INSERT INTO speed_configs (vehicle_id, speed_limit_kmh, grace_margin_kmh, is_active)
		 VALUES (?, ?, ?, TRUE)`,
		vidArg, req.SpeedLimitKMH, req.GraceMarginKMH,
	)
	if err != nil {
		slog.Error("speed config create failed", "error", err)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	newID, _ := res.LastInsertId()
	writeSuccess(c, http.StatusCreated, gin.H{"id": newID})
}

// PATCH /speed-configs/:id (Admin).
func speedConfigsUpdateHandler(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	if !requireRole(c, "Admin") {
		return
	}
	var req struct {
		SpeedLimitKMH  *float64 `json:"speed_limit_kmh"`
		GraceMarginKMH *float64 `json:"grace_margin_kmh"`
		IsActive       *bool    `json:"is_active"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if req.SpeedLimitKMH != nil && *req.SpeedLimitKMH <= 0 {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "speed_limit_kmh must be > 0")
		return
	}
	db, err := companyDB(c)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	q := `UPDATE speed_configs SET`
	args := []interface{}{}
	apply := func(col string, v interface{}) {
		if len(args) > 0 {
			q += ","
		}
		q += " " + col + " = ?"
		args = append(args, v)
	}
	if req.SpeedLimitKMH != nil {
		apply("speed_limit_kmh", *req.SpeedLimitKMH)
	}
	if req.GraceMarginKMH != nil {
		apply("grace_margin_kmh", *req.GraceMarginKMH)
	}
	if req.IsActive != nil {
		apply("is_active", *req.IsActive)
	}
	if len(args) == 0 {
		writeError(c, http.StatusBadRequest, "EMPTY_UPDATE", "no fields to update")
		return
	}
	q += ` WHERE id = ?`
	args = append(args, id)

	res, err := db.Exec(q, args...)
	if err != nil {
		slog.Error("speed config update failed", "error", err, "config_id", id)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		writeError(c, http.StatusNotFound, "SPEED_CONFIG_NOT_FOUND", "speed config not found or no change")
		return
	}
	writeSuccess(c, http.StatusOK, gin.H{"id": id})
}

// DELETE /speed-configs/:id (Admin).
func speedConfigsDeleteHandler(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	if !requireRole(c, "Admin") {
		return
	}
	db, err := companyDB(c)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	if _, err := db.Exec(`DELETE FROM speed_configs WHERE id = ?`, id); err != nil {
		slog.Error("speed config delete failed", "error", err, "config_id", id)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	writeSuccess(c, http.StatusOK, gin.H{"id": id})
}