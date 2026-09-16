package main

import (
	"fmt"
	"net/http"
	"adatrack/internal/config"
	"adatrack/internal/logger"
	"adatrack/internal/dbclient"
	"adatrack/internal/redclient"
	"adatrack/internal/natsclient"
)

func main() {
	logger.InitLogger()
	cfg := config.Load()

	if err := dbclient.Connect(cfg); err != nil {
		logger.Log.Error("Failed to connect DB", "error", err)
	} else {
		logger.Log.Info("DB Connected")
	}

	if err := redclient.Connect(cfg); err != nil {
		logger.Log.Error("Failed to connect Redis", "error", err)
	} else {
		logger.Log.Info("Redis Connected")
	}

	if err := natsclient.Connect(cfg); err != nil {
		logger.Log.Error("Failed to connect NATS", "error", err)
	} else {
		logger.Log.Info("NATS Connected")
		if err := natsclient.ProvisionStreams(); err != nil {
			logger.Log.Error("Failed to provision NATS streams", "error", err)
		} else {
			logger.Log.Info("NATS Streams provisioned")
		}
	}

	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		// Dummy prometheus metrics endpoint
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("# HELP active_connections Active connections\nactive_connections 1\n"))
	})

	logger.Log.Info("foundation-check service running on :8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		logger.Log.Error("HTTP Server failed", "error", err)
	}
}
