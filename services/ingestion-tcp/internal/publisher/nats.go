package publisher

import (
	"encoding/json"
	"fmt"
	"time"

	"backend/internal/logger"
	"backend/internal/models"
	"backend/internal/natsclient"
)

// PublishTelemetry securely writes to NATS JetStream
func PublishTelemetry(payload models.TelemetryPayload) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	subject := fmt.Sprintf("telemetry.raw.%s", payload.IMEI)
	
	// Add context timeout to avoid blocking forever
	_, err = natsclient.JS.Publish(subject, data)
	if err != nil {
		logger.Log.Error("Failed to publish telemetry to NATS", "imei", payload.IMEI, "error", err)
		return err
	}
	
	return nil
}
