package natsclient

import (
	"adatrack/internal/config"
	"github.com/nats-io/nats.go"
)

var (
	NC *nats.Conn
	JS nats.JetStreamContext
)

func Connect(cfg *config.Config) error {
	var err error
	NC, err = nats.Connect(cfg.NatsURL)
	if err != nil {
		return err
	}
	JS, err = NC.JetStream()
	return err
}

func ProvisionStreams() error {
	streams := map[string][]string{
		"TELEMETRY": {"telemetry.raw.>"},
		"ALERT":     {"alert.*"},
		"NOTIFY":    {"notify.*"},
		"MEDIA":     {"media.*"},
	}

	for name, subjects := range streams {
		_, err := JS.AddStream(&nats.StreamConfig{
			Name:     name,
			Subjects: subjects,
			MaxAge:   48 * 3600 * 1000 * 1000 * 1000, // 48 hours
		})
		if err != nil {
			return err
		}
	}
	return nil
}
