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
//   - readiness: /healthz + /metrics on LIVE_METRICS_ADDR
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"adatrack_gps/internal"
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

	nac, err := internal.NewNATSClient(cfg)
	if err != nil {
		slog.Error("nats unavailable", "error", err)
		os.Exit(1)
	}
	defer nac.Close()

	worker := controllers.New(cfg, red, nac)
	sub, err := worker.Start()
	if err != nil {
		slog.Error("failed to subscribe telemetry.raw.>", "error", err)
		os.Exit(1)
	}
	defer nac.Unsubscribe(sub)

	hs := internal.NewHealthServer(cfg.Server.MetricsAddr, registry,
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

	slog.Info("worker-live started", "batch_interval_ms", cfg.Live.BatchInterval.Milliseconds(),
		"offline_after_min", cfg.Live.OfflineAfterMinutes,
		"idle_after_s", cfg.Live.IdleAfter.Seconds(), "state_ttl_s", cfg.Redis.TTL.Seconds())

	<-ctx.Done()
	slog.Info("shutdown signal received; draining live-state buffer")
	worker.Stop()
	slog.Info("worker-live stopped")
}
