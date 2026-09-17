// Command worker-persistence batches telemetry and writes it to PostgreSQL
// (PRD Module 3):
//
//	telemetry.raw.> --(queue group "persistence")--> buffer --> th_telemetry_logs
//
// Behaviour:
//   - batched inserts: BATCH_SIZE rows or BATCH_TIMEOUT_SEC, whichever first
//     (FR-3.1), one INSERT per tenant schema (FR-3.4)
//   - retry with exponential backoff on transient errors; exhausted retries are
//     published to telemetry.error.<IMEI> (dead-letter, no silent drop)
//   - heartbeat/fuel-only packets skip th_telemetry_logs (positionless)
//   - graceful shutdown drains the final batch
//   - readiness: /healthz + /metrics on PERSISTENCE_METRICS_ADDR
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"adatrack_gps/internal"
	"adatrack_gps/internal/tenant"
	"adatrack_gps/worker-persistence/controllers"
)

func main() {
	internal.ConfigureLogging()
	internal.LoadProjectEnv()

	cfg := internal.LoadConfig()
	if err := cfg.Validate(); err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	cfg.Server.MetricsAddr = internal.EnvOr("PERSISTENCE_METRICS_ADDR", ":8092")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	registry := internal.GetRegistry()
	controllers.RegisterMetrics(registry)

	tcfg := tenant.ConfigFromEnv(cfg)
	if err := tcfg.Validate(); err != nil {
		slog.Error("invalid tenant configuration", "error", err,
			"env", "MASTER_DB_NAME/COMPANY_DB_PREFIX/COMPANY_MIGRATIONS_DIR")
		os.Exit(1)
	}

	tm, err := tenant.New(ctx, cfg, tcfg, nil, registry)
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

	persister := controllers.New(cfg, tm, nac)
	sub, err := persister.Start()
	if err != nil {
		slog.Error("failed to subscribe telemetry.raw.>", "error", err)
		os.Exit(1)
	}
	defer nac.Unsubscribe(sub)

	hs := internal.NewHealthServer(cfg.Server.MetricsAddr, registry,
		internal.HealthCheck{Name: "postgres_master", Critical: true,
			Fn: func(ctx context.Context) error { return tm.Master().Ping(ctx) }},
		internal.HealthCheck{Name: "tenant_pools", Critical: true,
			Fn: func(ctx context.Context) error { return tm.Health(ctx) }},
		internal.HealthCheck{Name: "nats", Critical: true, Fn: func(context.Context) error {
			if !nac.IsConnected() {
				return errNATS
			}
			return nil
		}},
	)
	hs.Start()
	defer hs.Shutdown(context.Background())

	slog.Info("worker-persistence started",
		"batch_size", cfg.Persistence.BatchSize,
		"batch_timeout_s", cfg.Persistence.BatchTimeout.Seconds(),
		"retry_max", cfg.Persistence.RetryMax,
		"backoff", cfg.Persistence.Backoff)

	<-ctx.Done()
	slog.Info("shutdown signal received; draining the final batch")
	persister.Stop()
	slog.Info("worker-persistence stopped")
}
