package controllers

import (
	"database/sql"
	"log/slog"
	"net/http"

	"ajb_gps/api-vehicle/models"

	"github.com/gin-gonic/gin"
)

// vehicleCols = kolom list/detail vehicles (migration 002).
const vehicleCols = `id, imei, plate_number, make, model, fuel_type,
vehicle_type_code, driver_user_id, device_model, status, created_at`

func scanVehicle(rows *sql.Rows) (models.VehicleItem, error) {
	var (
		it     models.VehicleItem
		makeS  sql.NullString
		modelS sql.NullString
		fuel   sql.NullString
		vtype  sql.NullString
		driver sql.NullInt64
		device sql.NullString
	)
	err := rows.Scan(&it.ID, &it.IMEI, &it.PlateNumber, &makeS, &modelS, &fuel,
		&vtype, &driver, &device, &it.Status, &it.CreatedAt)
	if err != nil {
		return it, err
	}
	it.Make = nullableStrP(makeS)
	it.Model = nullableStrP(modelS)
	it.FuelType = nullableStrP(fuel)
	it.VehicleTypeCd = nullableStrP(vtype)
	it.DriverUserID = nullableUint64(driver)
	it.DeviceModel = nullableStrP(device)
	return it, nil
}

// GET /vehicles — admin lihat semua; role lain hanya user_vehicles (row-level).
func vehiclesListHandler(c *gin.Context) {
	page, limit := paginationParams(c)
	db, err := companyRead(c) // B4 HA: READ → replica
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	cond := " WHERE deleted_at IS NULL"
	args := []interface{}{}
	if !isAdmin(c) {
		allowed := accessibleVehicleIDs(c)
		if len(allowed) == 0 {
			writeSuccess(c, http.StatusOK, []models.VehicleItem{},
				&models.PaginationInfo{Page: page, Limit: limit, Total: 0})
			return
		}
		cond += ` AND id IN (` + placeholders(len(allowed)) + `)`
		args = append(args, mapKeys(allowed)...)
	}
	if s := c.Query("status"); s != "" {
		cond += ` AND status = ?`
		args = append(args, s)
	}

	var total int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM vehicles`+cond, args...).Scan(&total); err != nil {
		slog.Error("vehicles count failed", "error", err)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	rows, err := db.Query(`SELECT `+vehicleCols+` FROM vehicles`+cond+
		` ORDER BY id LIMIT ? OFFSET ?`,
		append(append([]interface{}{}, args...), limit, (page-1)*limit)...)
	if err != nil {
		slog.Error("vehicles list query failed", "error", err)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	defer rows.Close()

	items := make([]models.VehicleItem, 0, limit)
	for rows.Next() {
		it, err := scanVehicle(rows)
		if err != nil {
			slog.Error("vehicle scan failed", "error", err)
			writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
			return
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	writeSuccess(c, http.StatusOK, items,
		&models.PaginationInfo{Page: page, Limit: limit, Total: total})
}

// GET /vehicles/:id
func vehicleDetailHandler(c *gin.Context) {
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
	rows, err := db.Query(`SELECT `+vehicleCols+` FROM vehicles WHERE id = ? AND deleted_at IS NULL`, id)
	if err != nil {
		slog.Error("vehicle detail query failed", "error", err, "vehicle_id", id)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	defer rows.Close()
	if !rows.Next() {
		writeError(c, http.StatusNotFound, "VEHICLE_NOT_FOUND", "vehicle not found")
		return
	}
	item, err := scanVehicle(rows)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	writeSuccess(c, http.StatusOK, item)
}