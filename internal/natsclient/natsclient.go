package natsclient

import (
	"fmt"
	"time"
	"backend/internal/config"
	"backend/internal/logger"
	"github.com/nats-io/nats.go"
)

var ( NC *nats.Conn; JS nats.JetStreamContext )

func Connect(cfg *config.Config) error {
	var err error
	opts := []nats.Option{
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2 * time.Second),
		nats.DisconnectErrHandler(func(nc *nats.Conn, err error) { logger.Log.Warn("NATS Disconnected", "error", err) }),
		nats.ReconnectHandler(func(nc *nats.Conn) { logger.Log.Info("NATS Reconnected") }),
	}
	NC, err = nats.Connect(cfg.NatsURL, opts...)
	if err != nil { return err }
	JS, err = NC.JetStream()
	if err != nil { return err }
	return nil
}

func ProvisionStreams(cfg *config.Config) error {
	maxAge := time.Duration(cfg.JetstreamMaxAgeHours) * time.Hour
	maxBytes := cfg.JetstreamMaxBytes

	streamConfigs := []nats.StreamConfig{
		{
			Name: "TELEMETRY", Subjects: []string{"telemetry.raw.>"},
			MaxAge: maxAge, MaxBytes: maxBytes, Discard: nats.DiscardOld,
		},
		{
			Name: "DEADLETTER", Subjects: []string{"dlq.>"},
			MaxAge: 168 * time.Hour, // 7 days retention for debugging DLQ
			MaxBytes: maxBytes, Discard: nats.DiscardOld,
		},
		{
			Name: "ALERT", Subjects: []string{"alert.*"},
			MaxAge: maxAge, MaxBytes: maxBytes, Discard: nats.DiscardOld,
		},
		{
			Name: "NOTIFY", Subjects: []string{"notify.*"},
			MaxAge: maxAge, MaxBytes: maxBytes, Discard: nats.DiscardOld,
		},
		{
			Name: "MEDIA", Subjects: []string{"media.*"},
			MaxAge: 168 * time.Hour, MaxBytes: 1 * 1024 * 1024 * 1024, Discard: nats.DiscardOld,
		},
	}
	for _, scfg := range streamConfigs {
		_, err := JS.AddStream(&scfg)
		if err != nil { return err }
	}
	return nil
}

// PublishToDLQ safely handles Poison Pill messages
func PublishToDLQ(topic string, data []byte) {
	_, err := JS.Publish(fmt.Sprintf("dlq.%s", topic), data)
	if err != nil {
		logger.Log.Error("CRITICAL: Failed to publish to DLQ", "err", err)
	}
}
