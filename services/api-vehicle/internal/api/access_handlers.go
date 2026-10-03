package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"backend/internal/auth"
	"backend/internal/dbclient"
	"backend/internal/tenant"
)

// PERSONEL HANDLERS

type PersonelRequest struct {
	Name         string  `json:"name" validate:"required"`
	PersonelType string  `json:"personelType" validate:"required"`
	NIK          *string `json:"nik"`
	Phone        *string `json:"phone"`
	Email        *string `json:"email"`
	Address      *string `json:"address"`
	CardID       *string `json:"cardId"` // String but we will parse to int if not empty
	Status       string  `json:"status"`
	Notes        *string `json:"notes"`
}

func (h *Handler) ListPersonel(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)

	query := fmt.Sprintf(`
		SELECT id, name, personel_type, nik, phone, email, address, card_id, status, notes, created_at, updated_at
		FROM %s.tm_personel
		WHERE deleted_at IS NULL ORDER BY id DESC
	`, schema)

	rows, err := tenant.NewReadRouter(claims.CompanyCode).Query(r.Context(), query)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to list personel")
		return
	}
	defer rows.Close()

	personelList := make([]map[string]interface{}, 0)
	for rows.Next() {
		var id int
		var name, personelType, status string
		var nik, phone, email, address, notes *string
		var cardId *int
		var createdAt, updatedAt time.Time

		if err := rows.Scan(&id, &name, &personelType, &nik, &phone, &email, &address, &cardId, &status, &notes, &createdAt, &updatedAt); err != nil {
			continue
		}

		var cardIdStr *string
		if cardId != nil {
			str := strconv.Itoa(*cardId)
			cardIdStr = &str
		}

		personelList = append(personelList, map[string]interface{}{
			"id":           strconv.Itoa(id),
			"name":         name,
			"personelType": personelType,
			"nik":          nik,
			"phone":        phone,
			"email":        email,
			"address":      address,
			"cardId":       cardIdStr,
			"status":       status,
			"notes":        notes,
			"createdAt":    createdAt.Format(time.RFC3339),
			"updatedAt":    updatedAt.Format(time.RFC3339),
		})
	}
	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
		"data":   personelList,
	})
}

func (h *Handler) CreatePersonel(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}

	var req PersonelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid request body")
		return
	}

	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	
	var cardIDInt *int
	if req.CardID != nil && *req.CardID != "" {
		parsed, err := strconv.Atoi(*req.CardID)
		if err == nil {
			cardIDInt = &parsed
		}
	}

	var newID int
	query := fmt.Sprintf(`
		INSERT INTO %s.tm_personel (name, personel_type, nik, phone, email, address, card_id, status, notes)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id
	`, schema)
	
	err := dbclient.Pool.QueryRow(r.Context(), query, 
		req.Name, req.PersonelType, req.NIK, req.Phone, req.Email, req.Address, cardIDInt, req.Status, req.Notes,
	).Scan(&newID)
	
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to create personel")
		return
	}
	
	h.auditLog(r.Context(), claims.CompanyCode, "CREATE_PERSONEL", "SUCCESS", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Created personel %d", newID))

	h.writeJSON(w, http.StatusCreated, map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"id": strconv.Itoa(newID),
		},
	})
}

func (h *Handler) UpdatePersonel(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}

	idStr := chi.URLParam(r, "id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_ID", "Invalid ID")
		return
	}

	var req PersonelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid request body")
		return
	}

	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	
	var cardIDInt *int
	if req.CardID != nil && *req.CardID != "" {
		parsed, err := strconv.Atoi(*req.CardID)
		if err == nil {
			cardIDInt = &parsed
		}
	}

	query := fmt.Sprintf(`
		UPDATE %s.tm_personel
		SET name = $1, personel_type = $2, nik = $3, phone = $4, email = $5, address = $6, card_id = $7, status = $8, notes = $9, updated_at = CURRENT_TIMESTAMP
		WHERE id = $10 AND deleted_at IS NULL
	`, schema)

	_, err = dbclient.Pool.Exec(r.Context(), query,
		req.Name, req.PersonelType, req.NIK, req.Phone, req.Email, req.Address, cardIDInt, req.Status, req.Notes, id,
	)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to update personel")
		return
	}
	
	h.auditLog(r.Context(), claims.CompanyCode, "UPDATE_PERSONEL", "SUCCESS", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Updated personel %d", id))

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
	})
}

