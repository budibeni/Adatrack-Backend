package controllers

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"ajb_gps/api-vehicle/models"

	"github.com/gin-gonic/gin"
)

// PATCH /routes/:id (Admin/Manager).
func routesUpdateHandler(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	if !requireAdminOrManager(c) {
		return
	}
	var req struct {
		Name                 *string            `json:"name,omitempty"`
		Waypoints            *[]models.Waypoint `json:"waypoints,omitempty"`
		EstimatedDurationSec *int               `json:"estimated_duration_sec,omitempty"`
		IsActive             *bool              `json:"is_active,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	db, err := companyDB(c)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	q := `UPDATE routes SET`
	args := []interface{}{}
	apply := func(col string, v interface{}) {
		if len(args) > 0 {
			q += ","
		}
		q += " " + col + " = ?"
		args = append(args, v)
	}
	if req.Name != nil && *req.Name != "" {
		apply("name", *req.Name)
	}
	if req.Waypoints != nil {
		if len(*req.Waypoints) < 2 {
			writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "at least 2 waypoints required")
			return
		}
		wps, _ := json.Marshal(*req.Waypoints)
		apply("waypoints", wps)
	}
	if req.EstimatedDurationSec != nil {
		apply("estimated_duration_sec", *req.EstimatedDurationSec)
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
		slog.Error("route update failed", "error", err, "route_id", id)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		writeError(c, http.StatusNotFound, "ROUTE_NOT_FOUND", "route not found or no change")
		return
	}
	writeSuccess(c, http.StatusOK, gin.H{"id": id})
}

// DELETE /routes/:id (Admin/Manager) — soft delete via is_active=FALSE.
func routesDeleteHandler(c *gin.Context) {
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
	res, err := db.Exec(`UPDATE routes SET is_active = FALSE WHERE id = ?`, id)
	if err != nil {
		slog.Error("route delete failed", "error", err, "route_id", id)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		writeError(c, http.StatusNotFound, "ROUTE_NOT_FOUND", "route not found")
		return
	}
	writeSuccess(c, http.StatusOK, gin.H{"id": id})
}