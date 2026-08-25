package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ajb_gps/api-vehicle/controllers"
	"ajb_gps/internal"
	"ajb_gps/internal/tenant"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// ---------------------------------------------------------------------------
// api-vehicle (Phase B3) — REST API manajemen vehicle (multi-tenant, PRD §6):
//   - auth via master DB users (email + bcrypt), JWT HS256 identik B2,
//   - per-request routing ke adatrack_gps_{company_code} via TenantManager,
//   - RBAC user_company_access + user_vehicles; cross-tenant 403.
// Endpoint: vehicles CRUD, user↔vehicle assignment, speed_configs,
// geofence_vehicles, routes + route_assignments.
// ---------------------------------------------------------------------------

func main() {
	internal.ConfigureLogging()
	internal.LoadProjectEnv()

	cfg := internal.LoadConfig()
	if err := cfg.Validate(); err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	// Bind per-service (single env): port HTTP unik api-vehicle.
	cfg.HTTP.Addr = internal.EnvOr("API_VEHICLE_HTTP_ADDR", ":8081")

	metricsReg := getRegistry()
	metricsHTTP := promhttp.HandlerFor(metricsReg, promhttp.HandlerOpts{})

	redis, err := internal.NewRedisClient(cfg, internal.RedisOperations, internal.RedisOperationDuration)
	if err != nil {
		slog.Error("failed to init redis", "error", err)
		os.Exit(1)
	}
	defer redis.Close()

	tcfg := tenant.NewConfigFromEnv()
	if err := tcfg.Validate(); err != nil {
		slog.Error("invalid tenant configuration", "error", err, "env", "MASTER_DB_*/COMPANY_DB_PREFIX")
		os.Exit(1)
	}
	tm, err := tenant.New(context.Background(), tcfg, redis.Client(), metricsReg)
	if err != nil {
		slog.Error("failed to init tenant manager", "error", err)
		os.Exit(1)
	}
	go tm.Run(context.Background())

	controllers.Init(cfg, redis, tm, metricsHTTP)

	srv := &http.Server{
		Addr:         cfg.HTTP.Addr,
		Handler:      controllers.Router(),
		ReadTimeout:  cfg.HTTP.ReadTimeout,
		WriteTimeout: cfg.HTTP.WriteTimeout,
	}

	go func() {
		if cfg.HTTP.TLS.Enabled {
			slog.Info("api-vehicle https listening (TLS)", "addr", cfg.HTTP.Addr, "cert", cfg.HTTP.TLS.CertFile, "key", cfg.HTTP.TLS.KeyFile)
			if err := srv.ListenAndServeTLS(cfg.HTTP.TLS.CertFile, cfg.HTTP.TLS.KeyFile); err != nil && err != http.ErrServerClosed {
				slog.Error("https server failed", "error", err)
				os.Exit(1)
			}
		} else {
			slog.Info("api-vehicle http listening", "addr", cfg.HTTP.Addr)
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				slog.Error("http server failed", "error", err)
				os.Exit(1)
			}
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	slog.Info("shutdown signal received")

	shCtx, scancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer scancel()
	if err := srv.Shutdown(shCtx); err != nil {
		slog.Warn("http shutdown error", "error", err)
	}
	controllers.Shutdown() // tutup tenant pools
	slog.Info("api-vehicle stopped")
}

// getRegistry registers adatrack metrics plus Go runtime collectors.
func getRegistry() *prometheus.Registry {
	r := internal.GetRegistry()
	r.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	r.MustRegister(prometheus.NewGoCollector())
	return r
}
