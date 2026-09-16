package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"backend/ingestion-tcp/internal/server"
	"backend/internal/config"
	"backend/internal/dbclient"
	"backend/internal/logger"
	"backend/internal/natsclient"
)

func main() {
	logger.InitLogger()
	cfg := config.Load()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := dbclient.Connect(ctx, cfg); err != nil {
		logger.Log.Error("FATAL DB", "err", err); os.Exit(1)
	}
	if err := natsclient.Connect(cfg); err != nil {
		logger.Log.Error("FATAL NATS", "err", err); os.Exit(1)
	}
	// Note: NATS streams provisioning is handled by migration or init script, 
	// but can be safely called here (idempotent).
	natsclient.ProvisionStreams()

	// Metrics/Health Server
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	healthServer := &http.Server{Addr: ":8081", Handler: mux}
	go healthServer.ListenAndServe()

	tcpServer := server.NewTCPServer(":9000", 5000) // Support 5000 concurrent devices per replica
	go func() {
		if err := tcpServer.Start(); err != nil {
			logger.Log.Error("TCP Server crashed", "err", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Log.Info("Initiating Graceful Shutdown...")
	tcpServer.Stop()
	
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	healthServer.Shutdown(shutdownCtx)
	
	dbclient.Pool.Close()
	natsclient.NC.Close()
	logger.Log.Info("Shutdown complete")
}