func (h *Handler) DeletePersonel(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}

	idStr := chi.URLParam(r, "id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_ID", "Invalid ID")
		return
	}

	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	query := fmt.Sprintf(`UPDATE %s.tm_personel SET deleted_at = CURRENT_TIMESTAMP WHERE id = $1`, schema)

	_, err = dbclient.Pool.Exec(r.Context(), query, id)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to delete personel")
		return
	}

	h.auditLog(r.Context(), claims.CompanyCode, "DELETE_PERSONEL", "SUCCESS", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Deleted personel %d", id))

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
	})
}

// CARD HANDLERS

type CardRequest struct {
	UID        string  `json:"uid" validate:"required"`
	Name       string  `json:"name" validate:"required"`
	Type       string  `json:"type" validate:"required"`
	HolderType *string `json:"holderType"`
	HolderId   *string `json:"holderId"`
	Status     string  `json:"status"`
}

func (h *Handler) ListCards(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)

	query := fmt.Sprintf(`
		SELECT id, card_number, COALESCE(name, ''), COALESCE(type, 'RFID'), assigned_to_type, assigned_to_id, status, created_at, updated_at
		FROM %s.tm_rfid_cards
		WHERE deleted_at IS NULL ORDER BY id DESC
	`, schema)

	rows, err := tenant.NewReadRouter(claims.CompanyCode).Query(r.Context(), query)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to list cards")
		return
	}
	defer rows.Close()

	cardList := make([]map[string]interface{}, 0)
	for rows.Next() {
		var id int
		var cardNumber, name, cardType, status string
		var assignedToType *string
		var assignedToId *int
		var createdAt, updatedAt time.Time

		if err := rows.Scan(&id, &cardNumber, &name, &cardType, &assignedToType, &assignedToId, &status, &createdAt, &updatedAt); err != nil {
			continue
		}

		var holderIdStr *string
		if assignedToId != nil {
			str := strconv.Itoa(*assignedToId)
			holderIdStr = &str
		}

		cardList = append(cardList, map[string]interface{}{
			"id":         strconv.Itoa(id),
			"uid":        cardNumber,
			"name":       name,
			"type":       cardType,
			"holderType": assignedToType,
			"holderId":   holderIdStr,
			"status":     status,
			"createdAt":  createdAt.Format(time.RFC3339),
			"updatedAt":  updatedAt.Format(time.RFC3339),
		})
	}
	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
		"data":   cardList,
	})
}

func (h *Handler) CreateCard(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}

	var req CardRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid request body")
		return
	}

	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	
	var holderIDInt *int
	if req.HolderId != nil && *req.HolderId != "" {
		parsed, err := strconv.Atoi(*req.HolderId)
		if err == nil {
			holderIDInt = &parsed
		}
	}

	var newID int
	query := fmt.Sprintf(`
		INSERT INTO %s.tm_rfid_cards (card_number, name, type, assigned_to_type, assigned_to_id, status)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id
	`, schema)
	
	err := dbclient.Pool.QueryRow(r.Context(), query, 
		req.UID, req.Name, req.Type, req.HolderType, holderIDInt, req.Status,
	).Scan(&newID)
	
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to create card, possibly duplicate UID")
		return
	}
	
	h.auditLog(r.Context(), claims.CompanyCode, "CREATE_CARD", "SUCCESS", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Created card %d", newID))

	h.writeJSON(w, http.StatusCreated, map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"id": strconv.Itoa(newID),
		},
	})
}

