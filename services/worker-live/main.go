// Command worker-live maintains the Redis live state and the real-time fan-out
// for service-websocket (PRD Module 2):
//
//	telemetry.raw.> --(queue group "live")--> Redis live state
//	                                        --> NATS telemetry.live.<IMEI>
//
// Behaviour:
//   - batch writes: buffer + MSET every LIVE_BATCH_INTERVAL_MS (FR-2.3)
//   - status machine: ONLINE / IDLE / OFFLINE with an active staleness sweeper
//     (FR-2.2, OFFLINE_AFTER_MINUTES)
//   - partial fuel messages merge into the existing state without touching
//     position/speed (FR-2.3 / B5a scaffolding)
//   - fleet accumulators: Haversine odometer + ACC-gated engine hours (FR-2.5)
//     and the MOVING↔STOPPED trip/stop machine persisted every 30 s (FR-2.6)
//   - readiness: /healthz + /metrics on LIVE_METRICS_ADDR
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"adatrack_gps/internal"
	"adatrack_gps/internal/tenant"
	"adatrack_gps/worker-live/controllers"
)

func main() {
	internal.ConfigureLogging()
	internal.LoadProjectEnv()

	cfg := internal.LoadConfig()
	if err := cfg.Validate(); err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	cfg.Server.MetricsAddr = internal.EnvOr("LIVE_METRICS_ADDR", ":8091")

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

	store := controllers.NewPostgresFleetStore(tm)
	worker := controllers.New(cfg, red, nac, store)
	sub, err := worker.Start()
	if err != nil {
		slog.Error("failed to subscribe telemetry.raw.>", "error", err)
		os.Exit(1)
	}
	defer nac.Unsubscribe(sub)

	hs := internal.NewHealthServer(cfg.Server.MetricsAddr, registry,
		internal.HealthCheck{Name: "redis", Critical: true,
			Fn: func(ctx context.Context) error { return red.Ping(ctx) }},
		internal.HealthCheck{Name: "postgres", Critical: true,
			Fn: func(ctx context.Context) error { return store.Readiness(ctx) }},
		internal.HealthCheck{Name: "nats", Critical: true, Fn: func(context.Context) error {
			if !nac.IsConnected() {
				return errNATS
			}
			return nil
		}},
	)
	hs.Start()
	defer hs.Shutdown(context.Background())

	slog.Info("worker-live started", "batch_interval_ms", cfg.Live.BatchInterval.Milliseconds(),
		"offline_after_min", cfg.Live.OfflineAfterMinutes,
		"idle_after_s", cfg.Live.IdleAfter.Seconds(), "state_ttl_s", cfg.Redis.TTL.Seconds(),
		"fleet_flush_s", cfg.Fleet.FlushEvery.Seconds(), "fleet_flush_batch", cfg.Fleet.FlushBatch,
		"odometer_max_jump_km", cfg.Fleet.MaxJumpKM,
		"trip_stop_grace_s", cfg.Fleet.StopGrace.Seconds(),
		"trip_min_stop_s", cfg.Fleet.MinStop.Seconds(),
		"trip_max_stop_s", cfg.Fleet.MaxStop.Seconds())

	<-ctx.Done()
	slog.Info("shutdown signal received; draining live-state buffer")
	worker.Stop()
	slog.Info("worker-live stopped")
}
