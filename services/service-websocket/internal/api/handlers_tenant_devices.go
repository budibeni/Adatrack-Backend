package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"backend/internal/auth"
	"backend/internal/dbclient"
)

type TenantDevice struct {
	IMEI            string     `json:"imei"`
	DeviceBrand     string     `json:"device_brand"`
	DeviceModel     string     `json:"device_model"`
	SimNumber       string     `json:"sim_number"`
	Protocol        string     `json:"protocol"`
	AssignedCompany string     `json:"assigned_company"`
	Status          string     `json:"status"`
	Iccid           *string    `json:"iccid"`
	VehicleID       *int       `json:"vehicle_id"`
	VehicleName     *string    `json:"vehicle_name"`
	PlateNumber     *string    `json:"plate_number"`
	DriverID        *int64     `json:"driver_id"`
	DriverName      *string    `json:"driver_name"`
	DriverPhone     *string    `json:"driver_phone"`
	LastSeenAt      *time.Time `json:"last_seen_at"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type CreateTenantDeviceRequest struct {
	IMEI        string  `json:"imei"`
	DeviceBrand string  `json:"device_brand"`
	DeviceModel string  `json:"device_model"`
	SimNumber   string  `json:"sim_number"`
	Protocol    string  `json:"protocol"`
	Iccid       *string `json:"iccid"`
	VehicleID   *int    `json:"vehicle_id"`
	DriverID    *int64  `json:"driver_id"`
}

type UpdateTenantDeviceRequest struct {
	DeviceBrand string  `json:"device_brand"`
	DeviceModel string  `json:"device_model"`
	SimNumber   string  `json:"sim_number"`
	Protocol    string  `json:"protocol"`
	Iccid       *string `json:"iccid"`
	VehicleID   *int    `json:"vehicle_id"`
	DriverID    *int64  `json:"driver_id"`
	Status      string  `json:"status"`
}

// ListTenantDevices returns all GPS / smartphone devices belonging to the logged in tenant company
func (h *Handler) ListTenantDevices(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	schema := fmt.Sprintf("adatrack_gps_%s", strings.ToLower(claims.CompanyCode))
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	statusFilter := strings.TrimSpace(r.URL.Query().Get("status"))
	protocolFilter := strings.TrimSpace(r.URL.Query().Get("protocol"))

	query := fmt.Sprintf(`
		SELECT 
			d.imei,
			COALESCE(d.device_brand, ''),
			COALESCE(d.device_model, ''),
			COALESCE(d.sim_number, ''),
			COALESCE(d.protocol, 'SMARTPHONE'),
			COALESCE(d.assigned_company, ''),
			COALESCE(d.status, 'idle'),
			d.iccid,
			COALESCE(v_phys.id, v_drv.id),
			COALESCE(v_phys.vehicle_name, v_drv.vehicle_name),
			COALESCE(v_phys.plate_number, v_drv.plate_number),
			COALESCE(v_phys.last_seen_at, v_drv.last_seen_at),
			d.created_at,
			d.updated_at,
			dr.id,
			dr.name,
			dr.phone
		FROM adatrack_gps_master.tm_gps_devices d
		LEFT JOIN %[1]s.tm_drivers dr ON d.imei = dr.device_imei AND dr.deleted_at IS NULL
		LEFT JOIN LATERAL (
			SELECT v.id, v.vehicle_name, v.plate_number, v.last_seen_at
			FROM %[1]s.tm_driver_vehicles dv
			JOIN %[1]s.tm_vehicles v ON dv.vehicle_id = v.id AND v.deleted_at IS NULL
			WHERE dv.driver_id = dr.id AND dv.unassigned_at IS NULL
			ORDER BY dv.assigned_at DESC LIMIT 1
		) v_drv ON true
		LEFT JOIN %[1]s.tm_vehicles v_phys ON d.imei = v_phys.imei AND v_phys.deleted_at IS NULL
		WHERE UPPER(d.assigned_company) = UPPER($1)
	`, schema)

	args := []interface{}{claims.CompanyCode}
	argIdx := 2

	if search != "" {
		query += fmt.Sprintf(` AND (
			d.imei ILIKE $%d OR 
			d.device_brand ILIKE $%d OR 
			d.device_model ILIKE $%d OR 
			d.sim_number ILIKE $%d OR 
			v.plate_number ILIKE $%d OR 
			v.vehicle_name ILIKE $%d OR
			dr.name ILIKE $%d OR
			dr.phone ILIKE $%d
		)`, argIdx, argIdx, argIdx, argIdx, argIdx, argIdx, argIdx, argIdx)
		args = append(args, "%"+search+"%")
		argIdx++
	}

	if statusFilter != "" && statusFilter != "all" {
		if statusFilter == "assigned" {
			query += ` AND (dr.id IS NOT NULL OR v.id IS NOT NULL)`
		} else if statusFilter == "idle" {
			query += ` AND dr.id IS NULL AND v.id IS NULL`
		} else {
			query += fmt.Sprintf(` AND d.status = $%d`, argIdx)
			args = append(args, statusFilter)
			argIdx++
		}
	}

	if protocolFilter != "" && protocolFilter != "all" {
		query += fmt.Sprintf(` AND UPPER(d.protocol) = UPPER($%d)`, argIdx)
		args = append(args, protocolFilter)
		argIdx++
	}

	query += ` ORDER BY d.created_at DESC`

	rows, err := dbclient.Pool.Query(ctx, query, args...)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to query devices: "+err.Error())
		return
	}
	defer rows.Close()

	devices := make([]TenantDevice, 0)
	for rows.Next() {
		var d TenantDevice
		if err := rows.Scan(
			&d.IMEI,
			&d.DeviceBrand,
			&d.DeviceModel,
			&d.SimNumber,
			&d.Protocol,
			&d.AssignedCompany,
			&d.Status,
			&d.Iccid,
			&d.VehicleID,
			&d.VehicleName,
			&d.PlateNumber,
			&d.LastSeenAt,
			&d.CreatedAt,
			&d.UpdatedAt,
			&d.DriverID,
			&d.DriverName,
			&d.DriverPhone,
		); err != nil {
			continue
		}
		if d.DriverID != nil || d.VehicleID != nil {
			d.Status = "assigned"
		}
		devices = append(devices, d)
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
		"data":   devices,
	})
}

// GetTenantDevice returns a single device
func (h *Handler) GetTenantDevice(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}

	imei := chi.URLParam(r, "imei")
	if imei == "" {
		h.writeError(w, http.StatusBadRequest, "INVALID_PARAM", "Device IMEI is required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	schema := fmt.Sprintf("adatrack_gps_%s", strings.ToLower(claims.CompanyCode))
	query := fmt.Sprintf(`
		SELECT 
			d.imei,
			COALESCE(d.device_brand, ''),
			COALESCE(d.device_model, ''),
			COALESCE(d.sim_number, ''),
			COALESCE(d.protocol, 'SMARTPHONE'),
			COALESCE(d.assigned_company, ''),
			COALESCE(d.status, 'idle'),
			d.iccid,
			COALESCE(v_phys.id, v_drv.id),
			COALESCE(v_phys.vehicle_name, v_drv.vehicle_name),
			COALESCE(v_phys.plate_number, v_drv.plate_number),
			COALESCE(v_phys.last_seen_at, v_drv.last_seen_at),
			d.created_at,
			d.updated_at,
			dr.id,
			dr.name,
			dr.phone
		FROM adatrack_gps_master.tm_gps_devices d
		LEFT JOIN %[1]s.tm_drivers dr ON d.imei = dr.device_imei AND dr.deleted_at IS NULL
		LEFT JOIN LATERAL (
			SELECT v.id, v.vehicle_name, v.plate_number, v.last_seen_at
			FROM %[1]s.tm_driver_vehicles dv
			JOIN %[1]s.tm_vehicles v ON dv.vehicle_id = v.id AND v.deleted_at IS NULL
			WHERE dv.driver_id = dr.id AND dv.unassigned_at IS NULL
			ORDER BY dv.assigned_at DESC LIMIT 1
		) v_drv ON true
		LEFT JOIN %[1]s.tm_vehicles v_phys ON d.imei = v_phys.imei AND v_phys.deleted_at IS NULL
		WHERE d.imei = $1 AND UPPER(d.assigned_company) = UPPER($2)
	`, schema)

	var d TenantDevice
	err := dbclient.Pool.QueryRow(ctx, query, imei, claims.CompanyCode).Scan(
		&d.IMEI,
		&d.DeviceBrand,
		&d.DeviceModel,
		&d.SimNumber,
		&d.Protocol,
		&d.AssignedCompany,
		&d.Status,
		&d.Iccid,
		&d.VehicleID,
		&d.VehicleName,
		&d.PlateNumber,
		&d.LastSeenAt,
		&d.CreatedAt,
		&d.UpdatedAt,
		&d.DriverID,
		&d.DriverName,
		&d.DriverPhone,
	)
	if err != nil {
		h.writeError(w, http.StatusNotFound, "NOT_FOUND", "Device not found")
		return
	}

	if d.DriverID != nil || d.VehicleID != nil {
		d.Status = "assigned"
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
		"data":   d,
	})
}

// CreateTenantDevice registers a new GPS or smartphone device under the current tenant company
func (h *Handler) CreateTenantDevice(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}

	var req CreateTenantDeviceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_PAYLOAD", "Malformed request body: "+err.Error())
		return
	}

	req.IMEI = strings.TrimSpace(req.IMEI)
	if req.IMEI == "" {
		h.writeError(w, http.StatusBadRequest, "MISSING_FIELD", "ID Perangkat / IMEI wajib diisi")
		return
	}

	if req.Protocol == "" {
		req.Protocol = "SMARTPHONE"
	}
	protoUpper := strings.ToUpper(strings.TrimSpace(req.Protocol))
	if protoUpper != "SMARTPHONE" && protoUpper != "HTTP_MOBILE" {
		h.writeError(w, http.StatusBadRequest, "INVALID_PROTOCOL", "Hanya perangkat dengan protokol smartphone (SMARTPHONE / HTTP_MOBILE) yang dapat didaftarkan pada portal ini. Pendaftaran GPS fisik dilakukan melalui Admin Portal.")
		return
	}

	if req.DeviceBrand == "" {
		req.DeviceBrand = "Smartphone Android/iOS"
	}
	if req.DeviceModel == "" {
		req.DeviceModel = "Mobile Tracker App"
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	// Check if already registered
	var existingCompany *string
	err := dbclient.Pool.QueryRow(ctx,
		"SELECT assigned_company FROM adatrack_gps_master.tm_gps_devices WHERE imei = $1",
		req.IMEI,
	).Scan(&existingCompany)

	if err == nil {
		h.writeError(w, http.StatusConflict, "DUPLICATE_IMEI", "Perangkat dengan ID/IMEI ini sudah terdaftar di sistem")
		return
	}

	initialStatus := "idle"
	if (req.VehicleID != nil && *req.VehicleID > 0) || (req.DriverID != nil && *req.DriverID > 0) {
		initialStatus = "assigned"
	}

	// Insert into master devices
	_, err = dbclient.Pool.Exec(ctx, `
		INSERT INTO adatrack_gps_master.tm_gps_devices (
			imei, device_brand, device_model, sim_number, protocol, iccid, assigned_company, status, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, req.IMEI, req.DeviceBrand, req.DeviceModel, req.SimNumber, req.Protocol, req.Iccid, claims.CompanyCode, initialStatus)

	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Gagal mendaftarkan perangkat: "+err.Error())
		return
	}

	schema := fmt.Sprintf("adatrack_gps_%s", strings.ToLower(claims.CompanyCode))

	// If driver assignment requested, link to driver
	if req.DriverID != nil && *req.DriverID > 0 {
		_, _ = dbclient.Pool.Exec(ctx, fmt.Sprintf(
			"UPDATE %s.tm_drivers SET device_imei = NULL WHERE device_imei = $1", schema),
			req.IMEI,
		)
		_, _ = dbclient.Pool.Exec(ctx, fmt.Sprintf(
			"UPDATE %s.tm_drivers SET device_imei = $1, updated_at = CURRENT_TIMESTAMP WHERE id = $2 AND deleted_at IS NULL", schema),
			req.IMEI, *req.DriverID,
		)
	}

	// If vehicle assignment requested, link to vehicle and vehicle_imei_map
	if req.VehicleID != nil && *req.VehicleID > 0 {
		// Update vehicle imei
		_, errVeh := dbclient.Pool.Exec(ctx, fmt.Sprintf(
			"UPDATE %s.tm_vehicles SET imei = $1 WHERE id = $2", schema),
			req.IMEI, *req.VehicleID,
		)
		if errVeh != nil {
			// Non-fatal, just log
		}

		// Update tm_vehicle_imei_map
		_, _ = dbclient.Pool.Exec(ctx, `
			INSERT INTO adatrack_gps_master.tm_vehicle_imei_map (imei, company_code, vehicle_id, created_at)
			VALUES ($1, $2, $3, CURRENT_TIMESTAMP)
			ON CONFLICT (imei) DO UPDATE SET company_code = EXCLUDED.company_code, vehicle_id = EXCLUDED.vehicle_id
		`, req.IMEI, claims.CompanyCode, *req.VehicleID)
	}

	h.writeJSON(w, http.StatusCreated, map[string]interface{}{
		"status":  "success",
		"message": "Perangkat berhasil didaftarkan",
		"data": map[string]interface{}{
			"imei":             req.IMEI,
			"protocol":         req.Protocol,
			"device_brand":     req.DeviceBrand,
			"device_model":     req.DeviceModel,
			"assigned_company": claims.CompanyCode,
			"status":           initialStatus,
		},
	})
}

