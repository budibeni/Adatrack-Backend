package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"ajb_gps/internal"
	"ajb_gps/internal/tenant"
	"ajb_gps/worker-alert/controllers"

	"github.com/prometheus/client_golang/prometheus"
)

// ---------------------------------------------------------------------------
// worker-alert (Phase B3) — multi-tenant alert detection.
//
// Subscribe telemetry.raw.> (queue "alert") lalu menjalankan deteksi:
//   - Geofence breach (entry/exit via geofence_state di Redis, circle+polygon)
//   - Over speeding (speed_configs + grace margin)
//   - BATTERY_LOW, OFFLINE (monitor live-state Redis)
//   - SOS (critical, dedup, eskalasi otomatis + TTA)
//   - ROUTE_DEVIATION (routes + route_assignments)
// Semua alert tersimpan ke alerts di adatrack_gps_{company_code}, dipublikasikan
// ke alert.<type>.<company>, dan dikirim sesuai notification_preferences
// (websocket/email/sms + audit notifications).
// ---------------------------------------------------------------------------

func main() {
	internal.ConfigureLogging()
	internal.LoadProjectEnv()

	cfg := internal.LoadConfig()
	if err := cfg.Validate(); err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	cfg.Server.Addr = internal.EnvOr("ALERT_METRICS_ADDR", ":8093")

	registry := internal.GetRegistry()
	registry.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	registry.MustRegister(prometheus.NewGoCollector())

	redis, err := internal.NewRedisClient(cfg, internal.RedisOperations, internal.RedisOperationDuration)
	if err != nil {
		slog.Error("failed to init redis", "error", err)
		os.Exit(1)
	}
	defer redis.Close()

	// TenantManager: master + per-company DB pools (PRD §6).
	tcfg := tenant.NewConfigFromEnv()
	if err := tcfg.Validate(); err != nil {
		slog.Error("invalid tenant configuration", "error", err, "env", "MASTER_DB_*/COMPANY_DB_PREFIX")
		os.Exit(1)
	}
	tm, err := tenant.New(context.Background(), tcfg, redis.Client(), registry)
	if err != nil {
		slog.Error("failed to init tenant manager", "error", err)
		os.Exit(1)
	}
	defer tm.Close()
	go tm.Run(context.Background())

	nac, err := internal.NewNATSClient(cfg,
		internal.NATSMessagesPublished, internal.NATSMessagesConsumed, internal.NATPendingMessages)
	if err != nil {
		slog.Error("failed to init nats", "error", err)
		os.Exit(1)
	}
	defer nac.Close()

	wa, err := controllers.New(cfg, tm, nac, redis, registry)
	if err != nil {
		slog.Error("failed to init worker-alert", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	wa.SetContext(ctx, cancel)

	if err := wa.Start(); err != nil {
		slog.Error("failed to start worker-alert", "error", err)
		os.Exit(1)
	}

	go wa.ServeMetrics()
	slog.Info("worker-alert started")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	slog.Info("shutdown signal received, draining...")
	wa.Stop()
	slog.Info("worker-alert stopped")
}

