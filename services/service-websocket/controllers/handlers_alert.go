package controllers

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"ajb_gps/service-websocket/models"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// Alerts endpoints — RBAC scoped via JOIN vehicles (company migration 008).
//   alerts: id, vehicle_id, alert_type, severity, description, status,
//           acknowledged_by, acknowledged_at, resolved_at,
//           vehicle_lat, vehicle_lon, created_at   (NO deleted_at column)
// ---------------------------------------------------------------------------

const alertSelectCols = `a.id, a.vehicle_id, a.alert_type, a.severity, a.description,
a.status, a.acknowledged_by, a.acknowledged_at, a.resolved_at,
a.vehicle_lat, a.vehicle_lon, a.created_at`

const alertBase = ` FROM alerts a
JOIN vehicles v ON v.id = a.vehicle_id AND v.deleted_at IS NULL`

// scanAlertJoined scans a row produced by SELECT <alertSelectCols>, v.imei <alertBase>.
func scanAlertJoined(rows *sql.Rows) (*models.AlertItem, error) {
	item := &models.AlertItem{}
	var (
		description sql.NullString
		ackBy       sql.NullInt64
		ackAt       sql.NullTime
		resolved    sql.NullTime
		latF, lonF  sql.NullFloat64
	)
	err := rows.Scan(
		&item.ID, &item.VehicleID, &item.AlertType, &item.Severity, &description,
		&item.Status, &ackBy, &ackAt, &resolved, &latF, &lonF, &item.CreatedAt,
		&item.IMEI,
	)
	if err != nil {
		return nil, err
	}
	item.Description = nullableStrP(description)
	item.AcknowledgedBy = nullableUint(ackBy)
	item.AcknowledgedAt = nullableTimeP(ackAt)
	item.ResolvedAt = nullableTimeP(resolved)
	item.VehicleLat = nullableFloat(latF)
	item.VehicleLon = nullableFloat(lonF)
	return item, nil
}

// alertsListHandler handles GET /api/v1/alerts?status=&page=&limit=.
func alertsListHandler(c *gin.Context) {
	page, limit := paginationParams(c)
	db, err := companyRead(c) // B4 HA: READ → replica
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	allowed := accessibleVehicleIDsFromCtx(c)

	var cond string
	args := []interface{}{}
	if !isAdmin(c) {
		if len(allowed) == 0 {
			writeSuccess(c, http.StatusOK, []models.AlertItem{},
				&models.PaginationInfo{Page: page, Limit: limit, Total: 0})
			return
		}
		cond += ` AND a.vehicle_id IN (` + placeholders(len(allowed)) + `)`
		args = append(args, mapKeys(allowed)...)
	}
	if s := c.Query("status"); s != "" {
		cond += ` AND a.status = ?`
		args = append(args, s)
	}

	totalQuery := `SELECT COUNT(*) FROM alerts a JOIN vehicles v ON v.id = a.vehicle_id AND v.deleted_at IS NULL WHERE 1=1` + cond
	var total int64
	if err := db.QueryRow(totalQuery, args...).Scan(&total); err != nil {
		slog.Error("alerts count query failed", "error", err)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	query := `SELECT ` + alertSelectCols + `, v.imei` + alertBase + ` WHERE 1=1` + cond +
		` ORDER BY a.created_at DESC, a.id DESC LIMIT ? OFFSET ?`
	pageArgs := append(append([]interface{}{}, args...), limit, (page-1)*limit)

	rows, err := db.Query(query, pageArgs...)
	if err != nil {
		slog.Error("alerts list query failed", "error", err)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	defer rows.Close()

	items := make([]models.AlertItem, 0, limit)
	for rows.Next() {
		item, err := scanAlertJoined(rows)
		if err != nil {
			slog.Error("alert scan failed", "error", err)
			writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
			return
		}
		items = append(items, *item)
	}
	if err := rows.Err(); err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	writeSuccess(c, http.StatusOK, items, &models.PaginationInfo{Page: page, Limit: limit, Total: total})
}

// alertAckHandler handles PATCH /api/v1/alerts/{id}/acknowledge.
func alertAckHandler(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "alert id must be a number")
		return
	}
	db, err := companyDB(c)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	// Pastikan alert milik vehicle yang bisa diakses caller (row-level security).
	var vehicleID uint64
	err = db.QueryRow(`SELECT vehicle_id FROM alerts WHERE id = ?`, id).Scan(&vehicleID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(c, http.StatusNotFound, "ALERT_NOT_FOUND", "alert not found")
			return
		}
		slog.Error("alert lookup failed", "error", err, "alert_id", id)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	if !requireVehicleAccess(c, vehicleID) {
		return
	}

	u, _ := loadAuthUser(c)
	ackBy := u.CompanyUserID
	if ackBy == 0 {
		ackBy = u.ID
	}
	res, err := db.Exec(`UPDATE alerts
SET status = 'acknowledged', acknowledged_by = ?, acknowledged_at = NOW()
WHERE id = ? AND status != 'resolved'`, ackBy, id)
	if err != nil {
		slog.Error("alert ack update failed", "error", err, "alert_id", id)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		writeError(c, http.StatusBadRequest, "ALERT_NOT_UPDATABLE",
			"alert cannot be acknowledged (already resolved or missing)")
		return
	}

	item, err := alertByID(db, id)
	if err != nil {
		slog.Error("alert reload failed", "error", err, "alert_id", id)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	writeSuccess(c, http.StatusOK, item)
}

// alertByID loads a single alert row (with imei via join).
func alertByID(db *sql.DB, id uint64) (*models.AlertItem, error) {
	rows, err := db.Query(`SELECT `+alertSelectCols+`, v.imei`+alertBase+
		` WHERE a.id = ? LIMIT 1`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	return scanAlertJoined(rows)
}
