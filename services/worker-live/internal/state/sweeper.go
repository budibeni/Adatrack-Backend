package state

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"backend/internal/logger"
	"backend/internal/models"
	"backend/internal/natsclient"
	"backend/internal/redclient"
	"backend/internal/dbclient"
	"github.com/redis/go-redis/v9"
)

func StartOfflineSweeper(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	go func() {
		for {
			select {
			case <-ctx.Done():
				ticker.Stop()
				return
			case <-ticker.C:
				sweepOffline(ctx)
			}
		}
	}()
}

func sweepOffline(ctx context.Context) {
	now := time.Now().Unix()
	cutoff := float64(now - (5 * 60)) // 5 minutes ago

	ops := redclient.Client
	expired, err := ops.ZRangeByScore(ctx, "adatrack_gps:last_updates", &redis.ZRangeBy{
		Min: "-inf",
		Max: fmt.Sprintf("%f", cutoff),
	}).Result()

	if err != nil {
		logger.Log.Error("Sweeper failed to ZRangeByScore", "err", err)
		return
	}

	if len(expired) > 0 {
		for _, member := range expired {
			// member is "company_code:imei"
			parts := strings.Split(member, ":")
			if len(parts) != 2 {
				continue
			}
			companyCode := parts[0]
			imei := parts[1]
			
			// Get last known state
			key := fmt.Sprintf("adatrack_gps:%s:vehicle:state:%s", companyCode, imei)
			valStr, err := ops.Get(ctx, key).Result()
			
			var p models.TelemetryPayload
			if err == nil && valStr != "" {
				json.Unmarshal([]byte(valStr), &p)
			} else {
				p.IMEI = imei
				p.CompanyCode = companyCode
			}

			// Publish OFFLINE status
			p.Status = "OFFLINE"
			p.Timestamp = time.Now()
			
			val, _ := json.Marshal(p)
			
			// Update Redis with OFFLINE status
			ops.Set(ctx, key, val, 5*time.Minute)
			
			if companyCode != "" {
				wsTenantSubject := fmt.Sprintf("telemetry.live.%s.%s", companyCode, imei)
				natsclient.NC.Publish(wsTenantSubject, val)
			}
			wsSubject := fmt.Sprintf("telemetry.live.%s", imei)
			natsclient.NC.Publish(wsSubject, val)
			
			// Publish to internal alert queue for worker-alert to generate OFFLINE alert
			natsclient.NC.Publish("alert.internal.offline", val)
			
			// Remove from ZSET so we don't spam
			ops.ZRem(ctx, "adatrack_gps:last_updates", member)

			// DB Update offline status
			if p.VehicleID > 0 {
				schema := fmt.Sprintf("adatrack_gps_%s", companyCode)
				query := fmt.Sprintf("UPDATE %s.tm_vehicles SET status = 'OFFLINE' WHERE id = $1", schema)
				if _, err := dbclient.Pool.Exec(ctx, query, p.VehicleID); err != nil {
					logger.Log.Error("Sweeper failed to update offline status", "err", err, "vehicle_id", p.VehicleID)
				}
			}
		}
		logger.Log.Info("Swept offline vehicles", "count", len(expired))
	}
}
