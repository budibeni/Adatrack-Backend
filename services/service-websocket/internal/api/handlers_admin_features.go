package api

import (
	"encoding/json"
	"net/http"

	"backend/internal/dbclient"
)

// -------------------------------------------------------------------------
// Global Devices (Read-only view from adatrack_gps_master.tm_vehicle_imei_map)
// -------------------------------------------------------------------------

func (h *Handler) GetGlobalDevices(w http.ResponseWriter, r *http.Request) {
	query := `
		SELECT m.imei, m.company_code, m.vehicle_id, c.name, m.updated_at
		FROM adatrack_gps_master.tm_vehicle_imei_map m
		LEFT JOIN adatrack_gps_master.tm_companies c ON m.company_code = c.code
		ORDER BY m.updated_at DESC
	`
	rows, err := dbclient.Pool.Query(r.Context(), query)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to query global devices")
		return
	}
	defer rows.Close()

	var devices []map[string]interface{}
	for rows.Next() {
		var imei, companyCode, companyName string
		var vehicleID int
		var updatedAt string
		if err := rows.Scan(&imei, &companyCode, &vehicleID, &companyName, &updatedAt); err != nil {
			continue
		}
		devices = append(devices, map[string]interface{}{
			"imei":         imei,
			"company_code": companyCode,
			"company_name": companyName,
			"vehicle_id":   vehicleID,
			"updated_at":   updatedAt,
		})
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "data": devices})
}

// -------------------------------------------------------------------------
// SIM Cards (CRUD in adatrack_gps_master.tm_sim_cards)
// -------------------------------------------------------------------------

func (h *Handler) GetSimCards(w http.ResponseWriter, r *http.Request) {
	rows, err := dbclient.Pool.Query(r.Context(), `SELECT id, iccid, phone_number, provider, status, expiry_date FROM adatrack_gps_master.tm_sim_cards ORDER BY id DESC`)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to query sim cards")
		return
	}
	defer rows.Close()

	var sims []map[string]interface{}
	for rows.Next() {
		var id int
		var iccid, phone, provider, status string
		var expiry *string
		if err := rows.Scan(&id, &iccid, &phone, &provider, &status, &expiry); err != nil {
			continue
		}
		sims = append(sims, map[string]interface{}{
			"id": id, "iccid": iccid, "phone_number": phone, "provider": provider, "status": status, "expiry_date": expiry,
		})
	}
	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "data": sims})
}

func (h *Handler) CreateSimCard(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ICCID       string `json:"iccid"`
		PhoneNumber string `json:"phone_number"`
		Provider    string `json:"provider"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid JSON body")
		return
	}

	var id int
	err := dbclient.Pool.QueryRow(r.Context(), `
		INSERT INTO adatrack_gps_master.tm_sim_cards (iccid, phone_number, provider) VALUES ($1, $2, $3) RETURNING id
	`, req.ICCID, req.PhoneNumber, req.Provider).Scan(&id)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to create SIM Card")
		return
	}
	h.writeJSON(w, http.StatusCreated, map[string]interface{}{"status": "success", "data": map[string]interface{}{"id": id}})
}

// -------------------------------------------------------------------------
// Broadcasts (CRUD in adatrack_gps_master.tm_broadcasts)
// -------------------------------------------------------------------------

func (h *Handler) GetBroadcasts(w http.ResponseWriter, r *http.Request) {
	rows, err := dbclient.Pool.Query(r.Context(), `SELECT id, title, message, target_audience, status, created_at FROM adatrack_gps_master.tm_broadcasts ORDER BY id DESC`)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to query broadcasts")
		return
	}
	defer rows.Close()

	var broadcasts []map[string]interface{}
	for rows.Next() {
		var id int
		var title, message, target, status, createdAt string
		if err := rows.Scan(&id, &title, &message, &target, &status, &createdAt); err != nil {
			continue
		}
		broadcasts = append(broadcasts, map[string]interface{}{
			"id": id, "title": title, "message": message, "target": target, "status": status, "created_at": createdAt,
		})
	}
	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "data": broadcasts})
}

func (h *Handler) CreateBroadcast(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title   string `json:"title"`
		Message string `json:"message"`
		Target  string `json:"target"`
		Status  string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid JSON")
		return
	}
	if req.Status == "" {
		req.Status = "sent"
	}
	if req.Target == "" {
		req.Target = "all"
	}

	var id int
	err := dbclient.Pool.QueryRow(r.Context(), `
		INSERT INTO adatrack_gps_master.tm_broadcasts (title, message, target_audience, status) VALUES ($1, $2, $3, $4) RETURNING id
	`, req.Title, req.Message, req.Target, req.Status).Scan(&id)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to create broadcast")
		return
	}
	h.writeJSON(w, http.StatusCreated, map[string]interface{}{"status": "success", "data": map[string]interface{}{"id": id}})
}

// -------------------------------------------------------------------------
// Global Audit Logs (Read from adatrack_gps_master.tm_global_audit_logs)
// -------------------------------------------------------------------------

func (h *Handler) GetGlobalAuditLogs(w http.ResponseWriter, r *http.Request) {
	rows, err := dbclient.Pool.Query(r.Context(), `SELECT id, company_code, actor_email, actor_role, action, detail, created_at FROM adatrack_gps_master.tm_global_audit_logs ORDER BY id DESC LIMIT 100`)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to query global audit logs")
		return
	}
	defer rows.Close()

	var logs []map[string]interface{}
	for rows.Next() {
		var id int
		var companyCode, action, createdAt string
		var actorEmail, actorRole, detail *string
		if err := rows.Scan(&id, &companyCode, &actorEmail, &actorRole, &action, &detail, &createdAt); err != nil {
			continue
		}
		logs = append(logs, map[string]interface{}{
			"id": id, "company_code": companyCode, "actor_email": actorEmail, "actor_role": actorRole, "action": action, "detail": detail, "created_at": createdAt,
		})
	}
	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "data": logs})
}
