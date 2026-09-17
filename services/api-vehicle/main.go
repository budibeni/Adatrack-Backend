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
	"backend/internal/dbclient"
	"backend/internal/redclient"
	"backend/internal/tenant"
	"backend/internal/storage"
	"backend/api-vehicle/internal/api"
)

func main() {
	logger.InitLogger()
	cfg := config.Load()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := dbclient.Connect(ctx, cfg); err != nil {
		logger.Log.Error("FATAL Database", "err", err); os.Exit(1)
	}
	defer dbclient.Pool.Close()

	if err := redclient.InitRedis(cfg); err != nil {
		logger.Log.Error("FATAL Redis", "err", err); os.Exit(1)
	}

	store, err := storage.NewS3Store(cfg.S3Endpoint, cfg.S3AccessKey, cfg.S3SecretKey, cfg.S3BucketName, cfg.S3Region, cfg.S3UseSSL)
	if err != nil {
		logger.Log.Error("FATAL S3 Store", "err", err); os.Exit(1)
	}

	api.StartRetentionJob(context.Background(), cfg, store)

	router := api.SetupRouter(cfg, store)

	server := &http.Server{
		Addr:    ":8084",
		Handler: router,
	}

	go func() {
		logger.Log.Info("Starting api-vehicle", "port", "8084")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Log.Error("HTTP server error", "err", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Log.Info("Shutting down api-vehicle...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	
	server.Shutdown(shutdownCtx)
	
	logger.Log.Info("Shutdown complete")
}
