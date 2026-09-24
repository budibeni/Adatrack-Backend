// Command api-vehicle serves the fleet management REST API of ADATRACK
// (PRD §8.2, phase B3):
//
// HTTP : /api/v1/vehicles|geofences|routes|speed-configs|alerts + /healthz + /metrics
// Auth : the SAME JWT (HS256) + Redis revocation denylist as service-websocket
// RBAC : tm_user_company_access (role) + tm_user_vehicles (row-level), PRD §3.1
// Data : company-schema CRUD with soft delete + restore (PRD §6.0.1)
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"adatrack_gps/api-vehicle/controllers"
	"adatrack_gps/api-vehicle/models"
	"adatrack_gps/internal"
	"adatrack_gps/internal/tenant"
)

func main() {
	internal.ConfigureLogging()
	internal.LoadProjectEnv()
	cfg := internal.LoadConfig()
	if err := cfg.Validate(); err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	settings := controllers.LoadSettings()
	if err := settings.Validate(); err != nil {
		slog.Error("invalid api-vehicle configuration", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	registry := internal.GetRegistry()
	controllers.RegisterMetrics(registry)

	red, err := internal.NewRedisClient(cfg)
	if err != nil {
		slog.Error("redis unavailable", "error", err)
		os.Exit(1)
	}
	defer func() { _ = red.Close() }()

	// NATS is required by the B8 downlink path: the command endpoint publishes
	// `command.request.<company>` and ingestion-tcp (the only process holding the
	// device sockets) consumes it. Failing fast at boot beats an endpoint that
	// accepts requests it can never deliver.
	nac, err := internal.NewNATSClient(cfg)
	if err != nil {
		slog.Error("nats unavailable", "error", err)
		os.Exit(1)
	}
	defer nac.Close()

	tcfg := tenant.ConfigFromEnv(cfg)
	if err := tcfg.Validate(); err != nil {
		slog.Error("invalid tenant configuration", "error", err,
			"env", "MASTER_DB_NAME/COMPANY_DB_PREFIX/COMPANY_MIGRATIONS_DIR")
		os.Exit(1)
	}
	tm, err := tenant.New(ctx, cfg, tcfg, red, registry)
	if err != nil {
		slog.Error("tenant manager failed", "error", err)
		os.Exit(1)
	}
	defer tm.Close()
	go tm.Run(ctx)

	if cfg.Migrate.OnBoot {
		if _, err := internal.ApplyMigrations(ctx, tm.Master().DB, "master", cfg.Migrate.MasterSchema,
			cfg.Migrate.MasterDir, cfg.Migrate.LedgerTable, cfg.Migrate.LockTimeout); err != nil {
			slog.Error("auto-migration failed", "error", err)
			os.Exit(1)
		}
	}

	service := controllers.NewService(controllers.Deps{
		Settings: settings,
		Store:    controllers.NewPostgresStore(tm),
		KV:       controllers.NewRedisKVWithPrefix(red.Client(), cfg.Redis.KeyPrefix),
		Tenants:  tm,
		Registry: registry,
		Commands: commandPublisher{cfg: cfg, nats: nac},
	})

	server := &http.Server{
		Addr:              settings.HTTPAddr,
		Handler:           service.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		slog.Info("api-vehicle listening", "addr", settings.HTTPAddr,
			"revocation", settings.RevocationEnabled,
			"rate_limit_per_min", settings.APIRateLimit)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http server failed", "addr", settings.HTTPAddr, "error", err)
			stop()
		}
	}()

	<-ctx.Done()
	slog.Info("shutdown signal received; draining connections")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Warn("http server shutdown error", "error", err)
	}
	slog.Info("api-vehicle stopped")
}

// commandPublisher publishes a B8 downlink request on `command.request.<company>`
// (core NATS: the dispatcher in ingestion-tcp subscribes with a queue group).
type commandPublisher struct {
	cfg  *internal.Config
	nats *internal.NATSClient
}

// PublishDeviceCommand marshals the audited row and publishes it to the tenant's
// command subject. A publish error is returned so the handler can report 503 —
// the request row stays `pending` and is never silently dropped.
func (p commandPublisher) PublishDeviceCommand(company string, cmd *models.DeviceCommand) error {
	payload, err := json.Marshal(cmd)
	if err != nil {
		return err
	}
	return p.nats.Publish(internal.CommandRequestSubject(company), payload)
}
