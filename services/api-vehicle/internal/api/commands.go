package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"backend/internal/auth"
	"backend/internal/logger"
	"backend/internal/natsclient"
	"backend/internal/tenant"
	"github.com/go-chi/chi/v5"
)

type CommandRequest struct {
	Type   string            `json:"type"`
	Params map[string]string `json:"params"`
	Raw    string            `json:"raw"`
}

func (h *Handler) SendCommand(w http.ResponseWriter, r *http.Request) {
	vehicleID := chi.URLParam(r, "id")
	if vehicleID == "" {
		http.Error(w, "missing vehicle ID", http.StatusBadRequest)
		return
	}

	var req CommandRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	claims, ok := r.Context().Value(auth.ClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}

	schema := fmt.Sprintf("adatrack_gps_%s", claims.CompanyCode)

	var imei string
	err := tenant.NewReadRouter(claims.CompanyCode).QueryRow(r.Context(), fmt.Sprintf("SELECT imei FROM %s.tm_vehicles WHERE id = $1 AND deleted_at IS NULL", schema), vehicleID).Scan(&imei)
	if err != nil {
		http.Error(w, "vehicle not found", http.StatusNotFound)
		return
	}

	// Publish to NATS
	subject := fmt.Sprintf("downlink.commands.%s", imei)
	data, _ := json.Marshal(req)
	if err := natsclient.NC.Publish(subject, data); err != nil {
		logger.Log.Error("Failed to publish command", "err", err, "imei", imei)
		http.Error(w, "failed to send command", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "command_sent"})
}
