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
	"backend/internal/redclient"
	"backend/service-monitor/internal/api"
)

func main() {
	logger.InitLogger()
	cfg := config.Load()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := redclient.Connect(ctx, cfg); err != nil {
		logger.Log.Error("Redis connection failed", "err", err)
	}

	router := api.SetupRouter(cfg)

	server := &http.Server{
		Addr:    ":8085",
		Handler: router,
	}

	go func() {
		logger.Log.Info("Starting service-monitor", "port", "8085")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Log.Error("HTTP server error", "err", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Log.Info("Shutting down service-monitor...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	server.Shutdown(shutdownCtx)

	logger.Log.Info("Shutdown complete")
}
