package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"ajb_gps/internal/config"
	"ajb_gps/internal/logger"
	"ajb_gps/internal/natsclient"
	"ajb_gps/internal/redclient"
	"ajb_gps/internal/tenant"
	"ajb_gps/internal/dbclient"
	
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
	}
	if err := redclient.Connect(cfg); err != nil {
		logger.Log.Error("Failed to connect Redis", "error", err)
		return
	}
	if err := natsclient.Connect(cfg); err != nil {
		logger.Log.Error("Failed to connect NATS", "error", err)
		return
	}

	logger.Log.Info("worker-live started")

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

		key := fmt.Sprintf("adatrack_gps:%s:vehicle:state:%s", schema, payload.IMEI)
		
		err = redclient.Client.HSet(ctx, key, map[string]interface{}{
			"lat":       payload.Lat,
			"lng":       payload.Lng,
			"speed":     payload.Speed,
			"acc":       payload.Acc,
			"timestamp": payload.Timestamp.Unix(),
			"status":    "ONLINE",
		}).Err()
		
		if err != nil {
			logger.Log.Error("Failed to update Redis", "error", err)
		} else {
			redclient.Client.Expire(ctx, key, 5*time.Minute)
			logger.Log.Debug("Updated live state", "key", key)
			
			// Publish for websocket
			natsclient.NC.Publish(fmt.Sprintf("telemetry.live.%s", payload.IMEI), m.Data)
		}
	})

	if err != nil {
		logger.Log.Error("Subscribe error", "error", err)
		return
	}

	select {}
}
