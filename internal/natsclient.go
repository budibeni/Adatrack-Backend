package internal

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus"
)

// StreamNames are the JetStream streams created at boot (PRD §4.1/§FR-4.1).
const (
	StreamTelemetryRaw  = "telemetry-raw"
	StreamTelemetryLive = "telemetry-live"
	StreamTelemetryErr  = "telemetry-error"
	StreamAlert         = "alert"
	StreamNotify        = "notify"
	StreamMedia         = "media"
)

// NATSClient wraps the core NATS connection + JetStream context.
type NATSClient struct {
	conn      *nats.Conn
	js        nats.JetStreamContext
	cfg       *Config
	published *prometheus.CounterVec
	consumed  *prometheus.CounterVec
	pending   *prometheus.GaugeVec
}

// NewNATSClient connects to NATS_, ensures the JetStream streams exist and
// returns the shared client. Reconnection is unlimited with a 2 s backoff.
func NewNATSClient(cfg *Config) (*NATSClient, error) {
	nc, err := nats.Connect(cfg.NATS.URL,
		nats.Name("adatrack-"+serviceName()),
		nats.ReconnectWait(2*time.Second),
		nats.MaxReconnects(-1),
		nats.DisconnectHandler(func(c *nats.Conn) {
			slog.Warn("nats disconnected", "url", c.ConnectedUrl())
		}),
		nats.ReconnectHandler(func(c *nats.Conn) {
			slog.Info("nats reconnected", "url", c.ConnectedUrl())
		}),
		nats.ClosedHandler(func(c *nats.Conn) {
			slog.Warn("nats connection closed", "last_error", c.LastError())
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("nats connect: %w", err)
	}

	js, err := nc.JetStream(nats.MaxWait(10 * time.Second))
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("nats jetstream context: %w", err)
	}

	c := &NATSClient{
		conn:      nc,
		js:        js,
		cfg:       cfg,
		published: NATSMessagesPublished,
		consumed:  NATSMessagesConsumed,
		pending:   NATPendingMessages,
	}
	if err := c.ensureStreams(); err != nil {
		// Non-fatal: core NATS pub/sub still works (dev without -js).
		slog.Warn("jetstream stream setup incomplete", "error", err)
	}
	return c, nil
}

// ensureStreams creates/updates every stream with the configured retention.
// Idempotent: existing streams are updated so retention limits always follow
// the config (an unbounded stream would grow without limit — §FR-4.1).
//
// If the server cannot honour the requested MaxBytes (e.g. a small data volume
// reports "insufficient storage resources available"), the limit is reduced
// step-by-step and the effective value is logged — the stream is still created
// so durable consumers work, and the operator can raise the server limit via
// deployments/nats/nats.conf (jetstream.max_file_store).
func (c *NATSClient) ensureStreams() error {
	defs := []struct {
		name     string
		subjects []string
	}{
		{StreamTelemetryRaw, []string{c.Subject("raw", ">")}},
		{StreamTelemetryLive, []string{c.Subject("live", ">")}},
		{StreamTelemetryErr, []string{c.Subject("error", ">")}},
		{StreamAlert, []string{"alert.>"}},
		{StreamNotify, []string{"notify.>"}},
		{StreamMedia, []string{"media.>"}},
	}

	var errs []error
	for _, d := range defs {
		cfg := &nats.StreamConfig{
			Name:      d.name,
			Subjects:  d.subjects,
			Retention: nats.LimitsPolicy,
			Discard:   nats.DiscardOld,
			Storage:   nats.FileStorage,
		}
		applyJetStreamRetention(cfg, c.cfg)

		if err := c.addOrUpdateStream(cfg); err != nil {
			errs = append(errs, fmt.Errorf("stream %s: %w", d.name, err))
		}
	}

	// Durable consumers used by the B1 workers (load balanced queue groups).
	for _, consumer := range []struct{ stream, name string }{
		{StreamTelemetryRaw, "persistence"},
		{StreamTelemetryRaw, "live"},
		{StreamTelemetryRaw, "alert"},
		{StreamTelemetryLive, "websocket"},
	} {
		if _, err := c.js.AddConsumer(consumer.stream, &nats.ConsumerConfig{
			Durable:       consumer.name,
			DeliverPolicy: nats.DeliverNewPolicy,
			AckPolicy:     nats.AckExplicitPolicy,
			AckWait:       30 * time.Second,
			MaxAckPending: 100, // FR-4.1 MaxInflight per subscriber
		}); err != nil && !strings.Contains(err.Error(), "already in use") {
			slog.Warn("jetstream consumer not created", "stream", consumer.stream,
				"consumer", consumer.name, "error", err)
		}
	}
	return errors.Join(errs...)
}

// addOrUpdateStream adds (or updates) one stream, degrading MaxBytes when the
// server reports insufficient storage instead of failing outright.
func (c *NATSClient) addOrUpdateStream(cfg *nats.StreamConfig) error {
	const minMaxBytes = 128 * 1024 * 1024 // 128 MiB floor
	for attempt := 0; attempt < 5; attempt++ {
		_, err := c.js.AddStream(cfg)
		if err == nil {
			slog.Info("jetstream stream ready", "stream", cfg.Name,
				"max_age_hours", cfg.MaxAge.Hours(), "max_bytes", cfg.MaxBytes)
			return nil
		}

		if strings.Contains(err.Error(), "already in use") {
			if _, uerr := c.js.UpdateStream(cfg); uerr == nil {
				return nil
			} else if !strings.Contains(uerr.Error(), "insufficient storage") {
				return fmt.Errorf("update: %w", uerr)
			}
		}

		if !strings.Contains(err.Error(), "insufficient storage") {
			return err
		}
		next := cfg.MaxBytes / 2
		if next < minMaxBytes {
			return fmt.Errorf("storage limit too small: %w", err)
		}
		slog.Warn("jetstream storage limit reduced (server cannot honour requested MaxBytes)",
			"stream", cfg.Name, "from_bytes", cfg.MaxBytes, "to_bytes", next)
		cfg.MaxBytes = next
	}
	return fmt.Errorf("stream %s: could not create within storage limits", cfg.Name)
}

// Subject builds a prefixed telemetry subject (Config.Subject).
func (c *NATSClient) Subject(parts ...string) string { return c.cfg.Subject(parts...) }

// SubjectPlain joins parts without a prefix (alert.*/notify.*/media.*).
func (c *NATSClient) SubjectPlain(parts ...string) string { return c.cfg.SubjectPlain(parts...) }

// Publish sends a core NATS message. Core (not JetStream) is used because the
// telemetry path must never block on persistence acknowledgement (FR-4.1);
// workers consume through durable JetStream consumers.
func (c *NATSClient) Publish(subject string, payload []byte) error {
	if err := c.conn.Publish(subject, payload); err != nil {
		return err
	}
	if c.published != nil {
		c.published.WithLabelValues(subject).Inc()
	}
	return nil
}

// PublishJetStream publishes with a server acknowledgement (used for the
// telemetry.error dead-letter path so failures are visible in the stream).
func (c *NATSClient) PublishJetStream(subject string, payload []byte) error {
	if _, err := c.js.Publish(subject, payload); err != nil {
		return err
	}
	if c.published != nil {
		c.published.WithLabelValues(subject).Inc()
	}
	return nil
}

// Subscribe registers a queue-group subscription; the handler error is logged
// and counted, never swallowed (rule §8 "no silent drop").
func (c *NATSClient) Subscribe(subject, queueGroup string, handler func(*nats.Msg) error) (*nats.Subscription, error) {
	sub, err := c.conn.QueueSubscribe(subject, queueGroup, func(msg *nats.Msg) {
		if herr := handler(msg); herr != nil {
			slog.Error("nats handler failed", "subject", msg.Subject, "queue", queueGroup, "error", herr)
		}
		if c.consumed != nil {
			c.consumed.WithLabelValues(subject, queueGroup).Inc()
		}
	})
	if err != nil {
		return nil, fmt.Errorf("subscribe %s (%s): %w", subject, queueGroup, err)
	}
	return sub, nil
}

// Pending returns the backlog of a JetStream stream (used for backpressure
// signalling, FR-1.5).
func (c *NATSClient) Pending(ctx context.Context, stream string) (uint64, error) {
	info, err := c.js.StreamInfo(stream)
	if err != nil {
		return 0, err
	}
	pending := info.State.Msgs
	if c.pending != nil {
		c.pending.WithLabelValues(stream).Set(float64(pending))
	}
	return pending, nil
}

// BackpressureLevel reports how loaded a stream is against its MaxBytes budget:
//
//	percent < warn  → ok
//	warn ≤ p < 90   → warn  (log warning, keep processing)
//	p ≥ 90          → drop  (FR-1.5: drop + log error + backpressure_drops_total)
func (c *NATSClient) BackpressureLevel(ctx context.Context, stream string) (level string, percent float64) {
	info, err := c.js.StreamInfo(stream)
	if err != nil {
		return "ok", 0
	}
	max := info.Config.MaxBytes
	if max <= 0 {
		max = int64(c.cfg.NATS.JetStreamMaxBytes)
	}
	used := info.State.Bytes
	if c.pending != nil {
		c.pending.WithLabelValues(stream).Set(float64(info.State.Msgs))
	}
	if max <= 0 {
		return "ok", 0
	}
	percent = float64(used) / float64(max) * 100
	switch {
	case percent >= 90:
		return "drop", percent
	case percent >= float64(c.cfg.NATS.MaxPendingPercent):
		return "warn", percent
	default:
		return "ok", percent
	}
}

// IsConnected reports the live NATS connection state (/healthz readiness).
func (c *NATSClient) IsConnected() bool { return c.conn != nil && c.conn.IsConnected() }

// Flush drains the client's write buffer with a timeout (shutdown path).
func (c *NATSClient) Flush(timeout time.Duration) error { return c.conn.FlushTimeout(timeout) }

// Unsubscribe cancels a subscription (ignoring an already-closed one).
func (c *NATSClient) Unsubscribe(sub *nats.Subscription) {
	if sub != nil {
		_ = sub.Unsubscribe()
	}
}

// Close flushes and closes the connection.
func (c *NATSClient) Close() {
	if c.conn == nil {
		return
	}
	_ = c.conn.FlushTimeout(2 * time.Second)
	c.conn.Close()
}
func applyJetStreamRetention(cfg *nats.StreamConfig, c *Config) {
	const (
		defHours = 48
		defBytes = 4 * 1024 * 1024 * 1024
	)
	hours, bytes := defHours, defBytes
	if c != nil {
		if c.NATS.JetStreamMaxAgeHours > 0 {
			hours = c.NATS.JetStreamMaxAgeHours
		}
		if c.NATS.JetStreamMaxBytes > 0 {
			bytes = c.NATS.JetStreamMaxBytes
		}
	}
	cfg.MaxAge = time.Duration(hours) * time.Hour
	cfg.MaxBytes = int64(bytes)
}
