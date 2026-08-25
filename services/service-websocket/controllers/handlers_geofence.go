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
// Geofence endpoints (FR-6.4 management) — RBAC scoped to accessible vehicles.
// Skema company migration 005: area_type (circle|polygon), coordinates GeoJSON,
// boundary_points untuk polygon, is_active boolean.
// ---------------------------------------------------------------------------

type geofenceModel struct {
	ID             uint64
	Name           string
	AreaType       string
	Coordinates    []byte
	RadiusMeters   sql.NullFloat64
	BoundaryPoints []byte
	CreatedBy      uint64
	IsActive       bool
	CreatedAt      time.Time
}

const geofenceSelectCols = `id, name, area_type, coordinates, radius_meters, boundary_points,
created_by, is_active, created_at`

// toItem converts a DB row to the API response shape (plus vehicle links).
func (g *geofenceModel) toItem(db *sql.DB) models.GeofenceItem {
	item := models.GeofenceItem{
		ID:           g.ID,
		Name:         g.Name,
		AreaType:     g.AreaType,
		Coordinates:  json.RawMessage(g.Coordinates),
		RadiusMeters: nil,
		IsActive:     g.IsActive,
		CreatedBy:    g.CreatedBy,
		CreatedAt:    g.CreatedAt,
	}
	if g.AreaType == "circle" && g.RadiusMeters.Valid {
		item.RadiusMeters = &g.RadiusMeters.Float64
	}
	if len(g.BoundaryPoints) > 0 {
		item.BoundaryPoints = json.RawMessage(g.BoundaryPoints)
	}
	// Geofence_vehicles link (kalau gagal, biarkan kosong — bukan fatal).
	item.Vehicles = []uint64{}
	if db != nil {
		rows, err := db.Query(`SELECT vehicle_id FROM geofence_vehicles WHERE geofence_id = ? AND is_enabled = 1`, g.ID)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var v uint64
				if err := rows.Scan(&v); err == nil {
					item.Vehicles = append(item.Vehicles, v)
				}
			}
		}
	}
	return item
}

// loadGeofenceByID returns a non-deleted geofence row (is_active = 1).
func loadGeofenceByID(db *sql.DB, id uint64) (*geofenceModel, error) {
	g := &geofenceModel{}
	var radius sql.NullFloat64
	err := db.QueryRow(
		`SELECT `+geofenceSelectCols+` FROM geofences WHERE id = ? AND is_active = TRUE`, id,
	).Scan(&g.ID, &g.Name, &g.AreaType, &g.Coordinates, &radius, &g.BoundaryPoints,
		&g.CreatedBy, &g.IsActive, &g.CreatedAt)
	if err != nil {
		return nil, err
	}
	g.RadiusMeters = radius
	return g, nil
}

// geofenceAccessible reports whether the caller may see/touch a geofence:
// Admin → semua; lainnya → hanya geofence yang punya vehicle yang bisa
// diaksesnya (row-level security via geofence_vehicles).
func geofenceAccessible(c *gin.Context, allowed map[uint64]struct{}, g *geofenceModel) bool {
	if isAdmin(c) {
		return true
	}
	if len(allowed) == 0 {
		return false
	}
	db, err := companyRead(c) // B4 HA: access-check = read-only
	if err != nil {
		return false
	}
	var n int
	err = db.QueryRow(`SELECT COUNT(*) FROM geofence_vehicles
WHERE geofence_id = ? AND vehicle_id IN (`+placeholders(len(allowed))+`) AND is_enabled = 1`,
		append([]interface{}{g.ID}, mapKeys(allowed)...)...).Scan(&n)
	if err != nil {
		slog.Error("geofence access check failed", "error", err, "geofence_id", g.ID)
		return false
	}
	return n > 0
}

