package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"backend/internal/logger"
	"backend/internal/natsclient"
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

	// Find the IMEI for this vehicle
	var imei string
	err := h.db.QueryRow(r.Context(), "SELECT imei FROM tm_vehicles WHERE id = $1 AND deleted_at IS NULL", vehicleID).Scan(&imei)
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
