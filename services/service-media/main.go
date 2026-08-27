package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ajb_gps/internal"
	"ajb_gps/internal/storage"
	"ajb_gps/internal/tenant"
	"ajb_gps/service-media/controllers"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// ---------------------------------------------------------------------------
// service-media (Phase B5b, PRD v1.3.0 Module 8) — Dashcam event media, Scope A.
//
// REST (Gin):
//   - POST /api/v1/media/events   (HMAC-SHA256 per company, FR-8.1; multipart &
//     JSON-presigned-PUT flows)
//   - GET  /api/v1/media           (RBAC list, FR-8.4)
//   - GET  /api/v1/media/:id       (RBAC detail)
//   - GET  /api/v1/media/:id/url   (short-TTL presigned URL + audit)
//   - DELETE /api/v1/media/:id     (soft-delete, Admin only)
// Object storage via internal/storage (S3-compatible: MinIO / AWS S3 / OSS).
// Publish media.event.<company> to NATS (FR-8.5); daily retention (FR-8.7).
// ---------------------------------------------------------------------------

func main() {
	internal.ConfigureLogging()
	internal.LoadProjectEnv()

	cfg := internal.LoadConfig()
	if err := cfg.Validate(); err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	cfg.HTTP.Addr = internal.EnvOr("SERVICE_MEDIA_HTTP_ADDR", ":8095")

	metricsReg := getRegistry()
	// Register service-media metrics (media_*, storage_objects) on the SAME
	// registry that backs the /metrics handler (FR-8.8). internal.GetRegistry()
	// creates a fresh registry per call, so this must use metricsReg directly.
	controllers.RegisterMediaMetrics(metricsReg)
	metricsHTTP := promhttp.HandlerFor(metricsReg, promhttp.HandlerOpts{})

	redis, err := internal.NewRedisClient(cfg, internal.RedisOperations, internal.RedisOperationDuration)
	if err != nil {
		slog.Error("failed to init redis", "error", err)
		os.Exit(1)
	}
	defer redis.Close()

	tcfg := tenant.NewConfigFromEnv()
	if err := tcfg.Validate(); err != nil {
		slog.Error("invalid tenant configuration", "error", err, "env", "MASTER_DB_*/COMPANY_DB_PREFIX")
		os.Exit(1)
	}
	tm, err := tenant.New(context.Background(), tcfg, redis.Client(), metricsReg)
	if err != nil {
		slog.Error("failed to init tenant manager", "error", err)
		os.Exit(1)
	}
	go tm.Run(context.Background())

	// Object store (FR-8.2): S3-compatible via internal/storage.
	st, err := buildStore(cfg)
	if err != nil {
		slog.Error("failed to init object store", "error", err)
		os.Exit(1)
	}
	ctx0, cancel0 := context.WithTimeout(context.Background(), 10*time.Second)
	if err := st.Ping(ctx0); err != nil {
		slog.Warn("object store not reachable at boot", "error", err)
	}
	cancel0()

	nats, err := internal.NewNATSClient(cfg,
		internal.NATSMessagesPublished, internal.NATSMessagesConsumed, internal.NATPendingMessages)
	if err != nil {
		slog.Error("failed to init nats", "error", err)
		os.Exit(1)
	}
	defer nats.Close()

	controllers.Init(cfg, redis, tm, st, nats, metricsHTTP)

	rerunCtx, rerunCancel := context.WithCancel(context.Background())
	runner := controllers.NewRetentionRunner(cfg, tm, st)
	runner.Start(rerunCtx)

	srv := &http.Server{
		Addr:         cfg.HTTP.Addr,
		Handler:      controllers.Router(),
		ReadTimeout:  cfg.HTTP.ReadTimeout,
		WriteTimeout: cfg.HTTP.WriteTimeout,
	}
	go func() {
		if cfg.HTTP.TLS.Enabled {
			slog.Info("service-media https listening (TLS)", "addr", cfg.HTTP.Addr)
			if err := srv.ListenAndServeTLS(cfg.HTTP.TLS.CertFile, cfg.HTTP.TLS.KeyFile); err != nil && err != http.ErrServerClosed {
				slog.Error("https server failed", "error", err)
				os.Exit(1)
			}
		} else {
			slog.Info("service-media http listening", "addr", cfg.HTTP.Addr)
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				slog.Error("http server failed", "error", err)
				os.Exit(1)
			}
		}
	}()

	// Metrics/health on the same HTTP server (Router exposes both).
	go serveMetrics(cfg, metricsHTTP)

	slog.Info("service-media started")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	slog.Info("shutdown signal received")

	rerunCancel()
	shCtx, scancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer scancel()
	if err := srv.Shutdown(shCtx); err != nil {
		slog.Warn("http shutdown error", "error", err)
	}
	controllers.Shutdown()
	slog.Info("service-media stopped")
}

// buildStore constructs the S3-compatible object store from config.
func buildStore(cfg *internal.Config) (storage.Store, error) {
	s3cfg := storage.S3Config{
		Endpoint:  cfg.Media.S3Endpoint,
		Bucket:    cfg.Media.S3Bucket,
		AccessKey: cfg.Media.S3AccessKey,
		SecretKey: cfg.Media.S3SecretKey,
		UseSSL:    cfg.Media.S3UseSSL,
		Region:    cfg.Media.S3Region,
	}
	st, err := storage.NewS3Store(s3cfg, nil)
	if err != nil {
		return nil, err
	}
	if cfg.Media.S3Bucket != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := st.EnsureBucket(ctx, cfg.Media.S3Bucket); err != nil {
			slog.Warn("unable to ensure default bucket", "bucket", cfg.Media.S3Bucket, "error", err)
		}
	}
	return st, nil
}

// getRegistry registers adatrack + Go runtime metrics (shared with media).
func getRegistry() *prometheus.Registry {
	r := internal.GetRegistry()
	r.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	r.MustRegister(prometheus.NewGoCollector())
	return r
}

func serveMetrics(cfg *internal.Config, metrics http.Handler) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", metrics)
	srv := &http.Server{Addr: cfg.HTTP.MetricsAddr, Handler: mux}
	slog.Info("metrics server listening", "addr", cfg.HTTP.MetricsAddr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("metrics server failed", "error", err)
	}
}
