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
	"ajb_gps/internal/tenant"
	"ajb_gps/worker-persistence/controllers"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	internal.ConfigureLogging()
	internal.LoadProjectEnv()

	cfg := internal.LoadConfig()
	if err := cfg.Validate(); err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	cfg.Server.Addr = internal.EnvOr("PERSISTENCE_METRICS_ADDR", ":8091")

	registry := getRegistry()

	// TenantManager menyediakan dynamic DB routing: company_code → company pool
	// (PRD §6.2). Master + default + semua company pool di-pre-warm di startup.
	tcfg := tenant.NewConfigFromEnv()
	if err := tcfg.Validate(); err != nil {
		slog.Error("invalid tenant configuration", "error", err, "env", "MASTER_DB_*/COMPANY_DB_PREFIX")
		os.Exit(1)
	}
	tm, err := tenant.New(context.Background(), tcfg, nil, registry)
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

	controllers.Configure(tm, nac)
	controllers.RegisterMetrics(registry)

	go serveMetrics(cfg, registry, tm, nac)

	sub, err := controllers.Start()
	if err != nil {
		slog.Error("failed to subscribe to nats", "error", err)
		os.Exit(1)
	}
	defer nac.Unsubscribe(sub)

	slog.Info("worker-persistence started")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	slog.Info("shutdown signal received, draining batch...")

	controllers.Stop()
	slog.Info("worker-persistence stopped")
}

// getRegistry registers adatrack metrics plus Go runtime collectors.
func getRegistry() *prometheus.Registry {
	r := internal.GetRegistry()
	r.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	r.MustRegister(prometheus.NewGoCollector())
	return r
}

// serveMetrics starts the /healthz and /metrics HTTP endpoints.
// /healthz = readiness: master + default + all company DB pools ping, NATS ok.
func serveMetrics(cfg *internal.Config, r *prometheus.Registry, tm *tenant.Manager, nac *internal.NATSClient) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		code := http.StatusOK
		body := "ok"
		if err := tm.Health(ctx); err != nil {
			code = http.StatusServiceUnavailable
			body = "mysql:" + err.Error()
		} else if nac != nil && !nac.IsConnected() {
			code = http.StatusServiceUnavailable
			body = "nats:disconnected"
		}
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	})
	mux.Handle("/metrics", promhttp.HandlerFor(r, promhttp.HandlerOpts{}))

	srv := &http.Server{Addr: cfg.Server.Addr, Handler: mux}
	slog.Info("metrics server listening", "addr", cfg.Server.Addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("metrics server failed", "error", err)
	}
}
