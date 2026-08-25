package controllers

import (
	"log/slog"
	"net/http"

	"ajb_gps/api-vehicle/models"

	"github.com/gin-gonic/gin"
)

// POST /vehicles (Admin) — create + sinkron master.vehicle_imei_map
// (Key Decision #3) agar ingestion-tcp langsung mengenali IMEI baru.
func vehiclesCreateHandler(c *gin.Context) {
	if !requireRole(c, "Admin") {
		return
	}
	var req models.CreateVehicleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "imei and plate_number are required")
		return
	}
	db, err := companyDB(c)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	u, _ := loadAuthUser(c)

	res, err := db.Exec(
		`INSERT INTO vehicles (imei, plate_number, make, model, variant, year_of_manufacture,
		   color, fuel_type, vehicle_category_code, vehicle_type_code, driver_user_id,
		   device_model, firmware_version, registration_number, notes, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'active')`,
		req.IMEI, req.PlateNumber, nullableStrPtr(req.Make), nullableStrPtr(req.Model),
		nullableStrPtr(req.Variant), req.Year, nullableStrPtr(req.Color),
		nullableStrPtr(req.FuelType), nullableStrPtr(req.CategoryCd),
		nullableStrPtr(req.VehicleTypeCd), nullableUint(req.DriverUserID),
		nullableStrPtr(req.DeviceModel), nullableStrPtr(req.FirmwareVer),
		nullableStrPtr(req.RegNumber), nullableStrPtr(req.Notes),
	)
	if err != nil {
		slog.Error("vehicle create failed", "error", err, "imei", req.IMEI, "company", u.CompanyCode)
		if isDuplicateErr(err) {
			writeError(c, http.StatusConflict, "DUPLICATE", "imei already registered in this company")
			return
		}
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	newID, _ := res.LastInsertId()

	// Kegagalan sync di-log keras: device akan ditolak ingestion bila map
	// tidak ter-update (admin dapat retry).
	if _, err := masterDB().Exec(
		`INSERT INTO vehicle_imei_map (imei, company_code, vehicle_id)
		 VALUES (?, ?, ?)
		 ON DUPLICATE KEY UPDATE company_code = VALUES(company_code), vehicle_id = VALUES(vehicle_id)`,
		req.IMEI, u.CompanyCode, newID,
	); err != nil {
		slog.Error("vehicle_imei_map upsert failed — device will be rejected by ingestion",
			"imei", req.IMEI, "company", u.CompanyCode, "error", err)
	}

	slog.Info("vehicle created", "company", u.CompanyCode, "vehicle_id", newID, "imei", req.IMEI)
	writeSuccess(c, http.StatusCreated, gin.H{"id": newID, "imei": req.IMEI})
}