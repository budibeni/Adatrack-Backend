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
	"github.com/redis/go-redis/v9"
)

// ProcessBatch takes a batch of telemetry payloads, MSETs to Redis, and publishes to live websocket topic
func ProcessBatch(ctx context.Context, payloads []models.TelemetryPayload) error {
	if len(payloads) == 0 {
		return nil
	}

	pipe := redclient.Client.Pipeline()
	now := float64(time.Now().Unix())
	for _, p := range payloads {
		if p.ACCStatus == 1 && p.Speed > 0 {
			p.Status = "ONLINE"
		} else {
			p.Status = "IDLE" // ACC OFF or Speed 0 is IDLE
		}

		key := fmt.Sprintf("adatrack_gps:%s:vehicle:state:%s", p.CompanyCode, p.IMEI)
		val, _ := json.Marshal(p)
		pipe.Set(ctx, key, val, 5*time.Minute) 
		
		// Update ZSET for sweeper
		member := fmt.Sprintf("%s:%s", p.CompanyCode, p.IMEI)
		pipe.ZAdd(ctx, "adatrack_gps:last_updates", redis.Z{Score: now, Member: member})
	}

	_, err := pipe.Exec(ctx)
	if err != nil && err != redis.Nil {
		logger.Log.Error("Redis pipeline exec failed", "err", err)
		return err // Do not ACK to NATS
	}

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
