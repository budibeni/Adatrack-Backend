// Command service-media serves the dashcam EVENT MEDIA API of ADATRACK
// (PRD Module 8 / Scope A, phase B5b):
//
//	HTTP   : /api/v1/media[/...] + /healthz + /metrics on MEDIA_HTTP_ADDR (:8095)
//	Ingest : HMAC-SHA256 per company (X-Signature) — multipart OR JSON + presigned PUT
//	Catalog: th_media_events per tenant (lifecycle pending → complete → expired/deleted)
//	Storage: internal/storage (MinIO dev / S3-OSS prod) with presigned GET (FR-8.4)
//	WS     : publishes media.event.<company_code> → service-websocket MEDIA_EVENT
//	Retain : MEDIA_CLEANUP_CRON sweep deletes objects past retention (FR-8.7)
//
// Live video streaming (WebRTC/RTSP) is OUT OF SCOPE for this phase.
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
	"adatrack_gps/internal/storage"
	"adatrack_gps/internal/tenant"
	"adatrack_gps/service-media/controllers"
)

func main() {
	internal.ConfigureLogging()
	internal.LoadProjectEnv()

	cfg := internal.LoadConfig()
	if err := cfg.Validate(); err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	settings := controllers.LoadSettings(cfg)
	if err := settings.Validate(); err != nil {
		slog.Error("invalid service-media configuration", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	registry := internal.GetRegistry()
	controllers.RegisterMetrics(registry)

	store, err := storage.New(storage.Options{
		Backend:   cfg.Media.Backend,
		Endpoint:  cfg.Media.S3Endpoint,
		Region:    cfg.Media.S3Region,
		Bucket:    cfg.Media.S3Bucket,
		AccessKey: cfg.Media.S3AccessKey,
		SecretKey: cfg.Media.S3SecretKey,
		UseSSL:    cfg.Media.S3UseSSL,
	})
	if err != nil {
		slog.Error("object storage unavailable", "backend", cfg.Media.Backend, "error", err)
		os.Exit(1)
	}
	// Bucket bootstrap is idempotent (FR-8.1 "bucket + policy"): a failure is not
	// fatal — /healthz reports the storage check and every write fails loudly.
	if s3store, ok := store.(*storage.S3Store); ok {
		bootCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		if err := s3store.EnsureBucket(bootCtx); err != nil {
			slog.Warn("could not ensure the media bucket (healthz will report it)", "error", err)
		}
		cancel()
	}

	tcfg := tenant.ConfigFromEnv(cfg)
	if err := tcfg.Validate(); err != nil {
		slog.Error("invalid tenant configuration", "error", err,
			"env", "MASTER_DB_NAME/COMPANY_DB_PREFIX/COMPANY_MIGRATIONS_DIR")
		os.Exit(1)
	}
	// Redis is a CRITICAL dependency: JWT revocation (FR-5.7), the ingest/API rate
	// limiters and the /healthz readiness check all read through it. Booting
	// without it would silently disable those protections (audit finding
	// 2026-09-22), so a missing Redis is fail-fast.
	red, err := internal.NewRedisClient(cfg)
	if err != nil {
		slog.Error("redis unavailable", "error", err)
		os.Exit(1)
	}
	defer func() { _ = red.Close() }()

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

	service := controllers.NewService(controllers.Deps{
		Settings: settings,
		Store:    controllers.NewPostgresStore(tm),
		Storage:  store,
		KV:       controllers.NewKV(red),
		NATS:     nac,
		Registry: registry,
	})
	service.Start(ctx)
	defer service.Stop()

	server := &http.Server{
		Addr:              settings.HTTPAddr,
		Handler:           service.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		slog.Info("service-media listening", "addr", settings.HTTPAddr,
			"metrics_addr", settings.MetricsAddr,
			"backend", cfg.Media.Backend, "bucket", cfg.Media.S3Bucket,
			"max_file_mb", settings.MaxFileMB, "retention_days", settings.RetentionDays,
			"cleanup_cron", settings.CleanupCron,
			// Protection state is logged explicitly: after the 2026-09-22 audit
			// (Redis was not wired) an operator can confirm at boot that the
			// revocation denylist and both rate limiters are active.
			"revocation", settings.RevocationEnabled,
			"ingest_rate_limit_per_min", settings.IngestRateLimit,
			"api_rate_limit_per_min", settings.APIRateLimit,
			"audit", settings.AuditEnabled)
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
	slog.Info("service-media stopped")
}
