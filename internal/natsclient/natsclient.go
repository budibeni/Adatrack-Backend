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
