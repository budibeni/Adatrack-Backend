package internal

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// HealthCheck is one readiness dependency (PRD §10.2).
type HealthCheck struct {
	Name string
	// Critical dependencies make /healthz fail; optional ones are reported only.
	Critical bool
	Fn       func(ctx context.Context) error
}

// HealthServer serves /healthz (readiness) and /metrics (Prometheus).
type HealthServer struct {
	Addr     string
	Checks   []HealthCheck
	Registry *prometheus.Registry
	Server   *http.Server
}

// NewHealthServer builds the health/metrics server for a service.
func NewHealthServer(addr string, registry *prometheus.Registry, checks ...HealthCheck) *HealthServer {
	h := &HealthServer{Addr: addr, Checks: checks, Registry: registry}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", h.handleHealth)
	mux.HandleFunc("/livez", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	if registry != nil {
		mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	}
	h.Server = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
	}
	return h
}

// Start begins serving in a background goroutine (non-fatal on failure: the
// service keeps running and the error is logged loudly).
func (h *HealthServer) Start() {
	go func() {
		slog.Info("health/metrics server listening", "addr", h.Addr)
		if err := h.Server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("health/metrics server failed", "addr", h.Addr, "error", err)
		}
	}()
}

// Shutdown stops the server gracefully.
func (h *HealthServer) Shutdown(ctx context.Context) {
	if h == nil || h.Server == nil {
		return
	}
	if err := h.Server.Shutdown(ctx); err != nil {
		slog.Warn("health server shutdown error", "error", err)
	}
}

// healthResponse is the /healthz JSON body.
type healthResponse struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks,omitempty"`
	Time   string            `json:"time"`
}

// handleHealth runs every dependency check and returns 200 only when all
// critical dependencies are healthy (readiness gate).
func (h *HealthServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	resp := healthResponse{Status: "ok", Checks: map[string]string{}, Time: time.Now().UTC().Format(time.RFC3339)}
	status := http.StatusOK

	for _, check := range h.Checks {
		if check.Fn == nil {
			continue
		}
		if err := check.Fn(ctx); err != nil {
			resp.Checks[check.Name] = "error: " + err.Error()
			if check.Critical {
				resp.Status = "unavailable"
				status = http.StatusServiceUnavailable
			}
		} else {
			resp.Checks[check.Name] = "ok"
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
}
