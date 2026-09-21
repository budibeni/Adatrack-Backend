package api

import (
	"context"
	"net/http"
	"time"

	"backend/internal/dbclient"
	"backend/internal/redclient"
	"backend/internal/natsclient"
)

type ServiceInfo struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	State  string `json:"state"`
	Status string `json:"status"`
}

func (h *Handler) GetServices(w http.ResponseWriter, r *http.Request) {
	services := []ServiceInfo{}

	// Check DB
	dbState := "stopped"
	dbStatus := "Disconnected"
	if dbclient.Pool != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		err := dbclient.Pool.Ping(ctx)
		cancel()
		if err == nil {
			dbState = "running"
			dbStatus = "Up and healthy"
		} else {
			dbStatus = "Error: " + err.Error()
		}
	}
	services = append(services, ServiceInfo{ID: "postgres", Name: "PostgreSQL Database", State: dbState, Status: dbStatus})

	// Check Redis
	redisState := "stopped"
	redisStatus := "Disconnected"
	if redclient.Client != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		err := redclient.Client.Ping(ctx).Err()
		cancel()
		if err == nil {
			redisState = "running"
			redisStatus = "Up and healthy"
		} else {
			redisStatus = "Error: " + err.Error()
		}
	}
	services = append(services, ServiceInfo{ID: "redis", Name: "Redis Cache", State: redisState, Status: redisStatus})

	// Check NATS
	natsState := "stopped"
	natsStatus := "Disconnected"
	if natsclient.NC != nil && natsclient.NC.IsConnected() {
		natsState = "running"
		natsStatus = "Up and healthy"
	}
	services = append(services, ServiceInfo{ID: "nats", Name: "NATS Message Broker", State: natsState, Status: natsStatus})

	// Core API
	services = append(services, ServiceInfo{ID: "api", Name: "Websocket API Core", State: "running", Status: "Up and healthy"})

	h.writeJSON(w, http.StatusOK, services)
}

func (h *Handler) ControlService(w http.ResponseWriter, r *http.Request) {
	// Mock endpoint for start/stop/restart
	h.writeJSON(w, http.StatusOK, map[string]string{"message": "Action received"})
}
