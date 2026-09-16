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
	for _, p := range payloads {
		key := fmt.Sprintf("adatrack_gps:%s:vehicle:state:%s", p.CompanyCode, p.IMEI)
		val, _ := json.Marshal(p)
		pipe.Set(ctx, key, val, 5*time.Minute) // TTL 5 mins (OFFLINE if expired)
	}

	_, err := pipe.Exec(ctx)
	if err != nil && err != redis.Nil {
		logger.Log.Error("Redis pipeline exec failed", "err", err)
		return err // Do not ACK to NATS
	}

	// Publish to Websocket live topic (Fire and forget, since Websocket is ephemeral)
	for _, p := range payloads {
		wsSubject := fmt.Sprintf("telemetry.live.%s", p.IMEI)
		val, _ := json.Marshal(p)
		natsclient.NC.Publish(wsSubject, val)
	}

	return nil
}
