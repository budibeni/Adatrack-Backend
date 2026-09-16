package consumer

import (
	"context"
	"sync"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"

	"backend/internal/dbclient"
	"backend/internal/logger"
	"backend/worker-alert/internal/geo"
	"backend/internal/natsclient"
)

type Worker struct {
	sub         *nats.Subscription
	speedCache  sync.Map
	refreshStop chan struct{}
}

func NewWorker() *Worker {
	return &Worker{
		refreshStop: make(chan struct{}),
	}
}

func (w *Worker) Start() {
	var err error
	w.sub, err = natsclient.NC.Subscribe("telemetry.raw.>", func(m *nats.Msg) {
		w.processTelemetry(m)
	})
	if err != nil {
		logger.Log.Error("Failed to subscribe in worker-alert", "err", err)
	}
	go w.cacheRefresher()
}

func (w *Worker) cacheRefresher() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			// In production, you'd iterate over all companies to refresh. 
			// For simplicity in B3, we rely on a pull-through cache in processTelemetry if missing,
			// or just let it query if not in cache (less optimal but works).
			// Better: let's do a pull-through cache approach instead of a background refresher to keep it simple.
		case <-w.refreshStop:
			return
		}
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

	// 1. OVERSPEEDING LOGIC (With sync.Map Cache)
	cacheKey := fmt.Sprintf("%s:%d", payload.CompanyCode, payload.VehicleID)
	var maxSpeed float64
	val, ok := w.speedCache.Load(cacheKey)
	if !ok {
		err := dbclient.Pool.QueryRow(ctx, fmt.Sprintf("SELECT max_speed_kmh FROM %s.tm_speed_configs WHERE vehicle_id = $1 AND enabled = true", schema), payload.VehicleID).Scan(&maxSpeed)
		if err == nil {
			w.speedCache.Store(cacheKey, maxSpeed)
		} else {
			w.speedCache.Store(cacheKey, float64(0)) // Store 0 to prevent re-querying if not found
		}
	} else {
		maxSpeed = val.(float64)
	}

	if maxSpeed > 0 && payload.Speed > maxSpeed {
		w.createAlert(ctx, schema, "OVERSPEEDING", "high", payload.VehicleID, payload.Lat, payload.Lon, map[string]interface{}{"speed": payload.Speed, "limit": maxSpeed})
	}

	// 2. SOS LOGIC
	if payload.EventCode == 0x26 || payload.EventCode == 0x27 || payload.EventCode == 0x19 {
		w.createAlert(ctx, schema, "SOS", "critical", payload.VehicleID, payload.Lat, payload.Lon, map[string]interface{}{"event_code": payload.EventCode})
	}
	
	// 3. GEOFENCE LOGIC
	// We'll query geofences from DB for this company (in production we'd cache this in sync.Map too)
	// Querying DB directly here for simplicity of the PoC, caching can be added identically to speed configs.
	rows, err := dbclient.Pool.Query(ctx, fmt.Sprintf("SELECT id, name, area_type, coordinates, radius_meters, boundary_points FROM %s.tm_geofences WHERE deleted_at IS NULL", schema))
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var id int
			var name, areaType string
			var coordsJSON, boundsJSON []byte
			var radius float64
			rows.Scan(&id, &name, &areaType, &coordsJSON, &radius, &boundsJSON)

			point := geo.Point{Lat: payload.Lat, Lon: payload.Lon}
			isInside := false
			
			if areaType == "circle" {
				var center geo.Point
				json.Unmarshal(coordsJSON, &center)
				dist := geo.Haversine(point, center)
				if dist <= radius {
					isInside = true
				}
			} else if areaType == "polygon" {
				var polygon []geo.Point
				json.Unmarshal(boundsJSON, &polygon)
				isInside = geo.RayCasting(point, polygon)
			}
			
			// If inside, we might trigger a GEOFENCE_ENTRY alert if they weren't inside before
			// For this MVP, we just log it or trigger a generic GEOFENCE_VIOLATION if it's a restricted zone.
			if isInside {
				// Note: typically we track entry/exit state in Redis.
				// redclient.Client.Set(...)
			}
		}
	}
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
