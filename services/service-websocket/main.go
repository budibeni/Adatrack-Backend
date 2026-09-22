// Command service-websocket serves the authenticated REST API and the real-time
// WebSocket fan-out of ADATRACK (PRD Module 5, phase B2):
//
//	HTTP  : REST (§8.2) + WS (§8.3) + /healthz + /metrics on HTTP_ADDR (:8082)
//	Auth  : bcrypt login → JWT HS256 (24 h) + rotating opaque refresh token
//	RBAC  : tm_user_company_access (role) + tm_user_vehicles (row-level)
//	WS    : telemetry.live.<IMEI> --(queue group "websocket")--> hub --> clients
//
// Every mutation/security event is written to the append-only `tm_audit_logs`
// (PRD §9.4); sensitive actions are fail-closed.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"adatrack_gps/internal"
	"adatrack_gps/internal/tenant"
	"adatrack_gps/service-websocket/controllers"
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
		slog.Error("invalid service-websocket configuration", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	registry := internal.GetRegistry()
	controllers.RegisterMetrics(registry)
	controllers.RegisterBridgeMetrics(registry)

	red, err := internal.NewRedisClient(cfg)
	if err != nil {
		slog.Error("redis unavailable", "error", err)
		os.Exit(1)
	}
	defer func() { _ = red.Close() }()

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

	nac, err := internal.NewNATSClient(cfg)
	if err != nil {
		slog.Error("nats unavailable", "error", err)
		os.Exit(1)
	}
	defer nac.Close()

	store := controllers.NewPostgresStore(tm)
	service := controllers.NewService(controllers.Deps{
		Settings: settings,
		Store:    store,
		KV:       controllers.NewRedisKV(red.Client()),
		Live:     controllers.NewRedisLiveState(red),
		Redis:    red,
		NATS:     nac,
		Registry: registry,
	})
	service.Start()
	defer service.Stop()

	bridge := controllers.NewBridge(cfg, nac, service.Hub())
	subs, err := bridge.Start()
	if err != nil {
		slog.Error("failed to subscribe the WebSocket bridges (telemetry.live / notify.alert / media.event)",
			"error", err)
		os.Exit(1)
	}
	defer func() {
		for _, sub := range subs {
			nac.Unsubscribe(sub)
		}
	}()

	server := &http.Server{
		Addr:              settings.HTTPAddr,
		Handler:           service.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		// Websocket connections are long-lived: WriteTimeout must stay 0 so the
		// hijacked connection is not cut off (gorilla manages its own deadlines).
		WriteTimeout: 0,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		slog.Info("service-websocket listening", "addr", settings.HTTPAddr,
			"jwt_expiry_h", settings.AccessExpiry.Hours(),
			"refresh_expiry_h", settings.RefreshExpiry.Hours(),
			"revocation", settings.RevocationEnabled,
			"ws_max_connections", settings.WSMaxConnections)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http server failed", "addr", settings.HTTPAddr, "error", err)
			stop()
		}
	}()

	<-ctx.Done()
	slog.Info("shutdown signal received; draining connections")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	service.Stop()
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Warn("http server shutdown error", "error", err)
	}
	slog.Info("service-websocket stopped")
}