// geofencesListHandler handles GET /api/v1/geofences.
func geofencesListHandler(c *gin.Context) {
	db, err := companyRead(c) // B4 HA: READ → replica
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	allowed := accessibleVehicleIDsFromCtx(c)

	var rows *sql.Rows
	if isAdmin(c) {
		rows, err = db.Query(`SELECT ` + geofenceSelectCols + ` FROM geofences WHERE is_active = TRUE ORDER BY id DESC`)
	} else {
		q := `SELECT g.` + geofenceSelectCols + ` FROM geofences g
   WHERE g.is_active = TRUE AND EXISTS (
       SELECT 1 FROM geofence_vehicles gv
       WHERE gv.geofence_id = g.id AND gv.is_enabled = 1
       AND gv.vehicle_id IN (` + placeholders(len(allowed)) + `)
   ) ORDER BY g.id DESC`
		rows, err = db.Query(q, mapKeys(allowed)...)
	}
	if err != nil {
		slog.Error("geofences list query failed", "error", err)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	defer rows.Close()

	items := make([]models.GeofenceItem, 0)
	for rows.Next() {
		g := &geofenceModel{}
		var radius sql.NullFloat64
		if err := rows.Scan(&g.ID, &g.Name, &g.AreaType, &g.Coordinates, &radius, &g.BoundaryPoints,
			&g.CreatedBy, &g.IsActive, &g.CreatedAt); err != nil {
			slog.Error("geofence scan failed", "error", err)
			writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
			return
		}
		g.RadiusMeters = radius
		items = append(items, g.toItem(db))
	}
	if err := rows.Err(); err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	writeSuccess(c, http.StatusOK, items)
}

// geofencesCreateHandler handles POST /api/v1/geofences (Admin-only).
func geofencesCreateHandler(c *gin.Context) {
	if !isAdmin(c) {
		writeError(c, http.StatusForbidden, "FORBIDDEN", "only admins can manage geofences")
		return
	}
	u, _ := loadAuthUser(c)
	db, err := companyDB(c)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	var req models.GeofenceCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid JSON body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "geofence name is required")
		return
	}
	req.AreaType = strings.ToLower(strings.TrimSpace(req.AreaType))
	if req.AreaType != "circle" && req.AreaType != "polygon" {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "area_type must be circle or polygon")
		return
	}
	if len(req.Coordinates) == 0 || !json.Valid(req.Coordinates) {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "coordinates must be valid GeoJSON")
		return
	}
	if req.AreaType == "circle" {
		if req.RadiusMeters == nil || *req.RadiusMeters <= 0 {
			writeError(c, http.StatusBadRequest, "INVALID_PARAM", "radius_meters must be positive for circle")
			return
		}
	}

	var boundary interface{}
	if len(req.BoundaryPoints) > 0 && json.Valid(req.BoundaryPoints) {
		boundary = req.BoundaryPoints
	}

	userID := u.CompanyUserID
	if userID == 0 {
		userID = u.ID
	}
	tx, err := db.BeginTx(c.Request.Context(), nil)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.Exec(`INSERT INTO geofences
(name, area_type, coordinates, radius_meters, boundary_points, created_by)
VALUES (?, ?, ?, ?, ?, ?)`,
		req.Name, req.AreaType, req.Coordinates, req.RadiusMeters, boundary, userID)
	if err != nil {
		slog.Error("geofence create failed", "error", err)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	gid, _ := res.LastInsertId()

	// Link ke vehicles (optional).
	for _, vid := range req.VehicleIDs {
		if !canAccessVehicle(c, vid) {
			writeError(c, http.StatusForbidden, "FORBIDDEN",
				"you cannot attach a vehicle you don't have access to")
			return
		}
		if _, err := tx.Exec(`INSERT IGNORE INTO geofence_vehicles (geofence_id, vehicle_id) VALUES (?, ?)`, gid, vid); err != nil {
			slog.Error("geofence vehicle link failed", "error", err)
			writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
			return
		}
	}

	if err := tx.Commit(); err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	writeSuccess(c, http.StatusCreated, gin.H{"id": gid})
}

// geofenceDetailHandler handles GET /api/v1/geofences/:id.
func geofenceDetailHandler(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "geofence id must be a number")
		return
	}
	db, err := companyRead(c) // B4 HA: READ → replica
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	g, err := loadGeofenceByID(db, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(c, http.StatusNotFound, "GEOFENCE_NOT_FOUND", "geofence not found")
			return
		}
		slog.Error("geofence detail query failed", "error", err, "geofence_id", id)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	if !geofenceAccessible(c, accessibleVehicleIDsFromCtx(c), g) {
		writeError(c, http.StatusForbidden, "FORBIDDEN", "you do not have access to this geofence")
		return
	}
	writeSuccess(c, http.StatusOK, g.toItem(db))
}

// geofenceDeleteHandler DELETE /api/v1/geofences/:id (soft: is_active = false).
func geofenceDeleteHandler(c *gin.Context) {
	if !isAdmin(c) {
		writeError(c, http.StatusForbidden, "FORBIDDEN", "only admins can manage geofences")
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "geofence id must be a number")
		return
	}
	db, err := companyDB(c)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	res, err := db.Exec(`UPDATE geofences SET is_active = FALSE WHERE id = ? AND is_active = TRUE`, id)
	if err != nil {
		slog.Error("geofence delete failed", "error", err)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		writeError(c, http.StatusNotFound, "GEOFENCE_NOT_FOUND", "geofence not found")
		return
	}
	writeSuccess(c, http.StatusOK, gin.H{"id": id, "deleted": true})
}
