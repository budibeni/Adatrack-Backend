package natsclient

import (
	"fmt"
	"time"

	"backend/internal/config"
	"backend/internal/logger"
	"github.com/nats-io/nats.go"
)

var (
	NC *nats.Conn
	JS nats.JetStreamContext
)

func Connect(cfg *config.Config) error {
	var err error
	opts := []nats.Option{
		nats.MaxReconnects(-1), // Retry forever
		nats.ReconnectWait(2 * time.Second),
		nats.DisconnectErrHandler(func(nc *nats.Conn, err error) {
			logger.Log.Warn("NATS Disconnected", "error", err)
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			logger.Log.Info("NATS Reconnected", "url", nc.ConnectedUrl())
		}),
		nats.ClosedHandler(func(nc *nats.Conn) {
			logger.Log.Error("NATS Connection Closed", "error", nc.LastError())
		}),
	}

	NC, err = nats.Connect(cfg.NatsURL, opts...)
	if err != nil {
		return fmt.Errorf("nats connect error: %w", err)
	}

	JS, err = NC.JetStream()
	if err != nil {
		return fmt.Errorf("nats jetstream error: %w", err)
	}

	return nil
}

// ProvisionStreams enforces strict PRD specifications for retention.
func ProvisionStreams() error {
	// PRD Sec 4: MaxPending: 10.000 msgs, Retention: 48h / 4 GiB per stream
	streamConfigs := []nats.StreamConfig{
		{
			Name:     "TELEMETRY",
			Subjects: []string{"telemetry.raw.>"},
			MaxAge:   48 * time.Hour,
			MaxBytes: 4 * 1024 * 1024 * 1024, // 4 GiB
			MaxMsgs:  -1,
		},
		{
			Name:     "ALERT",
			Subjects: []string{"alert.*"},
			MaxAge:   48 * time.Hour,
			MaxBytes: 4 * 1024 * 1024 * 1024,
			MaxMsgs:  -1,
		},
		{
			Name:     "NOTIFY",
			Subjects: []string{"notify.*"},
			MaxAge:   48 * time.Hour,
			MaxBytes: 4 * 1024 * 1024 * 1024,
			MaxMsgs:  -1,
		},
		{
			Name:     "MEDIA",
			Subjects: []string{"media.*"},
			MaxAge:   48 * time.Hour,
			MaxBytes: 4 * 1024 * 1024 * 1024,
			MaxMsgs:  -1,
		},
	}

	for _, cfg := range streamConfigs {
		_, err := JS.AddStream(&cfg)
		if err != nil {
			logger.Log.Error("Failed to add/update NATS Stream", "stream", cfg.Name, "error", err)
			return err
		}
		logger.Log.Info("NATS Stream configured successfully", "stream", cfg.Name)
	}
	return nil
}
