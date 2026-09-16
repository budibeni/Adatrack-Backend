package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"adatrack/internal/config"
	"adatrack/internal/dbclient"
	"adatrack/internal/logger"
	"adatrack/internal/natsclient"
	"adatrack/internal/redclient"
)

func main() {
	logger.InitLogger()
	cfg := config.Load()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Robust Bootstrapping
	if err := dbclient.Connect(ctx, cfg); err != nil {
		logger.Log.Error("FATAL: Failed to connect DB", "error", err)
		os.Exit(1)
	}
	logger.Log.Info("Database Connected")

	if err := redclient.Connect(ctx, cfg); err != nil {
		logger.Log.Error("FATAL: Failed to connect Redis", "error", err)
		os.Exit(1)
	}
	logger.Log.Info("Redis Connected")

	if err := natsclient.Connect(cfg); err != nil {
		logger.Log.Error("FATAL: Failed to connect NATS", "error", err)
		os.Exit(1)
	}
	logger.Log.Info("NATS Connected")

	if err := natsclient.ProvisionStreams(); err != nil {
		logger.Log.Error("FATAL: Failed to provision NATS streams", "error", err)
		os.Exit(1)
	}

	// Setup Server
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("# HELP active_connections active connections\nactive_connections 1\n"))
	})

	server := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	// Graceful Shutdown
	go func() {
		logger.Log.Info("Starting foundation-check server on :8080")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Log.Error("HTTP Server Error", "error", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Log.Info("Shutting down gracefully...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Log.Error("Server forced to shutdown", "error", err)
	}

	if dbclient.Pool != nil {
		dbclient.Pool.Close()
	}
	if natsclient.NC != nil {
		natsclient.NC.Close()
	}
	logger.Log.Info("Shutdown complete")
}
