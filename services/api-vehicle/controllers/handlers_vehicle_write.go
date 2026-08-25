package controllers

import (
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
)

// PATCH /vehicles/:id (Admin) — partial update.
func vehiclesUpdateHandler(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	if !isAdmin(c) {
		recordRBACDenial(c, "vehicle_update", "not_admin")
		writeError(c, http.StatusForbidden, "FORBIDDEN", "admin role required")
		return
	}
	var req struct {
		PlateNumber  *string `json:"plate_number,omitempty"`
		Make         *string `json:"make,omitempty"`
		Model        *string `json:"model,omitempty"`
		Color        *string `json:"color,omitempty"`
		FuelType     *string `json:"fuel_type,omitempty"`
		DeviceModel  *string `json:"device_model,omitempty"`
		FirmwareVer  *string `json:"firmware_version,omitempty"`
		RegNumber    *string `json:"registration_number,omitempty"`
		Notes        *string `json:"notes,omitempty"`
		Status       *string `json:"status,omitempty"` // active|inactive|maintenance
		DriverUserID *uint64 `json:"driver_user_id,omitempty"`
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

	q := `UPDATE vehicles SET`
	args := []interface{}{}
	apply := func(col string, v interface{}) {
		if len(args) > 0 {
			q += ","
		}
		q += " " + col + " = ?"
		args = append(args, v)
	}
	if req.PlateNumber != nil {
		apply("plate_number", *req.PlateNumber)
	}
	if req.Make != nil {
		apply("make", *req.Make)
	}
	if req.Model != nil {
		apply("model", *req.Model)
	}
	if req.Color != nil {
		apply("color", *req.Color)
	}
	if req.FuelType != nil {
		apply("fuel_type", *req.FuelType)
	}
	if req.DeviceModel != nil {
		apply("device_model", *req.DeviceModel)
	}
	if req.FirmwareVer != nil {
		apply("firmware_version", *req.FirmwareVer)
	}
	if req.RegNumber != nil {
		apply("registration_number", *req.RegNumber)
	}
	if req.Notes != nil {
		apply("notes", *req.Notes)
	}
	if req.Status != nil {
		switch *req.Status {
		case "active", "inactive", "maintenance":
			apply("status", *req.Status)
		default:
			writeError(c, http.StatusBadRequest, "INVALID_STATUS", "status must be active|inactive|maintenance")
			return
		}
	}
	if req.DriverUserID != nil {
		apply("driver_user_id", *req.DriverUserID)
	}
	if len(args) == 0 {
		writeError(c, http.StatusBadRequest, "EMPTY_UPDATE", "no fields to update")
		return
	}
	q += ` WHERE id = ? AND deleted_at IS NULL`
	args = append(args, id)

	res, err := db.Exec(q, args...)
	if err != nil {
		slog.Error("vehicle update failed", "error", err, "vehicle_id", id)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		writeError(c, http.StatusNotFound, "VEHICLE_NOT_FOUND", "vehicle not found or no change")
		return
	}
	writeSuccess(c, http.StatusOK, gin.H{"id": id})
}

// DELETE /vehicles/:id (Admin) — soft delete + hapus mapping master agar
// device tidak lagi diterima ingestion.
func vehiclesDeleteHandler(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	if !isAdmin(c) {
		recordRBACDenial(c, "vehicle_delete", "not_admin")
		writeError(c, http.StatusForbidden, "FORBIDDEN", "admin role required")
		return
	}
	db, err := companyDB(c)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	u, _ := loadAuthUser(c)

	var imei string
	err = db.QueryRow(`SELECT imei FROM vehicles WHERE id = ? AND deleted_at IS NULL`, id).Scan(&imei)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(c, http.StatusNotFound, "VEHICLE_NOT_FOUND", "vehicle not found")
			return
		}
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	if _, err := db.Exec(`UPDATE vehicles SET deleted_at = NOW(), status='inactive' WHERE id = ?`, id); err != nil {
		slog.Error("vehicle soft delete failed", "error", err, "vehicle_id", id)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	if imei != "" {
		if _, err := masterDB().Exec(`DELETE FROM vehicle_imei_map WHERE imei = ?`, imei); err != nil {
			slog.Error("vehicle_imei_map delete failed", "imei", imei, "company", u.CompanyCode, "error", err)
		}
	}
	slog.Info("vehicle deleted", "company", u.CompanyCode, "vehicle_id", id, "imei", imei)
	writeSuccess(c, http.StatusOK, gin.H{"id": id})
}