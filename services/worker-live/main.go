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
	"ajb_gps/worker-live/controllers"

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
	cfg.Server.Addr = internal.EnvOr("LIVE_METRICS_ADDR", ":8092")

	registry := getRegistry()

	red, err := internal.NewRedisClient(cfg, internal.RedisOperations, internal.RedisOperationDuration)
	if err != nil {
		slog.Error("failed to init redis", "error", err)
		os.Exit(1)
	}
	defer red.Close()

	nac, err := internal.NewNATSClient(cfg,
		internal.NATSMessagesPublished, internal.NATSMessagesConsumed, internal.NATPendingMessages)
	if err != nil {
		slog.Error("failed to init nats", "error", err)
		os.Exit(1)
	}
	defer nac.Close()

	controllers.Configure(red, nac)
	controllers.RegisterMetrics(registry)

	go serveMetrics(cfg, registry, red, nac)

	sub, err := controllers.Start()
	if err != nil {
		slog.Error("failed to subscribe to nats", "error", err)
		os.Exit(1)
	}
	defer nac.Unsubscribe(sub)

	slog.Info("worker-live started")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	slog.Info("shutdown signal received, flushing buffer...")

	controllers.Stop()
}

// getRegistry registers adatrack metrics plus Go runtime collectors.
func getRegistry() *prometheus.Registry {
	r := internal.GetRegistry()
	r.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	r.MustRegister(prometheus.NewGoCollector())
	return r
}

// serveMetrics starts the /healthz and /metrics HTTP endpoints.
// /healthz = readiness: Redis ping, NATS connected.
func serveMetrics(cfg *internal.Config, r *prometheus.Registry, red *internal.RedisClient, nac *internal.NATSClient) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		code := http.StatusOK
		body := "ok"
		if red != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := red.Ping(ctx); err != nil {
				code = http.StatusServiceUnavailable
				body = "redis:" + err.Error()
			}
		}
		if code == http.StatusOK && nac != nil && !nac.IsConnected() {
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
