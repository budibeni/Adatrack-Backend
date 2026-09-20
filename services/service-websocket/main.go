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
	"backend/internal/tenant"
	"backend/internal/dbclient"
	"backend/service-websocket/internal/api"
	"backend/service-websocket/internal/ws"
)

func main() {
	logger.InitLogger()
	cfg := config.Load()
	initCtx, initCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer initCancel()

	if err := dbclient.Connect(initCtx, cfg); err != nil {
		logger.Log.Error("FATAL Database", "err", err); os.Exit(1)
	}
	defer dbclient.Pool.Close()

	if err := redclient.Connect(initCtx, cfg); err != nil {
		logger.Log.Error("FATAL Redis", "err", err); os.Exit(1)
	}
	tenant.InitManager(cfg)
	
	if err := natsclient.Connect(cfg); err != nil {
		logger.Log.Error("FATAL NATS", "err", err); os.Exit(1)
	}
	defer natsclient.NC.Close()

	appCtx, appCancel := context.WithCancel(context.Background())
	defer appCancel()

	// Initialize WebSocket Hub
	hub := ws.NewHub(cfg)
	go hub.Run()
	// Setup consumer to push updates to hub
	go hub.StartConsumer(appCtx)

	// Initialize API Router
	router := api.SetupRouter(cfg, hub)

	server := &http.Server{
		Addr:    ":" + cfg.PortWebsocket,
		Handler: router,
	}

	go func() {
		logger.Log.Info("Starting service-websocket", "port", cfg.PortWebsocket)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Log.Error("HTTP server error", "err", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Log.Info("Shutting down service-websocket...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	
	hub.Stop()
	server.Shutdown(shutdownCtx)
	
	logger.Log.Info("Shutdown complete")
}
