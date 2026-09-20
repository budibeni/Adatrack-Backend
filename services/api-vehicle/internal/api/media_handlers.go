package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"backend/internal/auth"
	"backend/internal/dbclient"
	"backend/internal/natsclient"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type MediaEventReq struct {
	VehicleID string `json:"vehicle_id"`
	EventType string `json:"event_type"` // sos, alarm, geofence, overspeed, manual, scheduled, power
	FileType  string `json:"file_type"`  // image/jpeg, video/mp4
	FileName  string `json:"file_name"`
}

func verifyHMAC(secret, signature string, body []byte) bool {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(body)
	expected := hex.EncodeToString(h.Sum(nil))
	return hmac.Equal([]byte(signature), []byte(expected))
}

func (h *Handler) getMediaConfigSecret(ctx context.Context, companyCode string) (string, error) {
	var secret string
	err := dbclient.Pool.QueryRow(ctx, "SELECT hmac_secret FROM adatrack_gps_master.tm_company_media_config WHERE company_code = $1", companyCode).Scan(&secret)
	return secret, err
}

func (h *Handler) CreateMediaEvent(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized")
		return
	}

	// parse multipart
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		h.writeError(w, http.StatusBadRequest, "BAD_REQUEST", "Failed to parse multipart form")
		return
	}

	vidStr := r.FormValue("vehicle_id")
	eventType := r.FormValue("event_type")

	vid, _ := strconv.Atoi(vidStr)
	file, header, err := r.FormFile("file")
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "BAD_REQUEST", "File is required")
		return
	}
	defer file.Close()

	uid := uuid.New().String()
	yyyyMM := time.Now().Format("200601")
	
	key, err := h.store.Upload(r.Context(), claims.CompanyCode, vidStr, yyyyMM, uid, header.Header.Get("Content-Type"), file, header.Size)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "STORAGE_ERROR", "Failed to upload file")
		return
	}

	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	query := fmt.Sprintf(`
		INSERT INTO %s.th_media_events 
		(vehicle_id, status, storage_key, content_type, media_type, size_bytes, uploaded_at)
		VALUES ($1, 'pending', $2, $3, $4, $5, NOW()) RETURNING id
	`, schema)

	var id int
	err = dbclient.Pool.QueryRow(r.Context(), query, vid, key, header.Header.Get("Content-Type"), eventType, header.Size).Scan(&id)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to insert media event")
		return
	}

	h.auditLog(r.Context(), claims.CompanyCode, "MEDIA_UPLOADED", "success", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Media %d uploaded", id))
	
	// Publish to Websocket
	mediaPayload := map[string]interface{}{
		"type": "MEDIA_EVENT",
		"company_code": claims.CompanyCode,
		"vehicle_id": vid,
		"media_id": id,
		"event_type": eventType,
	}
	payloadBytes, _ := json.Marshal(mediaPayload)
	natsclient.NC.Publish(fmt.Sprintf("media.event.%s.%d", claims.CompanyCode, vid), payloadBytes)

	h.writeJSON(w, http.StatusCreated, map[string]interface{}{"status": "success", "data": map[string]interface{}{"id": id, "key": key}})
}

func (h *Handler) CompleteMediaEvent(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized")
		return
	}
	idStr := chi.URLParam(r, "id")
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)

	_, err := dbclient.Pool.Exec(r.Context(), fmt.Sprintf("UPDATE %s.th_media_events SET status = 'complete', expires_at = NOW() + INTERVAL '30 days' WHERE id = $1 AND status = 'pending'", schema), idStr)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to complete media event")
		return
	}

	h.auditLog(r.Context(), claims.CompanyCode, "MEDIA_COMPLETED", "success", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Media %s completed", idStr))
	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success"})
}

func (h *Handler) GetMediaURL(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized")
		return
	}
	idStr := chi.URLParam(r, "id")
	
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	var key string
	err := dbclient.Pool.QueryRow(r.Context(), fmt.Sprintf("SELECT storage_key FROM %s.th_media_events WHERE id = $1 AND deleted_at IS NULL", schema), idStr).Scan(&key)
	if err != nil {
		h.writeError(w, http.StatusNotFound, "NOT_FOUND", "Media not found")
		return
	}

	// Since GetPresignedURL rebuilds it, let's just use the client directly:
	urlDirect, err := h.store.Client().PresignedGetObject(r.Context(), h.cfg.S3BucketName, key, time.Hour, nil)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "STORAGE_ERROR", "Failed to generate URL")
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "data": map[string]interface{}{"url": urlDirect.String()}})
}

func (h *Handler) DeleteMediaEvent(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized")
		return
	}
	idStr := chi.URLParam(r, "id")
	
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	_, err := dbclient.Pool.Exec(r.Context(), fmt.Sprintf("UPDATE %s.th_media_events SET deleted_at = NOW() WHERE id = $1", schema), idStr)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to delete media")
		return
	}

	h.auditLog(r.Context(), claims.CompanyCode, "MEDIA_DELETED", "success", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Media %s soft deleted", idStr))
	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success"})
}

func (h *Handler) RestoreMediaEvent(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized")
		return
	}
	idStr := chi.URLParam(r, "id")
	
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)
	_, err := dbclient.Pool.Exec(r.Context(), fmt.Sprintf("UPDATE %s.th_media_events SET deleted_at = NULL WHERE id = $1", schema), idStr)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to restore media")
		return
	}

	h.auditLog(r.Context(), claims.CompanyCode, "MEDIA_RESTORED", "success", claims.UserID, claims.Email, claims.Role, fmt.Sprintf("Media %s restored", idStr))
	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success"})
}

func (h *Handler) ListMediaEvents(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized")
		return
	}
	vidStr := chi.URLParam(r, "id")
	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)

	query := fmt.Sprintf(`
		SELECT id, status, content_type, media_type, size_bytes, uploaded_at, expires_at 
		FROM %s.th_media_events 
		WHERE vehicle_id = $1 AND deleted_at IS NULL
		ORDER BY uploaded_at DESC LIMIT 100
	`, schema)

	rows, err := dbclient.Pool.Query(r.Context(), query, vidStr)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to fetch media events")
		return
	}
	defer rows.Close()

	var events []map[string]interface{}
	for rows.Next() {
		var id int
		var status, contentType, mediaType string
		var sizeBytes int64
		var uploadedAt, expiresAt *time.Time
		if err := rows.Scan(&id, &status, &contentType, &mediaType, &sizeBytes, &uploadedAt, &expiresAt); err == nil {
			events = append(events, map[string]interface{}{
				"id":           id,
				"status":       status,
				"content_type": contentType,
				"media_type":   mediaType,
				"size_bytes":   sizeBytes,
				"uploaded_at":  uploadedAt,
				"expires_at":   expiresAt,
			})
		}
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "data": events})
}
