package internal

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus"
)

// NATSClient handles NATS connection, publishing, and subscribing.
// It provides a unified interface for publishing messages and
// subscribing to subjects with queue groups for load balancing.
//
// Subject conventions follow PRD:
// - telemetry.raw.<IMEI>: raw telemetry dari device
// - telemetry.live.<IMEI>: update live ke WebSocket
// - telemetry.error.<IMEI>: parse error
// - alert.geofence.*, alert.speed.*, alert.offline.*, alert.battery.*, alert.sos.*
// Queue groups: persistence, live, websocket, alert
type NATSClient struct {
	conn      *nats.Conn
	js        nats.JetStreamContext
	config    *Config
	published *prometheus.CounterVec
	consumed  *prometheus.CounterVec
	pending   *prometheus.GaugeVec
}

// NewNATSClient creates a new NATS client connected to the configured URL.
// It initializes a JetStream context for stream-aware operations.
func NewNATSClient(config *Config, published, consumed *prometheus.CounterVec, pending *prometheus.GaugeVec) (*NATSClient, error) {
	nc, err := nats.Connect(config.NATS.URL,
		nats.Name("ajb-gps-adatrack"),
		nats.ReconnectWait(time.Second*2),
		nats.MaxReconnects(-1),
		nats.DisconnectHandler(func(conn *nats.Conn) {
			slog.Info("NATS disconnected", "url", conn.ConnectedUrl())
		}),
		nats.ReconnectHandler(func(conn *nats.Conn) {
			slog.Info("NATS reconnected", "url", conn.ConnectedUrl())
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to NATS: %w", err)
	}

	// Initialize JetStream context
	jsCtx, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("failed to create JetStream context: %w", err)
	}

	c := &NATSClient{
		conn:      nc,
		js:        jsCtx,
		config:    config,
		published: published,
		consumed:  consumed,
		pending:   pending,
	}

	// Attempt to initialize JetStream streams
	if err := c.initializeStreams(); err != nil {
		slog.Warn("Failed to initialize NATS streams", "error", err)
	}

	return c, nil
}

// applyJetStreamRetention sets MaxAge/MaxBytes retention on a stream config
// from Config (env JETSTREAM_MAX_AGE_HOURS / JETSTREAM_MAX_BYTES), with safe
// defaults (48 hours, 4 GiB) when unset/invalid. Pure — unit-tested.
func applyJetStreamRetention(cfg *nats.StreamConfig, config *Config) {
	const (
		defHours = 48
		defBytes = 4 * 1024 * 1024 * 1024 // 4 GiB
	)
	hours, bytes := defHours, defBytes
	if config != nil {
		if config.NATS.JetStreamMaxAgeHours > 0 {
			hours = config.NATS.JetStreamMaxAgeHours
		}
		if config.NATS.JetStreamMaxBytes > 0 {
			bytes = config.NATS.JetStreamMaxBytes
		}
	}
	cfg.MaxAge = time.Duration(hours) * time.Hour
	cfg.MaxBytes = int64(bytes)
}

// initializeStreams creates the default JetStream streams and consumers
// for the adatrack management system. Subject patterns are derived from the
// configured NATS_SUBJECT_PREFIX (telemetry.* family) so streams always match
// what publishers/subscribers actually use.
func (c *NATSClient) initializeStreams() error {
	streams := []struct {
		name  string
		parts []string // subject tokens
		pref  bool     // apply NATS_SUBJECT_PREFIX (telemetry.* family)
	}{
		{"telemetry-raw", []string{"raw", "*"}, true},
		{"telemetry-live", []string{"live", "*"}, true},
		{"telemetry-error", []string{"error", "*"}, true},
		{"alert-geofence", []string{"alert", "geofence", "*"}, false},
		{"alert-speed", []string{"alert", "speed", "*"}, false},
		{"alert-sos", []string{"alert", "sos", "*"}, false},
		{"alert-battery", []string{"alert", "battery", "*"}, false},
		{"alert-offline", []string{"alert", "offline", "*"}, false},
	}

	for _, s := range streams {
		var subj string
		if s.pref {
			subj = c.Subject(s.parts...)
		} else {
			subj = c.SubjectPlain(s.parts...)
		}
		cfg := &nats.StreamConfig{
			Name:        s.name,
			Subjects:    []string{subj},
			Description: fmt.Sprintf("adatrack GPS %s stream", s.name),
			Retention:   nats.LimitsPolicy,
			Discard:     nats.DiscardOld,
		}
		applyJetStreamRetention(cfg, c.config)
		// MaxWait diperbesar: UpdateStream pada stream besar memicu purge
		// server-side (pesan > MaxAge dihapus) yang bisa melebihi timeout
		// default 5 dtk → "context deadline exceeded" (kasus nyata 2026-08-31:
		// purge 1,77 GB). Satu stream gagal TIDAK menggagalkan stream lain.
		if _, err := c.js.AddStream(cfg, nats.MaxWait(30*time.Second)); err != nil {
			// Stream sudah ada (dibuat sesi/boot sebelumnya TANPA retensi):
			// terapkan konfigurasi baru via UpdateStream agar limit selalu
			// menyusul — tanpa ini stream lama tetap unbounded.
			if !strings.Contains(err.Error(), "already in use") {
				slog.Warn("jetstream: failed to add stream", "stream", s.name, "error", err)
				continue
			}
			if _, uerr := c.js.UpdateStream(cfg, nats.MaxWait(60*time.Second)); uerr != nil {
				slog.Warn("jetstream: failed to update stream retention", "stream", s.name, "error", uerr)
				continue
			}
		}
	}

	// Create queue group consumers for worker load balancing
	queueGroups := []string{"persistence", "live", "websocket", "alert"}
	for _, group := range queueGroups {
		_, err := c.js.AddConsumer("telemetry-raw", &nats.ConsumerConfig{
			Name:      group,
			Durable:   group,
			AckPolicy: nats.AckExplicitPolicy,
			AckWait:   30 * time.Second,
		})
		if err != nil {
			slog.Warn("Failed to add NATS consumer", "consumer", group, "error", err)
		}
	}

	return nil
}

// IsConnected reports whether the NATS connection is currently established.
func (c *NATSClient) IsConnected() bool {
	return c.conn != nil && c.conn.IsConnected()
}

// Subject builds a fully-qualified NATS subject in the telemetry.* namespace
// using the configured NATS_SUBJECT_PREFIX (see Config.Subject).
func (c *NATSClient) Subject(parts ...string) string {
	if c.config != nil {
		return c.config.Subject(parts...)
	}
	return strings.Join(parts, ".")
}

// SubjectPlain joins parts with '.' WITHOUT any prefix (alert.*/notify.* subjects).
func (c *NATSClient) SubjectPlain(parts ...string) string {
	return strings.Join(parts, ".")
}

// Publish publishes a message to the specified subject (core NATS).
// Updates the published metrics counter.
func (c *NATSClient) Publish(subject string, payload []byte) error {
	err := c.conn.Publish(subject, payload)
	if err != nil {
		return err
	}
	if c.published != nil {
		c.published.WithLabelValues(subject).Inc()
	}
	return nil
}

// PublishJetStream publishes a message to JetStream with acknowledgment.
func (c *NATSClient) PublishJetStream(subject string, payload []byte) error {
	_, err := c.js.Publish(subject, payload)
	if err != nil {
		return err
	}
	if c.published != nil {
		c.published.WithLabelValues(subject).Inc()
	}
	return nil
}

// Subscribe subscribes to a subject with a queue group for load balancing.
func (c *NATSClient) Subscribe(subject string, queueGroup string, handler func(*nats.Msg) error) (*nats.Subscription, error) {
	sub, err := c.conn.QueueSubscribe(subject, queueGroup, func(msg *nats.Msg) {
		if err := handler(msg); err != nil {
			slog.Error("NATS handler error", "subject", subject, "error", err)
		}
		if c.consumed != nil {
			c.consumed.WithLabelValues(subject, queueGroup).Inc()
		}
		if c.pending != nil {
			c.pending.WithLabelValues(subject).Dec()
		}
	})
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe to %s: %w", subject, err)
	}
	if c.published != nil {
		c.published.WithLabelValues(subject).Inc()
	}
	return sub, nil
}

// Unsubscribe unsubscribes from a subject.
func (c *NATSClient) Unsubscribe(sub *nats.Subscription) {
	sub.Unsubscribe()
}

// Flush flushes pending messages with a timeout.
func (c *NATSClient) Flush(timeout time.Duration) error {
	return c.conn.FlushTimeout(timeout)
}

// Close closes the NATS connection.
func (c *NATSClient) Close() error {
	c.conn.Close()
	return nil
}
