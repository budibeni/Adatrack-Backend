package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"ajb_gps/internal"
	"ajb_gps/internal/tenant"
	sw "ajb_gps/service-websocket/controllers"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// ---------------------------------------------------------------------------
// service-websocket (Phase B2) — thin entry point.
//
// REST API (Gin) + WebSocket (gorilla/websocket) + RBAC (JWT + user_vehicles),
// MULTI-TENANT aware (PRD §6):
//   - auth via master DB (users, global auth authority, email + bcrypt),
//   - per-request routing ke adatrack_gps_{company_code} via TenantManager,
//   - RBAC row-level via user_vehicles di company DB; cross-tenant denied 403.
//
//   - REST: login, vehicles, history, geofences, alerts (GAP #1/#3).
//   - WebSocket: subscribe vehicle.update.<id>, push VEHICLE_UPDATE only to the
//     authorised user (FR-5.1/5.2), ping every 30s (FR-5.3), resource limits
//     (FR-5.4): max conn, send buffer 256KB, queue 1000.
//   - NATS: queue group "websocket" on subject telemetry.raw.<IMEI> (GAP #9).
//
// All wiring lives in controllers.Setup/Router/Shutdown; main only orchestrates
// construction, lifecycle, and graceful shutdown.
// ---------------------------------------------------------------------------

func main() {
	internal.ConfigureLogging()
	internal.LoadProjectEnv()

	cfg := internal.LoadConfig()
	if err := cfg.Validate(); err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	// Bind per-service (single env): main HTTP + metrics pada port masing-masing.
	cfg.HTTP.Addr = internal.EnvOr("WEBSOCKET_HTTP_ADDR", ":8080")
	cfg.HTTP.MetricsAddr = internal.EnvOr("WEBSOCKET_METRICS_ADDR", ":9090")

	metricsReg := getRegistry()
	metricsHTTP := promhttp.HandlerFor(metricsReg, promhttp.HandlerOpts{})

	redis, err := internal.NewRedisClient(cfg, internal.RedisOperations, internal.RedisOperationDuration)
	if err != nil {
		slog.Error("failed to init redis", "error", err)
		os.Exit(1)
	}
	defer redis.Close()

	// TenantManager: master + per-company DB pools (PRD §6). Redis dipakai
	// sebagai cache IMEI → company untuk bridge (tenant resolution anti-spoofing).
	tcfg := tenant.NewConfigFromEnv()
	if err := tcfg.Validate(); err != nil {
		slog.Error("invalid tenant configuration", "error", err, "env", "MASTER_DB_*/COMPANY_DB_PREFIX")
		os.Exit(1)
	}
	// Auto-provision company (PRD §6.1 / FR-5.5) butuh template migrasi. Bila
	// COMPANY_MIGRATIONS_DIR belum diset, resolve dari lokasi biasa di dev
	// (backend/database/migrations/company relatif ke CWD service).
	if tcfg.MigrationsDir == "" {
		tcfg.MigrationsDir = resolveMigrationsDir()
	}
	tm, err := tenant.New(context.Background(), tcfg, redis.Client(), metricsReg)
	if err != nil {
		slog.Error("failed to init tenant manager", "error", err)
		os.Exit(1)
	}
	defer tm.Close()
	go tm.Run(context.Background())

	nats, err := internal.NewNATSClient(cfg,
		internal.NATSMessagesPublished, internal.NATSMessagesConsumed, internal.NATPendingMessages)
	if err != nil {
		slog.Error("failed to init nats", "error", err)
		os.Exit(1)
	}
	defer nats.Close()

	unsubscribe, err := sw.Setup(cfg, redis, nats, metricsHTTP, tm)
	if err != nil {
		slog.Error("failed to setup controllers", "error", err)
		os.Exit(1)
	}
	defer unsubscribe()

	srv := &http.Server{
		Addr:         cfg.HTTP.Addr,
		Handler:      sw.Router(),
		ReadTimeout:  cfg.HTTP.ReadTimeout,
		WriteTimeout: cfg.HTTP.WriteTimeout,
	}
	go func() {
		if cfg.HTTP.TLS.Enabled {
			slog.Info("service-websocket https listening (TLS/WSS)", "addr", cfg.HTTP.Addr,
				"cert", cfg.HTTP.TLS.CertFile, "key", cfg.HTTP.TLS.KeyFile)
			if err := srv.ListenAndServeTLS(cfg.HTTP.TLS.CertFile, cfg.HTTP.TLS.KeyFile); err != nil && err != http.ErrServerClosed {
				slog.Error("https server failed", "error", err)
				os.Exit(1)
			}
		} else {
			slog.Info("service-websocket http listening", "addr", cfg.HTTP.Addr)
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				slog.Error("http server failed", "error", err)
				os.Exit(1)
			}
		}
	}()

	go serveMetrics(cfg, metricsHTTP)

	slog.Info("service-websocket started")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	slog.Info("shutdown signal received")

	// Graceful shutdown: HTTP drain + WS connections + NATS unsubscribe.
	shCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shCtx); err != nil {
		slog.Warn("http shutdown error", "error", err)
	}
	sw.Shutdown()
	slog.Info("service-websocket stopped")
}

// getRegistry registers adatrack metrics plus Go runtime collectors.
func getRegistry() *prometheus.Registry {
	r := internal.GetRegistry()
	r.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	r.MustRegister(prometheus.NewGoCollector())
	return r
}

// resolveMigrationsDir locates the company migration template (backend/
// database/migrations/company) relative to the service CWD. Candidates cover
// common dev launch directories: services/service-websocket, services/, backend/.
// Returns "" when not found (auto-provision will then be unavailable).
func resolveMigrationsDir() string {
	candidates := []string{
		"../../database/migrations/company", // CWD = services/service-websocket
		"../database/migrations/company",    // CWD = services/
		"database/migrations/company",       // CWD = backend/
	}
	for _, cand := range candidates {
		if matches, err := filepath.Glob(filepath.Join(cand, "*.sql")); err == nil && len(matches) > 0 {
			if abs, err := filepath.Abs(cand); err == nil {
				slog.Info("resolved company migrations dir", "dir", abs)
				return abs
			}
			return cand
		}
	}
	slog.Warn("company migrations dir not found; auto-provision POST /api/v1/companies will be unavailable")
	return ""
}

// serveMetrics exposes /healthz and /metrics on the dedicated metrics address.
func serveMetrics(cfg *internal.Config, metrics http.Handler) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", metrics)
	// /healthz lives on the main HTTP server (controllers.Router); the metrics
	// port only needs /metrics.
	srv := &http.Server{Addr: cfg.HTTP.MetricsAddr, Handler: mux}
	slog.Info("metrics server listening", "addr", cfg.HTTP.MetricsAddr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("metrics server failed", "error", err)
	}
}
