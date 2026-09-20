package main

import (
	"context"
	"net/http"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"os"
	"os/signal"
	"syscall"
	"time"

	"backend/internal/config"
	"backend/internal/dbclient"
	"backend/internal/logger"
	"backend/internal/natsclient"
	"backend/internal/redclient"
	"backend/internal/tenant"
)

func main() {
	logger.InitLogger()
	cfg := config.Load()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := dbclient.Connect(ctx, cfg); err != nil {
		logger.Log.Error("FATAL DB", "err", err); os.Exit(1)
	}
	if err := redclient.Connect(ctx, cfg); err != nil {
		logger.Log.Error("FATAL Redis", "err", err); os.Exit(1)
	}
	tenant.InitManager(cfg)
	if err := natsclient.Connect(cfg); err != nil {
		logger.Log.Error("FATAL NATS", "err", err); os.Exit(1)
	}
	if err := natsclient.ProvisionStreams(cfg); err != nil {
		logger.Log.Error("FATAL Stream", "err", err); os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
	mux.Handle("/metrics", promhttp.Handler())
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("# HELP active_connections active connections\nactive_connections 1\n"))
	})
	server := &http.Server{Addr: ":8080", Handler: mux}

	go func() {
		logger.Log.Info("Foundation server started on :8080")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Log.Error("Server error", "err", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Log.Info("Graceful Shutdown...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	server.Shutdown(shutdownCtx)

	if dbclient.Pool != nil { dbclient.Pool.Close() }
	if natsclient.NC != nil { natsclient.NC.Close() }
	logger.Log.Info("Shutdown complete")
}