// UpdateTenantDevice updates device details or its vehicle assignment
func (h *Handler) UpdateTenantDevice(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}

	imei := chi.URLParam(r, "imei")
	if imei == "" {
		h.writeError(w, http.StatusBadRequest, "INVALID_PARAM", "Device IMEI is required")
		return
	}

	var req UpdateTenantDeviceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_PAYLOAD", "Malformed request body: "+err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	// Verify device belongs to this tenant and check protocol
	var currentAssigned *string
	var currentProtocol string
	err := dbclient.Pool.QueryRow(ctx,
		"SELECT assigned_company, protocol FROM adatrack_gps_master.tm_gps_devices WHERE imei = $1",
		imei,
	).Scan(&currentAssigned, &currentProtocol)

	if err != nil || currentAssigned == nil || !strings.EqualFold(*currentAssigned, claims.CompanyCode) {
		h.writeError(w, http.StatusNotFound, "NOT_FOUND", "Perangkat tidak ditemukan di perusahaan Anda")
		return
	}

	protoUpper := strings.ToUpper(strings.TrimSpace(currentProtocol))
	if protoUpper != "SMARTPHONE" && protoUpper != "HTTP_MOBILE" {
		h.writeError(w, http.StatusForbidden, "READ_ONLY_DEVICE", "Perangkat GPS fisik tidak dapat diedit melalui portal ini. Perangkat GPS fisik hanya dapat dilihat (read-only) dan dikelola melalui Admin Portal.")
		return
	}

	if req.Protocol == "" {
		req.Protocol = "SMARTPHONE"
	} else {
		newProtoUpper := strings.ToUpper(strings.TrimSpace(req.Protocol))
		if newProtoUpper != "SMARTPHONE" && newProtoUpper != "HTTP_MOBILE" {
			h.writeError(w, http.StatusBadRequest, "INVALID_PROTOCOL", "Protokol perangkat hanya dapat berupa protokol smartphone (SMARTPHONE / HTTP_MOBILE)")
			return
		}
	}

	status := req.Status
	if status == "" {
		status = "idle"
		if (req.VehicleID != nil && *req.VehicleID > 0) || (req.DriverID != nil && *req.DriverID > 0) {
			status = "assigned"
		}
	}

	_, err = dbclient.Pool.Exec(ctx, `
		UPDATE adatrack_gps_master.tm_gps_devices
		SET device_brand = $1, device_model = $2, sim_number = $3, protocol = $4, iccid = $5, status = $6, updated_at = CURRENT_TIMESTAMP
		WHERE imei = $7
	`, req.DeviceBrand, req.DeviceModel, req.SimNumber, req.Protocol, req.Iccid, status, imei)

	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Gagal memperbarui perangkat: "+err.Error())
		return
	}

	schema := fmt.Sprintf("adatrack_gps_%s", strings.ToLower(claims.CompanyCode))

	// Handle driver assignment changes
	if req.DriverID != nil && *req.DriverID > 0 {
		// Clear previous driver with this IMEI
		_, _ = dbclient.Pool.Exec(ctx, fmt.Sprintf(
			"UPDATE %s.tm_drivers SET device_imei = NULL WHERE device_imei = $1 AND id != $2", schema),
			imei, *req.DriverID,
		)
		// Set to new driver
		_, _ = dbclient.Pool.Exec(ctx, fmt.Sprintf(
			"UPDATE %s.tm_drivers SET device_imei = $1, updated_at = CURRENT_TIMESTAMP WHERE id = $2 AND deleted_at IS NULL", schema),
			imei, *req.DriverID,
		)
		_, _ = dbclient.Pool.Exec(ctx,
			"UPDATE adatrack_gps_master.tm_gps_devices SET status = 'assigned' WHERE imei = $1",
			imei,
		)
	} else if req.DriverID != nil && *req.DriverID == 0 {
		// Unassigned from driver
		_, _ = dbclient.Pool.Exec(ctx, fmt.Sprintf(
			"UPDATE %s.tm_drivers SET device_imei = NULL WHERE device_imei = $1", schema),
			imei,
		)
		if req.VehicleID == nil || *req.VehicleID == 0 {
			_, _ = dbclient.Pool.Exec(ctx,
				"UPDATE adatrack_gps_master.tm_gps_devices SET status = 'idle' WHERE imei = $1",
				imei,
			)
		}
	}

	// Handle vehicle assignment changes
	if req.VehicleID != nil && *req.VehicleID > 0 {
		// Clear imei from previous vehicle if any
		_, _ = dbclient.Pool.Exec(ctx, fmt.Sprintf(
			"UPDATE %s.tm_vehicles SET imei = '' WHERE imei = $1 AND id != $2", schema),
			imei, *req.VehicleID,
		)
		// Set to new vehicle
		_, _ = dbclient.Pool.Exec(ctx, fmt.Sprintf(
			"UPDATE %s.tm_vehicles SET imei = $1 WHERE id = $2", schema),
			imei, *req.VehicleID,
		)
		// Update map
		_, _ = dbclient.Pool.Exec(ctx, `
			INSERT INTO adatrack_gps_master.tm_vehicle_imei_map (imei, company_code, vehicle_id, created_at)
			VALUES ($1, $2, $3, CURRENT_TIMESTAMP)
			ON CONFLICT (imei) DO UPDATE SET company_code = EXCLUDED.company_code, vehicle_id = EXCLUDED.vehicle_id
		`, imei, claims.CompanyCode, *req.VehicleID)
	} else if req.VehicleID != nil && *req.VehicleID == 0 {
		// Unassigned from vehicle
		_, _ = dbclient.Pool.Exec(ctx, fmt.Sprintf(
			"UPDATE %s.tm_vehicles SET imei = '' WHERE imei = $1", schema),
			imei,
		)
		_, _ = dbclient.Pool.Exec(ctx,
			"DELETE FROM adatrack_gps_master.tm_vehicle_imei_map WHERE imei = $1",
			imei,
		)
		if req.DriverID == nil || *req.DriverID == 0 {
			_, _ = dbclient.Pool.Exec(ctx,
				"UPDATE adatrack_gps_master.tm_gps_devices SET status = 'idle' WHERE imei = $1",
				imei,
			)
		}
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "success",
		"message": "Perangkat berhasil diperbarui",
	})
}

