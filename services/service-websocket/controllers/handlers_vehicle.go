package controllers

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"ajb_gps/service-websocket/models"

	"github.com/gin-gonic/gin"
)

// vehicleModel is a row of the company vehicles table (migration 002).
type vehicleModel struct {
	ID           uint64
	IMEI         string
	PlateNumber  string
	DeviceModel  sql.NullString
	Status       string
	LastSeenAt   sql.NullTime
	CurrentLat   sql.NullFloat64
	CurrentLon   sql.NullFloat64
	CurrentSpeed sql.NullFloat64
}

const vehicleSelectCols = `v.id, v.imei, v.plate_number, v.device_model, v.status,
v.last_seen_at, v.current_latitude, v.current_longitude, v.current_speed`

// vehiclesListHandler handles GET /api/v1/vehicles (RBAC filtered + pagination).
func vehiclesListHandler(c *gin.Context) {
	page, limit := paginationParams(c)
	db, err := companyRead(c) // B4 HA: READ → replica
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	allowed := accessibleVehicleIDsFromCtx(c)

	total, err := countAccessibleVehicles(db, isAdmin(c), allowed)
	if err != nil {
		slog.Error("vehicles count failed", "error", err)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	vehicles, err := queryVehicles(db, isAdmin(c), allowed, page, limit)
	if err != nil {
		slog.Error("vehicles list query failed", "error", err)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	items := enrichVehicles(c, vehicles)
	writeSuccess(c, http.StatusOK, items, &models.PaginationInfo{
		Page: page, Limit: limit, Total: total,
	})
}

// countAccessibleVehicles counts vehicles visible to the caller within company.
func countAccessibleVehicles(db *sql.DB, allAdmin bool, allowed map[uint64]struct{}) (int64, error) {
	var total int64
	if allAdmin {
		err := db.QueryRow(
			`SELECT COUNT(*) FROM vehicles v WHERE v.deleted_at IS NULL`,
		).Scan(&total)
		return total, err
	}
	if len(allowed) == 0 {
		return 0, nil
	}
	args := make([]interface{}, 0, len(allowed))
	for id := range allowed {
		args = append(args, id)
	}
	err := db.QueryRow(
		`SELECT COUNT(*) FROM vehicles v
 WHERE v.deleted_at IS NULL AND v.id IN (`+placeholders(len(args))+`)`,
		args...,
	).Scan(&total)
	return total, err
}

// queryVehicles loads a page of vehicles according to RBAC scope (company DB).
func queryVehicles(db *sql.DB, allAdmin bool, allowed map[uint64]struct{},
	page, limit int) ([]vehicleModel, error) {

	q := `SELECT ` + vehicleSelectCols + ` FROM vehicles v WHERE v.deleted_at IS NULL`
	args := []interface{}{}

	if !allAdmin {
		if len(allowed) == 0 {
			return []vehicleModel{}, nil
		}
		q += ` AND v.id IN (` + placeholders(len(allowed)) + `)`
		for id := range allowed {
			args = append(args, id)
		}
	}

	q += ` ORDER BY v.id LIMIT ? OFFSET ?`
	args = append(args, limit, (page-1)*limit)

	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	vehicles := make([]vehicleModel, 0, limit)
	for rows.Next() {
		var v vehicleModel
		if err := rows.Scan(&v.ID, &v.IMEI, &v.PlateNumber, &v.DeviceModel, &v.Status,
			&v.LastSeenAt, &v.CurrentLat, &v.CurrentLon, &v.CurrentSpeed); err != nil {
			return nil, err
		}
		vehicles = append(vehicles, v)
	}
	return vehicles, rows.Err()
}

// loadVehicleByID returns a non-deleted vehicle row from the company DB.
func loadVehicleByID(db *sql.DB, id uint64) (*vehicleModel, error) {
	v := &vehicleModel{}
	err := db.QueryRow(
		`SELECT `+vehicleSelectCols+` FROM vehicles v WHERE v.id = ? AND v.deleted_at IS NULL`, id,
	).Scan(&v.ID, &v.IMEI, &v.PlateNumber, &v.DeviceModel, &v.Status,
		&v.LastSeenAt, &v.CurrentLat, &v.CurrentLon, &v.CurrentSpeed)
	if err != nil {
		return nil, err
	}
	return v, nil
}

// vehicleDetailHandler handles GET /api/v1/vehicles/:id.
func vehicleDetailHandler(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "vehicle id must be a number")
		return
	}
	db, err := companyRead(c) // B4 HA: READ → replica
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	v, err := loadVehicleByID(db, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(c, http.StatusNotFound, "VEHICLE_NOT_FOUND", "vehicle not found")
			return
		}
		slog.Error("vehicle detail query failed", "error", err, "vehicle_id", id)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	if !requireVehicleAccess(c, id) {
		return
	}

	items := enrichVehicles(c, []vehicleModel{*v})
	if len(items) == 0 {
		writeError(c, http.StatusNotFound, "VEHICLE_NOT_FOUND", "vehicle not found")
		return
	}
	writeSuccess(c, http.StatusOK, items[0])
}

