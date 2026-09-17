// Command ingestion-tcp is the device-facing entry point of the telemetry
// pipeline (PRD Module 1):
//
//	GPS device --TCP--> decode (GT06 / Teltonika) --> NATS telemetry.raw.<IMEI>
//
// Responsibilities:
//   - one listener per protocol (GT06 on TCP_PORT, Teltonika on TELTONIKA_TCP_PORT;
//     boot refuses a port clash → os.Exit(1), PRD Module 1c)
//   - connection management: max connections, idle timeout, per-connection budget
//   - anti-spoofing: IMEI allowlist through master.tm_vehicle_imei_map (FR-1.4)
//   - backpressure: warn >50% / drop >90% of the JetStream budget (FR-1.5)
//   - readiness: /healthz + /metrics on INGESTION_METRICS_ADDR
package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"adatrack_gps/ingestion-tcp/controllers"
	"adatrack_gps/ingestion-tcp/models"
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
	cfg.Server.MetricsAddr = internal.EnvOr("INGESTION_METRICS_ADDR", ":8090")

	// GT06 date encoding toggle (docs use plain-hex; some firmwares use BCD).
	controllers.SetDateEncoding(cfg.TCP.DateBCD)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	registry := internal.GetRegistry()
	controllers.RegisterMetrics(registry)

	// --- PostgreSQL (master + company pools, IMEI resolution) ----------------
	tcfg := tenant.ConfigFromEnv(cfg)
	if err := tcfg.Validate(); err != nil {
		slog.Error("invalid tenant configuration", "error", err,
			"env", "MASTER_DB_NAME/COMPANY_DB_PREFIX/COMPANY_MIGRATIONS_DIR")
		os.Exit(1)
	}

	// Redis is optional here: it only accelerates IMEI lookups.
	var cache tenant.Cache
	red, err := internal.NewRedisClient(cfg)
	if err != nil {
		slog.Warn("redis unavailable; IMEI lookup cache disabled", "error", err)
	} else {
		cache = red
		defer func() { _ = red.Close() }()
	}

	tm, err := tenant.New(ctx, cfg, tcfg, cache, registry)
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

	// --- NATS ----------------------------------------------------------------
	nac, err := internal.NewNATSClient(cfg)
	if err != nil {
		slog.Error("nats unavailable", "error", err)
		os.Exit(1)
	}
	defer nac.Close()

	// --- Listeners -----------------------------------------------------------
	srv := controllers.NewServer(cfg, tm, nac)
	defer srv.Shutdown()

	targets := []struct {
		port  string
		proto models.Protocol
	}{
		{cfg.TCP.Port, models.ProtoGT06},
		{cfg.TCP.TeltonikaPort, models.ProtoTeltonika},
	}

	seen := map[string]string{}
	listeners := 0
	for _, t := range targets {
		if t.port == "" || t.port == "0" {
			slog.Info("listener disabled by configuration", "protocol", t.proto.String(), "port", t.port)
			continue
		}
		if other, clash := seen[t.port]; clash {
			slog.Error("port clash between listeners", "port", t.port,
				"protocols", other+" + "+t.proto.String())
			os.Exit(1)
		}
		seen[t.port] = t.proto.String()

		ln, err := net.Listen("tcp", ":"+t.port)
		if err != nil {
			slog.Error("failed to listen", "port", t.port, "protocol", t.proto.String(), "error", err)
			os.Exit(1)
		}
		defer func() { _ = ln.Close() }()
		go srv.AcceptLoop(ln, t.proto)
		listeners++
		slog.Info("ingestion listener started", "addr", ":"+t.port, "protocol", t.proto.String(),
			"max_connections", cfg.TCP.MaxConnections, "idle_timeout_s", cfg.TCP.IdleTimeout.Seconds())
	}
	if listeners == 0 {
		slog.Error("no TCP listener configured (set TCP_PORT and/or TELTONIKA_TCP_PORT)")
		os.Exit(1)
	}

	// --- Readiness + metrics -------------------------------------------------
	hs := internal.NewHealthServer(cfg.Server.MetricsAddr, registry,
		internal.HealthCheck{Name: "postgres_master", Critical: true,
			Fn: func(ctx context.Context) error { return tm.Master().Ping(ctx) }},
		internal.HealthCheck{Name: "tenant_pools", Critical: false,
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

	slog.Info("ingestion-tcp started", "listeners", listeners)

	<-ctx.Done()
	slog.Info("shutdown signal received; stopping listeners")
	srv.Shutdown()
	slog.Info("ingestion-tcp stopped", "dropped_frames", srv.DroppedFrames())
}
