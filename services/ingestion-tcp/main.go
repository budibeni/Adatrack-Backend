package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ajb_gps/ingestion-tcp/controllers"
	"ajb_gps/ingestion-tcp/models"
	"ajb_gps/internal"
	"ajb_gps/internal/tenant"

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
	cfg.Server.Addr = internal.EnvOr("INGESTION_METRICS_ADDR", ":8090")

	// GT06 date encoding toggle: docs use plain-hex (byte value == decimal);
	// set GT06_DATE_BCD=true to decode/encode BCD dates for firmwares that use it.
	controllers.SetDateEncoding(envBool("GT06_DATE_BCD"))

	registry := getRegistry()

	// TenantManager: master DB (IMEI→company lookup, anti-spoofing) + pre-warmed
	// company DB pools. Redis dipakai sebagai cache lookup IMEI (opsional).
	tcfg := tenant.NewConfigFromEnv()
	if err := tcfg.Validate(); err != nil {
		slog.Error("invalid tenant configuration", "error", err, "env", "MASTER_DB_*/COMPANY_DB_PREFIX")
		os.Exit(1)
	}
	var cache tenant.Cache
	red, err := internal.NewRedisClient(cfg, internal.RedisOperations, internal.RedisOperationDuration)
	if err != nil {
		slog.Warn("redis unavailable; IMEI lookup cache disabled", "error", err)
	} else {
		cache = red.Client()
		defer red.Close()
	}

	tm, err := tenant.New(context.Background(), tcfg, cache, registry)
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

	controllers.Configure(tm, nac, cfg.TCP.MaxConnections)
	controllers.RegisterMetrics(registry)

	go serveMetrics(cfg, registry, tm, nac)

	// Start one TCP listener per protocol (dedicated ports).
	type target struct {
		addr  string
		proto models.Protocol
	}
	var targets []target
	if cfg.TCP.Port != "" && cfg.TCP.Port != "0" {
		targets = append(targets, target{":" + cfg.TCP.Port, models.ProtoGT06})
	}
	if cfg.TCP.TeltonikaPort != "" && cfg.TCP.TeltonikaPort != "0" {
		targets = append(targets, target{":" + cfg.TCP.TeltonikaPort, models.ProtoTeltonika})
	}
	if cfg.TCP.TK103Port != "" && cfg.TCP.TK103Port != "0" {
		targets = append(targets, target{":" + cfg.TCP.TK103Port, models.ProtoTK103})
	}
	if len(targets) == 0 {
		slog.Error("no TCP listener configured (set TCP_PORT)")
		os.Exit(1)
	}

	acceptCh := make(chan net.Conn, 64)
	for _, t := range targets {
		ln, err := net.Listen("tcp", t.addr)
		if err != nil {
			slog.Error("failed to listen", "error", err, "addr", t.addr, "protocol", t.proto.String())
			os.Exit(1)
		}
		defer ln.Close()
		go controllers.AcceptLoop(ln, acceptCh, t.proto)
		slog.Info("ingestion listener started", "addr", t.addr, "protocol", t.proto.String(),
			"max_conns", cfg.TCP.MaxConnections)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	slog.Info("shutdown signal received")

	controllers.Cancel()
	slog.Info("ingestion-tcp stopped")
}

// envBool reads a boolean env var, defaulting to false.
func envBool(key string) bool {
	switch os.Getenv(key) {
	case "1", "true", "TRUE", "yes", "YES", "t", "on":
		return true
	}
	return false
}

// getRegistry registers adatrack + ingestion metrics and Go runtime collectors.
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
