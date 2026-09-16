package natsclient
import (
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
func ProvisionStreams() error {
	streamConfigs := []nats.StreamConfig{
		{
			Name: "TELEMETRY", Subjects: []string{"telemetry.raw.>"},
			MaxAge: 48 * time.Hour, MaxBytes: 4 * 1024 * 1024 * 1024,
		},
		{
			Name: "ALERT", Subjects: []string{"alert.*"},
			MaxAge: 48 * time.Hour, MaxBytes: 4 * 1024 * 1024 * 1024,
		},
	}
	for _, cfg := range streamConfigs {
		_, err := JS.AddStream(&cfg)
		if err != nil { return err }
	}
	return nil
}
