package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"adatrack/internal/config"
	"adatrack/internal/logger"
	"adatrack/internal/natsclient"
)

type TelemetryPayload struct {
	IMEI      string    `json:"imei"`
	Timestamp time.Time `json:"timestamp"`
	Lat       float64   `json:"lat"`
	Lng       float64   `json:"lng"`
	Speed     float64   `json:"speed"`
	Course    float64   `json:"course"`
	Acc       bool      `json:"acc"`
	Tenant    string    `json:"tenant,omitempty"`
}

func main() {
	logger.InitLogger()
	cfg := config.Load()

	if err := natsclient.Connect(cfg); err != nil {
		logger.Log.Error("Failed to connect to NATS", "error", err)
		return
	}

	ln, err := net.Listen("tcp", ":5001")
	if err != nil {
		logger.Log.Error("Failed to listen TCP", "error", err)
		return
	}
	defer ln.Close()
	logger.Log.Info("ingestion-tcp started on :5001")

	for {
		conn, err := ln.Accept()
		if err != nil {
			logger.Log.Error("Accept error", "error", err)
			continue
		}
		go handleConnection(conn)
	}
}

func handleConnection(conn net.Conn) {
	defer conn.Close()
	logger.Log.Info("New device connected", "addr", conn.RemoteAddr().String())

	// Simulated GT06 decoding & NATS publishing
	payload := TelemetryPayload{
		IMEI:      "123456789012345",
		Timestamp: time.Now(),
		Lat:       -6.200000,
		Lng:       106.816666,
		Speed:     45.5,
		Acc:       true,
	}
	
	data, _ := json.Marshal(payload)
	subject := fmt.Sprintf("telemetry.raw.%s", payload.IMEI)
	
	if err := natsclient.NC.Publish(subject, data); err != nil {
		logger.Log.Error("Publish failed", "error", err)
	} else {
		logger.Log.Debug("Published telemetry", "subject", subject)
	}
}