// DeleteTenantDevice unregisters a device from the tenant company
func (h *Handler) DeleteTenantDevice(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}

	imei := chi.URLParam(r, "imei")
	if imei == "" {
		h.writeError(w, http.StatusBadRequest, "INVALID_PARAM", "Device IMEI is required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	// Verify device belongs to this tenant and check protocol
	var currentAssigned *string
	var currentProtocol string
	err := dbclient.Pool.QueryRow(ctx,
		"SELECT assigned_company, protocol FROM adatrack_gps_master.tm_gps_devices WHERE imei = $1",
		imei,
	).Scan(&currentAssigned, &currentProtocol)

	if err != nil || currentAssigned == nil || !strings.EqualFold(*currentAssigned, claims.CompanyCode) {
		h.writeError(w, http.StatusNotFound, "NOT_FOUND", "Perangkat tidak ditemukan di perusahaan Anda")
		return
	}

	protoUpper := strings.ToUpper(strings.TrimSpace(currentProtocol))
	if protoUpper != "SMARTPHONE" && protoUpper != "HTTP_MOBILE" {
		h.writeError(w, http.StatusForbidden, "READ_ONLY_DEVICE", "Perangkat GPS fisik tidak dapat dihapus melalui portal ini. Pengelolaan hanya dapat dilakukan melalui Admin Portal.")
		return
	}

	schema := fmt.Sprintf("adatrack_gps_%s", strings.ToLower(claims.CompanyCode))

	// 1. Unlink driver
	_, _ = dbclient.Pool.Exec(ctx, fmt.Sprintf(
		"UPDATE %s.tm_drivers SET device_imei = NULL WHERE device_imei = $1", schema),
		imei,
	)

	// 2. Unlink vehicle
	_, _ = dbclient.Pool.Exec(ctx, fmt.Sprintf(
		"UPDATE %s.tm_vehicles SET imei = '' WHERE imei = $1", schema),
		imei,
	)

	// 3. Remove from vehicle_imei_map
	_, _ = dbclient.Pool.Exec(ctx,
		"DELETE FROM adatrack_gps_master.tm_vehicle_imei_map WHERE imei = $1",
		imei,
	)

	// 4. Delete from tm_gps_devices
	_, errDel := dbclient.Pool.Exec(ctx,
		"DELETE FROM adatrack_gps_master.tm_gps_devices WHERE imei = $1",
		imei,
	)
	if errDel != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Gagal menghapus perangkat: "+errDel.Error())
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "success",
		"message": "Perangkat berhasil dihapus",
	})
}
