package controllers

import (
	"database/sql"
	"log/slog"
	"net/http"
	"time"

	"ajb_gps/internal/dialect"

	"github.com/gin-gonic/gin"
)

// fuelCols = kolom list fuel_configs (B5a, company migration 014).
const fuelCols = `id, vehicle_id, drop_threshold, refuel_threshold, window_seconds, enabled, created_at`

// vehicleFuelHistoryHandler — GET /vehicles/:id/fuel/history?from&to (B5a).
// RBAC row-level via user_vehicles (requireVehicleAccess); format GAP #3 +
// total_records (GAP #1) konsisten dengan history playback service-websocket.
func vehicleFuelHistoryHandler(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	db, err := companyRead(c) // B4 HA: history = berat dibaca → replica
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	// Vehicle harus ada & belum di-soft-delete.
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM vehicles WHERE id=? AND deleted_at IS NULL`, id).Scan(&n); err != nil {
		slog.Error("fuel history vehicle check failed", "error", err, "vehicle_id", id)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	if n == 0 {
		writeError(c, http.StatusNotFound, "VEHICLE_NOT_FOUND", "vehicle not found")
		return
	}
	if !requireVehicleAccess(c, id) {
		return
	}

	end := time.Now()
	start := end.Add(-24 * time.Hour)
	if s := c.Query("from"); s != "" {
		t, perr := time.Parse(time.RFC3339, s)
		if perr != nil {
			writeError(c, http.StatusBadRequest, "INVALID_PARAM", "from must be RFC3339 (e.g. 2026-08-01T00:00:00Z)")
			return
		}
		start = t
	}
	if e := c.Query("to"); e != "" {
		t, perr := time.Parse(time.RFC3339, e)
		if perr != nil {
			writeError(c, http.StatusBadRequest, "INVALID_PARAM", "to must be RFC3339 (e.g. 2026-08-16T23:59:59Z)")
			return
		}
		end = t
	}
	if !end.After(start) {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "to must be after from")
		return
	}
	if end.Sub(start) > 30*24*time.Hour {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "history window is limited to 30 days")
		return
	}
	limit := atoiDefault(c.Query("limit"), 5000)
	if limit < 1 || limit > 10000 {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "limit must be between 1 and 10000")
		return
	}

	rows, err := db.Query(
		`SELECT fuel_level, fuel_volume, fuel_temp_c, timestamp
		 FROM fuel_logs WHERE vehicle_id = ? AND timestamp BETWEEN ? AND ?
		 ORDER BY timestamp DESC LIMIT ?`,
		id, start, end, limit,
	)
	if err != nil {
		slog.Error("fuel history query failed", "error", err, "vehicle_id", id)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	defer rows.Close()

	points := make([]gin.H, 0)
	for rows.Next() {
		var (
			level  sql.NullFloat64
			volume sql.NullFloat64
			tempC  sql.NullFloat64
			ts     time.Time
		)
		if err := rows.Scan(&level, &volume, &tempC, &ts); err != nil {
			slog.Error("fuel history scan failed", "error", err)
			writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
			return
		}
		p := gin.H{"timestamp": ts.UTC().Format(time.RFC3339)}
		if level.Valid {
			p["fuel_level"] = level.Float64
		}
		if volume.Valid {
			p["fuel_volume"] = volume.Float64
		}
		if tempC.Valid {
			p["fuel_temp_c"] = tempC.Float64
		}
		points = append(points, p)
	}
	if err := rows.Err(); err != nil {
		slog.Error("fuel history rows failed", "error", err)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	total := int64(len(points))
	writeSuccessWithTotal(c, http.StatusOK, points, total)
}

// GET /fuel-configs — semua user terautentikasi boleh melihat.
func fuelConfigsListHandler(c *gin.Context) {
	db, err := companyRead(c) // B4 HA: READ → replica
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	rows, err := db.Query(`SELECT ` + fuelCols + ` FROM fuel_configs ORDER BY (vehicle_id IS NULL) DESC, id`)
	if err != nil {
		slog.Error("fuel configs list failed", "error", err)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	defer rows.Close()

	items := make([]gin.H, 0)
	for rows.Next() {
		var (
			id      uint64
			vid     *int64
			drop    float64
			refuel  float64
			window  int
			enabled bool
			created any
		)
		if err := rows.Scan(&id, &vid, &drop, &refuel, &window, &enabled, &created); err != nil {
			slog.Error("fuel config scan failed", "error", err)
			writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
			return
		}
		items = append(items, gin.H{
			"id":               id,
			"vehicle_id":       vid,
			"drop_threshold":   drop,
			"refuel_threshold": refuel,
			"window_seconds":   window,
			"enabled":          enabled,
			"created_at":       created,
		})
	}
	if err := rows.Err(); err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	writeSuccess(c, http.StatusOK, items)
}

// POST /fuel-configs (Admin/Manager). vehicle_id dihilangkan = konfigurasi global.
func fuelConfigsCreateHandler(c *gin.Context) {
	if !requireAdminOrManager(c) {
		return
	}
	var req struct {
		VehicleID       *uint64  `json:"vehicle_id,omitempty"`
		DropThreshold   *float64 `json:"drop_threshold"`
		RefuelThreshold *float64 `json:"refuel_threshold"`
		WindowSeconds   *int     `json:"window_seconds,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil ||
		req.DropThreshold == nil || *req.DropThreshold <= 0 ||
		req.RefuelThreshold == nil || *req.RefuelThreshold <= 0 {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "drop_threshold and refuel_threshold must be > 0")
		return
	}
	db, err := companyDB(c)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	var vidArg interface{}
	if req.VehicleID != nil && *req.VehicleID > 0 {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM vehicles WHERE id=? AND deleted_at IS NULL`, *req.VehicleID).Scan(&n); err != nil || n == 0 {
			writeError(c, http.StatusNotFound, "VEHICLE_NOT_FOUND", "vehicle not found")
			return
		}
		vidArg = *req.VehicleID
	}
	window := 300
	if req.WindowSeconds != nil && *req.WindowSeconds > 0 {
		window = *req.WindowSeconds
	}
	// Dialect-aware id retrieval: pgx stdlib does not support LastInsertId,
	// jadi postgres memakai INSERT ... RETURNING id (internal/dialect).
	newID, err := dialect.InsertReturningID(dialect.Current(), c.Request.Context(), db,
		`INSERT INTO fuel_configs (vehicle_id, drop_threshold, refuel_threshold, window_seconds, enabled)
 VALUES (?, ?, ?, ?, TRUE)`,
		vidArg, *req.DropThreshold, *req.RefuelThreshold, window)
	if err != nil {
		slog.Error("fuel config create failed", "error", err)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	writeSuccess(c, http.StatusCreated, gin.H{"id": newID})
}

// PATCH /fuel-configs/:id (Admin/Manager).
func fuelConfigsUpdateHandler(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	if !requireAdminOrManager(c) {
		return
	}
	var req struct {
		DropThreshold   *float64 `json:"drop_threshold"`
		RefuelThreshold *float64 `json:"refuel_threshold"`
		WindowSeconds   *int     `json:"window_seconds"`
		Enabled         *bool    `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if req.DropThreshold != nil && *req.DropThreshold <= 0 {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "drop_threshold must be > 0")
		return
	}
	if req.RefuelThreshold != nil && *req.RefuelThreshold <= 0 {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "refuel_threshold must be > 0")
		return
	}
	db, err := companyDB(c)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	q := `UPDATE fuel_configs SET`
	args := []interface{}{}
	apply := func(col string, v interface{}) {
		if len(args) > 0 {
			q += ","
		}
		q += " " + col + " = ?"
		args = append(args, v)
	}
	if req.DropThreshold != nil {
		apply("drop_threshold", *req.DropThreshold)
	}
	if req.RefuelThreshold != nil {
		apply("refuel_threshold", *req.RefuelThreshold)
	}
	if req.WindowSeconds != nil && *req.WindowSeconds > 0 {
		apply("window_seconds", *req.WindowSeconds)
	}
	if req.Enabled != nil {
		apply("enabled", *req.Enabled)
	}
	if len(args) == 0 {
		writeError(c, http.StatusBadRequest, "EMPTY_UPDATE", "no fields to update")
		return
	}
	q += ` WHERE id = ?`
	args = append(args, id)

	res, err := db.Exec(q, args...)
	if err != nil {
		slog.Error("fuel config update failed", "error", err, "config_id", id)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		writeError(c, http.StatusNotFound, "FUEL_CONFIG_NOT_FOUND", "fuel config not found or no change")
		return
	}
	writeSuccess(c, http.StatusOK, gin.H{"id": id})
}

// DELETE /fuel-configs/:id (Admin/Manager).
func fuelConfigsDeleteHandler(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
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
	if _, err := db.Exec(`DELETE FROM fuel_configs WHERE id = ?`, id); err != nil {
		slog.Error("fuel config delete failed", "error", err, "config_id", id)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	writeSuccess(c, http.StatusOK, gin.H{"id": id})
}
