// Command foundation-check boots the full B0 foundation end-to-end and proves the
// wiring (B0 task "Satu service minimal ter-boot end-to-end sebagai bukti wiring"):
//
//   - PostgreSQL  : master pool + auto-migration (MIGRATE_ON_BOOT, ledger §14.5)
//   - Redis       : ping + live-state write/read/delete round trip
//   - NATS        : JetStream streams created, publish → consume round trip
//   - Multi-tenant: tenant registry + IMEI resolution (anti-spoofing path)
//   - HTTP        : /healthz (readiness) + /metrics (Prometheus)
//
// It is a diagnostic service (no business logic): run it to verify a fresh
// environment before starting the pipeline services (B1).
//
// Usage:
//
//	go run ./services/foundation-check          # continuous (serves /healthz)
//	go run ./services/foundation-check -once    # one round trip, exit 0/1
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"ajb_gps/internal"
	"ajb_gps/internal/tenant"
)

func main() {
	internal.ConfigureLogging()
	internal.LoadProjectEnv()

	once := flag.Bool("once", false, "run a single wiring check and exit")
	flag.Parse()

	cfg := internal.LoadConfig()
	if err := cfg.Validate(); err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	cfg.Server.MetricsAddr = internal.EnvOr("FOUNDATION_METRICS_ADDR", ":8093")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	registry := internal.GetRegistry()

	// --- PostgreSQL (master) -------------------------------------------------
	pool, err := internal.OpenPostgresPool(cfg, cfg.Migrate.MasterSchema, "master")
	if err != nil {
		slog.Error("postgres unavailable", "error", err)
		os.Exit(1)
	}
	defer func() { _ = pool.Close() }()
	slog.Info("postgres connected", "schema", cfg.Migrate.MasterSchema)

	if cfg.Migrate.OnBoot {
		res, migErr := internal.ApplyMigrations(ctx, pool.DB, "master", cfg.Migrate.MasterSchema,
			cfg.Migrate.MasterDir, cfg.Migrate.LedgerTable, cfg.Migrate.LockTimeout)
		if migErr != nil {
			slog.Error("auto-migration failed", "error", migErr)
			os.Exit(1)
		}
		slog.Info("auto-migration ok", "applied", res.Applied, "skipped", res.Skipped,
			"duration_ms", res.Duration.Milliseconds())
	}

	// --- Redis ---------------------------------------------------------------
	red, err := internal.NewRedisClient(cfg)
	if err != nil {
		slog.Error("redis unavailable", "error", err)
		os.Exit(1)
	}
	defer func() { _ = red.Close() }()
	slog.Info("redis connected", "addr", cfg.RedisAddr())

	// --- NATS ----------------------------------------------------------------
	nac, err := internal.NewNATSClient(cfg)
	if err != nil {
		slog.Error("nats unavailable", "error", err)
		os.Exit(1)
	}
	defer nac.Close()
	slog.Info("nats connected", "url", cfg.NATS.URL, "subject_prefix", cfg.NATS.SubjectPrefix)

	// --- Tenant manager (routing + pre-warmed company pools) -----------------
	tm, err := tenant.New(ctx, cfg, tenant.ConfigFromEnv(cfg), red, registry)
	if err != nil {
		slog.Error("tenant manager failed", "error", err)
		os.Exit(1)
	}
	defer tm.Close()
	go tm.Run(ctx)

	// --- Round-trip checks ---------------------------------------------------
	if !reportChecks(runChecks(ctx, cfg, red, nac, tm)) {
		slog.Error("B0 foundation wiring check FAILED")
		os.Exit(1)
	}
	slog.Info("B0 foundation wiring check PASSED (postgres + redis + nats + tenant)")

	if *once {
		return
	}

	hs := internal.NewHealthServer(cfg.Server.MetricsAddr, registry, healthChecks(cfg, pool, red, nac, tm)...)
	hs.Start()
	defer hs.Shutdown(context.Background())

	<-ctx.Done()
	slog.Info("shutdown signal received")
}
