package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"

	"backend/internal/auth"
	"backend/internal/dbclient"
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
	err := dbclient.Pool.QueryRow(ctx, "SELECT hmac_secret FROM master.tm_company_media_config WHERE company_code = $1", companyCode).Scan(&secret)
	return secret, err
}

func (h *Handler) CreateMediaEvent(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	_ = claims.CompanyCode
	w.WriteHeader(http.StatusCreated)
}

func (h *Handler) CompleteMediaEvent(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) GetMediaURL(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) DeleteMediaEvent(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) RestoreMediaEvent(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}