// vehicleHistoryHandler handles GET /api/v1/vehicles/{id}/history?start=&end=.
// Reads the partitioned telemetry_logs via the (imei, timestamp) index
// (FR-6.5: query 30 hari < 1.5 s).
func vehicleHistoryHandler(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "vehicle id must be a number")
		return
	}
	db, err := companyRead(c) // B4 HA: history = berat dibaca → replica
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	v, err := loadVehicleByID(db, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(c, http.StatusNotFound, "VEHICLE_NOT_FOUND", "vehicle not found")
			return
		}
		slog.Error("vehicle detail query failed", "error", err, "vehicle_id", id)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	if !requireVehicleAccess(c, id) {
		return
	}

	end := time.Now()
	start := end.Add(-24 * time.Hour)
	if s := c.Query("start"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			writeError(c, http.StatusBadRequest, "INVALID_PARAM", "start must be RFC3339 (e.g. 2026-08-01T00:00:00Z)")
			return
		}
		start = t
	}
	if e := c.Query("end"); e != "" {
		t, err := time.Parse(time.RFC3339, e)
		if err != nil {
			writeError(c, http.StatusBadRequest, "INVALID_PARAM", "end must be RFC3339 (e.g. 2026-08-16T23:59:59Z)")
			return
		}
		end = t
	}
	if !end.After(start) {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "end must be after start")
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

	// telemetry_logs company schema: kolom timestamp (DATETIME) ter-partisi.
	var total int64
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM telemetry_logs WHERE imei = ? AND timestamp BETWEEN ? AND ?`,
		v.IMEI, start, end,
	).Scan(&total); err != nil {
		slog.Error("history count failed", "error", err, "imei", v.IMEI)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	// B4 perf: ORDER BY timestamp DESC — SEARAH dgn indeks (imei, timestamp
	// DESC) sehingga MySQL streaming-stop di `limit` baris pertama TANPA
	// filesort atas ratusan ribu entri (SLA 30 hari < 1.5s; lihat
	// backend/cmd/querybench). Urutan ASC utk playback dikembalikan di aplikasi.
	rows, err := db.Query(
		`SELECT timestamp, latitude, longitude, speed, heading
 FROM telemetry_logs
 WHERE imei = ? AND timestamp BETWEEN ? AND ?
 ORDER BY timestamp DESC
 LIMIT ?`,
		v.IMEI, start, end, limit,
	)
	if err != nil {
		slog.Error("history query failed", "error", err, "imei", v.IMEI)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	defer rows.Close()

	points := make([]models.HistoryPoint, 0, limit)
	for rows.Next() {
		var ts time.Time
		var p models.HistoryPoint
		if err := rows.Scan(&ts, &p.Lat, &p.Lon, &p.Speed, &p.Heading); err != nil {
			slog.Error("history scan failed", "error", err)
			writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
			return
		}
		p.Timestamp = ts.UTC().Format(time.RFC3339)
		points = append(points, p)
	}
	if err := rows.Err(); err != nil {
		slog.Error("history rows error", "error", err)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	// Balik DESC → ASC (kontrak playback FR-5.3 tetap kronologis).
	for i, j := 0, len(points)-1; i < j; i, j = i+1, j-1 {
		points[i], points[j] = points[j], points[i]
	}

	writeSuccessWithTotal(c, http.StatusOK, points, total)
}