func (h *Handler) UpdateCard(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}

	idStr := chi.URLParam(r, "id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_ID", "Invalid ID")
		return
	}

	var req CardRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid request body")
		return
	}

	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	
	var holderIDInt *int
	if req.HolderId != nil && *req.HolderId != "" {
		parsed, err := strconv.Atoi(*req.HolderId)
		if err == nil {
			holderIDInt = &parsed
		}
	}

	query := fmt.Sprintf(`
		UPDATE %s.tm_rfid_cards
		SET card_number = $1, name = $2, type = $3, assigned_to_type = $4, assigned_to_id = $5, status = $6, updated_at = CURRENT_TIMESTAMP
		WHERE id = $7 AND deleted_at IS NULL
	`, schema)

	_, err = dbclient.Pool.Exec(r.Context(), query,
		req.UID, req.Name, req.Type, req.HolderType, holderIDInt, req.Status, id,
	)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to update card")
		return
	}
	
	h.auditLog(r.Context(), claims.CompanyCode, "UPDATE_CARD", "SUCCESS", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Updated card %d", id))

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
	})
}

func (h *Handler) DeleteCard(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}

	idStr := chi.URLParam(r, "id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_ID", "Invalid ID")
		return
	}

	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	query := fmt.Sprintf(`UPDATE %s.tm_rfid_cards SET deleted_at = CURRENT_TIMESTAMP WHERE id = $1`, schema)

	_, err = dbclient.Pool.Exec(r.Context(), query, id)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to delete card")
		return
	}

	h.auditLog(r.Context(), claims.CompanyCode, "DELETE_CARD", "SUCCESS", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Deleted card %d", id))

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
	})
}

// LOG HANDLERS

func (h *Handler) ListAccessLogs(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)

	// In real life, we should query th_access_logs and join with tm_rfid_cards and tm_vehicles.
	// For now, since logs are generated by hardware, we will just fetch it directly.
	
	query := fmt.Sprintf(`
		SELECT l.id, l.card_number, COALESCE(c.name, ''), COALESCE(c.assigned_to_type, ''), v.plate_number, l.access_time, 
		       l.location_lat, l.location_lon, l.status
		FROM %s.th_access_logs l
		LEFT JOIN %s.tm_rfid_cards c ON l.card_number = c.card_number
		LEFT JOIN adatrack_gps_master.tm_gps_devices d ON l.reader_device_imei = d.imei
		LEFT JOIN %s.tm_vehicles v ON d.imei = v.imei
		ORDER BY l.access_time DESC LIMIT 100
	`, schema, schema, schema)

	rows, err := tenant.NewReadRouter(claims.CompanyCode).Query(r.Context(), query)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to list logs")
		return
	}
	defer rows.Close()

	logList := make([]map[string]interface{}, 0)
	for rows.Next() {
		var id int
		var cardUid, cardName, holderType, status string
		var vehiclePlate *string
		var accessTime time.Time
		var lat, lon *float64

		if err := rows.Scan(&id, &cardUid, &cardName, &holderType, &vehiclePlate, &accessTime, &lat, &lon, &status); err != nil {
			continue
		}

		locationStr := "N/A"
		if lat != nil && lon != nil {
			locationStr = fmt.Sprintf("%.6f, %.6f", *lat, *lon)
		}

		logList = append(logList, map[string]interface{}{
			"id":                 strconv.Itoa(id),
			"cardUid":            cardUid,
			"cardName":           cardName,
			"holderName":         "", // Would need to join driver or personel based on holderType
			"holderType":         holderType,
			"activityType":       "ACCESS", // default
			"vehiclePlateNumber": vehiclePlate,
			"timestamp":          accessTime.Format(time.RFC3339),
			"location":           locationStr,
			"status":             status,
		})
	}
	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
		"data":   logList,
	})
}
