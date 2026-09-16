package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"adatrack/internal/config"
	"adatrack/internal/dbclient"
	"adatrack/internal/logger"
	"adatrack/internal/natsclient"
	"adatrack/internal/tenant"
	
	"github.com/nats-io/nats.go"
)

type TelemetryPayload struct {
	IMEI      string    `json:"imei"`
	Timestamp time.Time `json:"timestamp"`
	Lat       float64   `json:"lat"`
	Lng       float64   `json:"lng"`
	Speed     float64   `json:"speed"`
	Acc       bool      `json:"acc"`
}

func main() {
	logger.InitLogger()
	cfg := config.Load()

	if err := dbclient.Connect(cfg); err != nil {
		logger.Log.Error("Failed to connect DB", "error", err)
		return
	}
	if err := natsclient.Connect(cfg); err != nil {
		logger.Log.Error("Failed to connect NATS", "error", err)
		return
	}

	logger.Log.Info("worker-persistence started")

	_, err := natsclient.NC.Subscribe("telemetry.raw.>", func(m *nats.Msg) {
		var payload TelemetryPayload
		if err := json.Unmarshal(m.Data, &payload); err != nil {
			logger.Log.Error("Failed to parse telemetry", "error", err)
			return
		}

		ctx := context.Background()
		schema, err := tenant.ResolveSchemaByIMEI(ctx, payload.IMEI)
		if err != nil {
			logger.Log.Warn("Tenant not found", "imei", payload.IMEI)
			schema = "adatrack_gps_default"
		}

		query := fmt.Sprintf(`
			INSERT INTO %s.th_telemetry_logs (imei, latitude, longitude, speed, acc, timestamp) 
			VALUES ($1, $2, $3, $4, $5, $6)
		`, schema)
		
		_, err = dbclient.Pool.Exec(ctx, query, 
			payload.IMEI, payload.Lat, payload.Lng, payload.Speed, payload.Acc, payload.Timestamp)
			
		if err != nil {
			logger.Log.Error("Failed to persist", "error", err)
		} else {
			logger.Log.Debug("Persisted telemetry", "imei", payload.IMEI)
		}
	})

	if err != nil {
		logger.Log.Error("Subscribe error", "error", err)
		return
	}

	select {}
}
