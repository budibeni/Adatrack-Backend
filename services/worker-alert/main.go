// Command worker-alert is the real-time alert engine (PRD Module 6, phase B3):
//
//	telemetry.raw.> --(queue group "alert")--> detectors --> th_alerts
//	                                          \--> alert.<category>.<company>
//	                                          \--> notify.alert.<vehicle_id> + td_notifications
//
// Detectors (PRD §5.9): GEOFENCE (circle/polygon), OVERSPEEDING, SOS (GT06
// 0x26/0x27/0x19), BATTERY_LOW, OFFLINE (staleness sweeper), ROUTE_DEVIATION.
// Every alert flows through a dedup window + one-open-alert guard and the
// open→acknowledged→resolved life-cycle; notifications follow
// tm_notification_preferences (websocket fan-out + email/SMS/push audit rows).
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"ajb_gps/internal"
	"ajb_gps/internal/tenant"
	"ajb_gps/worker-alert/controllers"
)

// errNATS is reported by the readiness probe when NATS is disconnected.
var errNATS = errors.New("nats: disconnected")

func main() {
	internal.ConfigureLogging()
	internal.LoadProjectEnv()

	cfg := internal.LoadConfig()
	if err := cfg.Validate(); err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	cfg.Server.MetricsAddr = cfg.Alert.MetricsAddr

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

	tcfg := tenant.ConfigFromEnv(cfg)
	if err := tcfg.Validate(); err != nil {
		slog.Error("invalid tenant configuration", "error", err)
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

	worker := controllers.New(cfg, red, nac, controllers.NewPostgresStore(tm))
	sub, err := worker.Start()
	if err != nil {
		slog.Error("failed to subscribe telemetry.raw.>", "error", err)
		os.Exit(1)
	}
	defer nac.Unsubscribe(sub)

	hs := internal.NewHealthServer(cfg.Server.MetricsAddr, registry,
		internal.HealthCheck{Name: "postgres", Critical: true,
			Fn: func(ctx context.Context) error { return worker.Readiness(ctx) }},
		internal.HealthCheck{Name: "redis", Critical: true,
			Fn: func(ctx context.Context) error { return red.Ping(ctx) }},
		internal.HealthCheck{Name: "nats", Critical: true, Fn: func(context.Context) error {
			if !nac.IsConnected() {
				return errNATS
			}
			return nil
		}},
	)
	hs.Start()
	defer hs.Shutdown(context.Background())

	slog.Info("worker-alert started",
		"dedup_window_s", cfg.Alert.DedupWindow.Seconds(),
		"offline_after_min", cfg.Alert.OfflineAfterMinutes,
		"battery_low_percent", cfg.Alert.BatteryLowPercent,
		"route_deviation_m", cfg.Alert.RouteDeviationThresholdM,
		"sos_escalation_min", cfg.Alert.SOSEscalationMinutes,
		"sos_escalation_max", cfg.Alert.SOSEscalationMax)

	<-ctx.Done()
	slog.Info("shutdown signal received; stopping worker-alert")
	worker.Stop()
	slog.Info("worker-alert stopped")
}
