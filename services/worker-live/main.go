package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"backend/internal/config"
	"backend/internal/logger"
	"backend/internal/natsclient"
	"backend/internal/redclient"
	"backend/worker-live/internal/consumer"
)

func main() {
	logger.InitLogger()
	cfg := config.Load()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := redclient.Connect(ctx, cfg); err != nil {
		logger.Log.Error("FATAL Redis", "err", err); os.Exit(1)
	}
	if err := natsclient.Connect(cfg); err != nil {
		logger.Log.Error("FATAL NATS", "err", err); os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	healthServer := &http.Server{Addr: ":8082", Handler: mux}
	go healthServer.ListenAndServe()

	worker := consumer.NewWorker()
	worker.Start()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	worker.Stop()
	
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	healthServer.Shutdown(shutdownCtx)
	
	natsclient.NC.Close()
	logger.Log.Info("Shutdown complete")
}
