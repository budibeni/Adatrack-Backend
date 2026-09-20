package state

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"backend/internal/logger"
	"backend/internal/models"
	"backend/internal/natsclient"
	"backend/internal/redclient"
	"backend/internal/utils"
	"backend/internal/dbclient"
	"github.com/redis/go-redis/v9"
	"github.com/jackc/pgx/v5"
)

// DetermineStatus computes vehicle state based on ACC
func DetermineStatus(accStatus int16) string {
	if accStatus == 1 {
		return "ONLINE"
	}
	return "IDLE"
}

// ProcessBatch takes a batch of telemetry payloads, MSETs to Redis, and publishes to live websocket topic
func ProcessBatch(ctx context.Context, payloads []models.TelemetryPayload) error {
	if len(payloads) == 0 {
		return nil
	}

	// MGET previous states to calculate Odometer & Engine Hours
	keys := make([]string, len(payloads))
	for i, p := range payloads {
		keys[i] = fmt.Sprintf("adatrack_gps:%s:vehicle:state:%s", p.CompanyCode, p.IMEI)
	}
	prevStatesInter, _ := redclient.Client.MGet(ctx, keys...).Result()

	// Pre-fetch missing initial states from DB (B7.1 gap fix)
	missingDBMap := make(map[string]models.TelemetryPayload)
	for i, p := range payloads {
		if prevStatesInter[i] == nil {
			schema := fmt.Sprintf("adatrack_gps_%s", p.CompanyCode)
			query := fmt.Sprintf("SELECT odometer_km, engine_hours FROM %s.tm_vehicles WHERE id = $1", schema)
			var odom, engine float64
			err := dbclient.Pool.QueryRow(ctx, query, p.VehicleID).Scan(&odom, &engine)
			if err == nil {
				missingDBMap[p.IMEI] = models.TelemetryPayload{
					IMEI: p.IMEI,
					OdometerKM: odom,
					EngineHours: engine,
					ACCStatus: p.ACCStatus,
					Timestamp: p.Timestamp,
				}
			}
		}
	}

	pipe := redclient.Client.Pipeline()
	now := float64(time.Now().Unix())
	
	dbBatch := &pgx.Batch{}
	
	// Prepare batch update for tm_vehicles
	for i, p := range payloads {
		p.Status = DetermineStatus(p.ACCStatus)

		// Parse previous state
		var prev models.TelemetryPayload
		if prevStatesInter[i] != nil {
			if str, ok := prevStatesInter[i].(string); ok {
				json.Unmarshal([]byte(str), &prev)
			}
		} else if fallback, ok := missingDBMap[p.IMEI]; ok {
			prev = fallback
		}

		// Handle Odometer & Engine Hours
		if prev.IMEI != "" {
			// Calculate distance
			dist := utils.CalculateDistance(prev.Latitude, prev.Longitude, p.Latitude, p.Longitude)
			// Guard: skip if distance is > 5km (GPS jump)
			if dist > 0 && dist < 5.0 {
				p.OdometerKM = prev.OdometerKM + dist
			} else {
				p.OdometerKM = prev.OdometerKM
			}

			// Calculate engine hours
			if p.ACCStatus == 1 && prev.ACCStatus == 1 {
				diffHours := p.Timestamp.Sub(prev.Timestamp).Hours()
				if diffHours > 0 && diffHours < 1.0 { // max 1 hour interval
					p.EngineHours = prev.EngineHours + diffHours
				} else {
					p.EngineHours = prev.EngineHours
				}
			} else {
				p.EngineHours = prev.EngineHours
			}
		}

		// Process Trip & Stop machine
		ProcessTripAndStop(ctx, &p, &prev)
		
		// Reassign to payloads slice so WebSocket gets the updated struct
		payloads[i] = p

		key := keys[i]
		val, _ := json.Marshal(p)
		pipe.Set(ctx, key, val, 5*time.Minute) 
		
		// Update ZSET for sweeper
		member := fmt.Sprintf("%s:%s", p.CompanyCode, p.IMEI)
		pipe.ZAdd(ctx, "adatrack_gps:last_updates", redis.Z{Score: now, Member: member})
		
		// Add to batch DB Update tm_vehicles
		schema := fmt.Sprintf("adatrack_gps_%s", p.CompanyCode)
		updateQuery := fmt.Sprintf(`
			UPDATE %s.tm_vehicles 
			SET last_seen_at = $1, current_lat = $2, current_lon = $3, current_speed = $4, odometer_km = $5, engine_hours = $6, status = $7
			WHERE id = $8 AND (last_seen_at IS NULL OR last_seen_at <= $1)
		`, schema)
		dbBatch.Queue(updateQuery, p.Timestamp, p.Latitude, p.Longitude, p.Speed, p.OdometerKM, p.EngineHours, p.Status, p.VehicleID)
	}

	_, err := pipe.Exec(ctx)
	if err != nil && err != redis.Nil {
		logger.Log.Error("Redis pipeline exec failed", "err", err)
		return err // Do not ACK to NATS
	}

	// Execute DB batch
	br := dbclient.Pool.SendBatch(ctx, dbBatch)
	for i := 0; i < dbBatch.Len(); i++ {
		_, err := br.Exec()
		if err != nil {
			logger.Log.Error("Worker-live DB update failed", "err", err)
		}
	}
	br.Close()

	// Publish to Websocket live topic (Fire and forget, since Websocket is ephemeral)
	for _, p := range payloads {
		val, _ := json.Marshal(p)
		if p.CompanyCode != "" {
			wsTenantSubject := fmt.Sprintf("telemetry.live.%s.%s", p.CompanyCode, p.IMEI)
			natsclient.NC.Publish(wsTenantSubject, val)
		}
		wsSubject := fmt.Sprintf("telemetry.live.%s", p.IMEI)
		natsclient.NC.Publish(wsSubject, val)
	}

	return nil
}
