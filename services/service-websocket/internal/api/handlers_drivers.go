package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"backend/internal/auth"
	"backend/internal/logger"
	"backend/internal/dbclient"
	"backend/internal/tenant"
)

type DriverItem struct {
	ID            int64      `json:"id"`
	Name          string     `json:"name"`
	Phone         *string    `json:"phone,omitempty"`
	Email         *string    `json:"email,omitempty"`
	LicenseNumber *string    `json:"license_number,omitempty"`
	LicenseType   *string    `json:"license_type,omitempty"`
	LicenseExpiry *string    `json:"license_expiry,omitempty"`
	RFIDTag       *string    `json:"rfid_tag,omitempty"`
	GroupID       *int64     `json:"group_id,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	AssignedVehicle *int64   `json:"assigned_vehicle_id,omitempty"`
}

func (h *Handler) ListDrivers(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)

	search := r.URL.Query().Get("search")
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 200 {
		limit = 50
	}
	offset := (page - 1) * limit

	var args []interface{}
	queryWhere := "WHERE deleted_at IS NULL"
	
	if search != "" {
		args = append(args, "%"+search+"%")
		queryWhere += fmt.Sprintf(" AND (name ILIKE $%d OR email ILIKE $%d OR phone ILIKE $%d)", len(args), len(args), len(args))
	}

	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM %s.tm_drivers %s", schema, queryWhere)
	
	args = append(args, limit, offset)
	query := fmt.Sprintf(`
		SELECT id, name, phone, email, license_number, license_type, CAST(license_expiry AS TEXT), rfid_tag, group_id, created_at, updated_at
		FROM %s.tm_drivers
		%s
		ORDER BY id ASC LIMIT $%d OFFSET $%d
	`, schema, queryWhere, len(args)-1, len(args))

	var total int
	router := tenant.NewReadRouter(claims.CompanyCode)
	_ = router.QueryRow(r.Context(), countQuery, args[:len(args)-2]...).Scan(&total)

	rows, err := router.Query(r.Context(), query, args...)
	if err != nil {
		logger.Log.Error("Failed to query drivers", "err", err)
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to retrieve drivers")
		return
	}
	defer rows.Close()

	var drivers []DriverItem
	for rows.Next() {
		var d DriverItem
		if err := rows.Scan(&d.ID, &d.Name, &d.Phone, &d.Email, &d.LicenseNumber, &d.LicenseType, &d.LicenseExpiry, &d.RFIDTag, &d.GroupID, &d.CreatedAt, &d.UpdatedAt); err == nil {
			drivers = append(drivers, d)
		} else {
			logger.Log.Error("Scan error", "err", err)
		}
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
		"data":   drivers,
		"pagination": map[string]interface{}{
			"page":  page,
			"limit": limit,
			"total": total,
		},
	})
}

func (h *Handler) GetDriver(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}

	idStr := chi.URLParam(r, "id")
	driverID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_ID", "Driver ID must be a valid integer")
		return
	}

	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)

	var d DriverItem
	router := tenant.NewReadRouter(claims.CompanyCode)
	
	err = router.QueryRow(r.Context(), fmt.Sprintf(`
		SELECT id, name, phone, email, license_number, license_type, CAST(license_expiry AS TEXT), rfid_tag, group_id, created_at, updated_at
		FROM %s.tm_drivers
		WHERE id = $1 AND deleted_at IS NULL
	`, schema), driverID).Scan(&d.ID, &d.Name, &d.Phone, &d.Email, &d.LicenseNumber, &d.LicenseType, &d.LicenseExpiry, &d.RFIDTag, &d.GroupID, &d.CreatedAt, &d.UpdatedAt)
	
	if err != nil {
		h.writeError(w, http.StatusNotFound, "DRIVER_NOT_FOUND", fmt.Sprintf("Driver with ID %d not found", driverID))
		return
	}
	
	// Get currently assigned vehicle
	_ = router.QueryRow(r.Context(), fmt.Sprintf(`
		SELECT vehicle_id FROM %s.tm_driver_vehicles WHERE driver_id = $1 AND unassigned_at IS NULL ORDER BY assigned_at DESC LIMIT 1
	`, schema), driverID).Scan(&d.AssignedVehicle)

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
		"data":   d,
	})
}

func (h *Handler) CreateDriver(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}

	var req DriverItem
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid JSON body")
		return
	}

	if req.Name == "" {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Name is required")
		return
	}

	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	router := dbclient.Pool

	var newID int64
	err := router.QueryRow(r.Context(), fmt.Sprintf(`
		INSERT INTO %s.tm_drivers (name, phone, email, license_number, license_type, license_expiry, rfid_tag, group_id)
		VALUES ($1, $2, $3, $4, $5, CAST($6 AS DATE), $7, $8) RETURNING id
	`, schema), req.Name, req.Phone, req.Email, req.LicenseNumber, req.LicenseType, req.LicenseExpiry, req.RFIDTag, req.GroupID).Scan(&newID)
	
	if err != nil {
		logger.Log.Error("Failed to create driver", "err", err)
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to create driver")
		return
	}
	
	req.ID = newID
	h.auditLog(r.Context(), claims.CompanyCode, "CREATE_DRIVER", "success", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Created driver %d", newID))

	h.writeJSON(w, http.StatusCreated, map[string]interface{}{
		"status": "success",
		"data":   req,
	})
}

func (h *Handler) UpdateDriver(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}

	idStr := chi.URLParam(r, "id")
	driverID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_ID", "Driver ID must be a valid integer")
		return
	}

	var req DriverItem
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid JSON body")
		return
	}

	if req.Name == "" {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Name is required")
		return
	}

	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	router := dbclient.Pool

	res, err := router.Exec(r.Context(), fmt.Sprintf(`
		UPDATE %s.tm_drivers SET name = $1, phone = $2, email = $3, license_number = $4, license_type = $5, license_expiry = CAST($6 AS DATE), rfid_tag = $7, group_id = $8, updated_at = CURRENT_TIMESTAMP
		WHERE id = $9 AND deleted_at IS NULL
	`, schema), req.Name, req.Phone, req.Email, req.LicenseNumber, req.LicenseType, req.LicenseExpiry, req.RFIDTag, req.GroupID, driverID)
	
	if err != nil {
		logger.Log.Error("Failed to update driver", "err", err)
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to update driver")
		return
	}

	if res.RowsAffected() == 0 {
		h.writeError(w, http.StatusNotFound, "DRIVER_NOT_FOUND", "Driver not found")
		return
	}

	req.ID = driverID
	h.auditLog(r.Context(), claims.CompanyCode, "UPDATE_DRIVER", "success", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Updated driver %d", driverID))

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
		"data":   req,
	})
}

func (h *Handler) DeleteDriver(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}

	idStr := chi.URLParam(r, "id")
	driverID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_ID", "Driver ID must be a valid integer")
		return
	}

	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	router := dbclient.Pool

	res, err := router.Exec(r.Context(), fmt.Sprintf(`
		UPDATE %s.tm_drivers SET deleted_at = CURRENT_TIMESTAMP WHERE id = $1 AND deleted_at IS NULL
	`, schema), driverID)
	
	if err != nil {
		logger.Log.Error("Failed to delete driver", "err", err)
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to delete driver")
		return
	}

	if res.RowsAffected() == 0 {
		h.writeError(w, http.StatusNotFound, "DRIVER_NOT_FOUND", "Driver not found")
		return
	}

	h.auditLog(r.Context(), claims.CompanyCode, "DELETE_DRIVER", "success", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Deleted driver %d", driverID))

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
		"message": "Driver deleted successfully",
	})
}

func (h *Handler) AssignDriverVehicle(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}

	idStr := chi.URLParam(r, "id")
	driverID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_ID", "Driver ID must be a valid integer")
		return
	}

	var req struct {
		VehicleID *int64 `json:"vehicle_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid JSON body")
		return
	}

	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	router := dbclient.Pool

	// First, unassign current vehicle if any
	_, err = router.Exec(r.Context(), fmt.Sprintf(`
		UPDATE %s.tm_driver_vehicles SET unassigned_at = CURRENT_TIMESTAMP WHERE driver_id = $1 AND unassigned_at IS NULL
	`, schema), driverID)
	
	if err != nil {
		logger.Log.Error("Failed to unassign driver vehicle", "err", err)
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to unassign driver")
		return
	}
	
	if req.VehicleID != nil {
		// Assign new vehicle
		_, err = router.Exec(r.Context(), fmt.Sprintf(`
			INSERT INTO %s.tm_driver_vehicles (driver_id, vehicle_id) VALUES ($1, $2)
		`, schema), driverID, *req.VehicleID)
		
		if err != nil {
			logger.Log.Error("Failed to assign driver vehicle", "err", err)
			h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to assign driver")
			return
		}
		
		h.auditLog(r.Context(), claims.CompanyCode, "ASSIGN_DRIVER", "success", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Assigned driver %d to vehicle %d", driverID, *req.VehicleID))
	} else {
		h.auditLog(r.Context(), claims.CompanyCode, "UNASSIGN_DRIVER", "success", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Unassigned driver %d", driverID))
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
		"message": "Driver assignment updated",
	})
}
