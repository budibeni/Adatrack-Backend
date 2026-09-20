package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os/exec"
	"strings"

	"github.com/go-chi/chi/v5"
)

type Handler struct{}

func NewHandler() *Handler {
	return &Handler{}
}

type ServiceInfo struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	State  string `json:"state"`
	Status string `json:"status"`
}

func (h *Handler) ListServices(w http.ResponseWriter, r *http.Request) {
	cmd := exec.CommandContext(r.Context(), "docker", "ps", "-a", "--format", "{{.ID}}\t{{.Names}}\t{{.State}}\t{{.Status}}")
	output, err := cmd.Output()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var services []ServiceInfo
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) >= 4 {
			name := parts[1]
			if strings.HasPrefix(name, "adatrack_") {
				services = append(services, ServiceInfo{
					ID:     parts[0],
					Name:   name,
					State:  parts[2],
					Status: parts[3],
				})
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(services)
}

func (h *Handler) StartService(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	cmd := exec.CommandContext(r.Context(), "docker", "start", id)
	if err := cmd.Run(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"started"}`))
}

func (h *Handler) StopService(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	cmd := exec.CommandContext(r.Context(), "docker", "stop", id)
	if err := cmd.Run(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"stopped"}`))
}

func (h *Handler) GetLogs(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	cmd := exec.CommandContext(r.Context(), "docker", "logs", "--tail", "100", id)
	
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Run(); err != nil {
		// Even if docker logs returns non-zero, we might have logs.
		// So we ignore err if out is not empty, but docker logs shouldn't error unless container not found.
		if out.Len() == 0 {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "text/plain")
	w.Write(out.Bytes())
}
