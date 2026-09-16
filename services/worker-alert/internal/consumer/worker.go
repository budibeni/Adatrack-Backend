package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"

	"backend/internal/dbclient"
	"backend/internal/logger"
	"backend/internal/natsclient"
)

type Worker struct {
	sub *nats.Subscription
}

func NewWorker() *Worker {
	return &Worker{}
}

func (w *Worker) Start() {
	var err error
	w.sub, err = natsclient.NC.Subscribe("telemetry.raw.>", func(m *nats.Msg) {
		w.processTelemetry(m)
	})
	if err != nil {
		logger.Log.Error("Failed to subscribe in worker-alert", "err", err)
	}
}

type TelemetryPayload struct {
	IMEI        string  `json:"imei"`
	CompanyCode string  `json:"company_code"`
	VehicleID   int     `json:"vehicle_id"`
	Lat         float64 `json:"lat"`
	Lon         float64 `json:"lon"`
	Speed       float64 `json:"speed"`
	EventCode   int     `json:"event_code"`
}

func (w *Worker) processTelemetry(m *nats.Msg) {
	var payload TelemetryPayload
	if err := json.Unmarshal(m.Data, &payload); err != nil {
		logger.Log.Error("Failed to decode telemetry", "err", err)
		return
	}
	
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	schema := "adatrack_gps_" + payload.CompanyCode

	// 1. OVERSPEEDING LOGIC
	var maxSpeed float64
	err := dbclient.Pool.QueryRow(ctx, fmt.Sprintf("SELECT max_speed_kmh FROM %s.tm_speed_configs WHERE vehicle_id = $1 AND enabled = true", schema), payload.VehicleID).Scan(&maxSpeed)
	if err == nil && payload.Speed > maxSpeed {
		w.createAlert(ctx, schema, "OVERSPEEDING", "high", payload.VehicleID, payload.Lat, payload.Lon, map[string]interface{}{"speed": payload.Speed, "limit": maxSpeed})
	}

	// 2. SOS LOGIC
	// Assuming event_code 0x26, 0x27, 0x19 means SOS for GT06
	if payload.EventCode == 0x26 || payload.EventCode == 0x27 || payload.EventCode == 0x19 {
		w.createAlert(ctx, schema, "SOS", "critical", payload.VehicleID, payload.Lat, payload.Lon, map[string]interface{}{"event_code": payload.EventCode})
	}
	
	// Geofence and other logic would go here.
}

func (w *Worker) createAlert(ctx context.Context, schema, alertType, severity string, vehicleID int, lat, lon float64, metadata map[string]interface{}) {
	metaJSON, _ := json.Marshal(metadata)
	query := fmt.Sprintf(`INSERT INTO %s.th_alerts (type, severity, vehicle_id, lat, lon, metadata) VALUES ($1, $2, $3, $4, $5, $6)`, schema)
	
	_, err := dbclient.Pool.Exec(ctx, query, alertType, severity, vehicleID, lat, lon, metaJSON)
	if err != nil {
		logger.Log.Error("Failed to persist alert", "type", alertType, "err", err)
		return
	}
	
	// Publish notification to websocket fanout channel
	natsclient.NC.Publish(fmt.Sprintf("alert.%s", alertType), metaJSON)
}

func (w *Worker) Stop() {
	if w.sub != nil {
		w.sub.Unsubscribe()
	}
}
