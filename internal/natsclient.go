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
	// StreamCommand carries the B8 downlink family: `command.request.<company>`
	// (api-vehicle → ingestion-tcp) and `command.result.<company>` (fan-out).
	StreamCommand = "command"
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
		{StreamCommand, []string{"command.>"}},
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

	// Durable PULL consumers for the B1 workers + WebSocket bridges. They are
	// created ONCE with DeliverNewPolicy and then PERSIST, which is what makes
	// at-least-once work across restarts:
	//   · DeliverNewPolicy → a brand-new durable never replays the 48 h stream
	//     history (replaying it corrupts the odometer/trip accumulator);
	//   · a pull consumer is NOT deleted when the client disconnects, so the next
	//     start RESUMES from the acknowledged position and picks up everything
	//     published during downtime.
	// (A push consumer created by js.QueueSubscribe is deleted on unsubscribe —
	// measured live 2026-09-29: the consumer was recreated at every service start
	// and the downtime backlog was silently skipped.)
	for _, consumer := range []struct{ stream, name, subject string }{
		{StreamTelemetryRaw, "persistence", c.Subject("raw", ">")},
		{StreamTelemetryRaw, "live", c.Subject("raw", ">")},
		{StreamTelemetryRaw, "alert", c.Subject("raw", ">")},
		{StreamTelemetryLive, "websocket-live", c.Subject("live", ">")},
		// The filter must match the subscriber's subject EXACTLY: a pull bind with
		// "notify.alert.>" against a "notify.>" consumer fails with
		// "subject does not match consumer" (found live).
		{StreamNotify, "websocket-notify", "notify.alert.>"},
		{StreamMedia, "websocket-media", "media.event.>"},
		{StreamCommand, "ingestion-command-dispatch", "command.request.>"},
	} {
		if _, err := c.js.AddConsumer(consumer.stream, &nats.ConsumerConfig{
			Durable:       consumer.name,
			FilterSubject: consumer.subject,
			DeliverPolicy: nats.DeliverNewPolicy,
			AckPolicy:     nats.AckExplicitPolicy,
			AckWait:       30 * time.Second,
			MaxDeliver:    5,
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
		addErr := func() error { _, err := c.js.AddStream(cfg); return err }()
		if addErr == nil {
			slog.Info("jetstream stream ready", "stream", cfg.Name,
				"max_age_hours", cfg.MaxAge.Hours(), "max_bytes", cfg.MaxBytes)
			return nil
		}

		// Stream sudah ada → UPDATE dengan konfigurasi yang sama (retention selalu
		// mengikuti config; stream tanpa batas akan tumbuh tanpa henti, FR-4.1).
		if strings.Contains(addErr.Error(), "already in use") {
			_, updErr := c.js.UpdateStream(cfg)
			if updErr == nil {
				slog.Info("jetstream stream ready", "stream", cfg.Name,
					"max_age_hours", cfg.MaxAge.Hours(), "max_bytes", cfg.MaxBytes)
				return nil
			}
			if !strings.Contains(updErr.Error(), "insufficient storage") {
				return fmt.Errorf("update: %w", updErr)
			}
			// UPDATE ditolak karena budget server. Sebelumnya cabang ini jatuh ke
			// `return addErr` ("already in use") sehingga MaxBytes TIDAK pernah
			// diturunkan pada jalur update — insiden 2026-09-23: setelah budget
			// habis, `telemetry-raw` gagal dibuat ulang dan hanya meninggalkan
			// WARN (retensi + guard backpressure hilang tanpa terlihat di /healthz).
			// Pakai error update sebagai pemicu degradasi.
			addErr = updErr
		}

		if !strings.Contains(addErr.Error(), "insufficient storage") {
			return addErr
		}
		next := cfg.MaxBytes / 2
		if next < minMaxBytes {
			return fmt.Errorf("storage limit too small: %w", addErr)
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
	return c.subscribeCore(subject, queueGroup, handler), nil
}

// subscribeCore is the core-NATS worker used by Subscribe.
func (c *NATSClient) subscribeCore(subject, queueGroup string, handler func(*nats.Msg) error) *nats.Subscription {
	sub, err := c.conn.QueueSubscribe(subject, queueGroup, func(msg *nats.Msg) {
		if herr := handler(msg); herr != nil {
			slog.Error("nats handler failed", "subject", msg.Subject, "queue", queueGroup, "error", herr)
		}
		if c.consumed != nil {
			c.consumed.WithLabelValues(subject, queueGroup).Inc()
		}
	})
	if err != nil {
		slog.Error("nats subscribe failed", "subject", subject, "queue", queueGroup, "error", err)
		return nil
	}
	return sub
}

// QueueSubscribeDurable consumes a subject through a DURABLE JetStream consumer
// with manual (explicit) acknowledgement.
//
// It exists for the B8 downlink path: a command published while ingestion-tcp is
// down must NOT be lost (with core NATS a subscriber that is offline simply misses
// it). The consumer survives restarts, re-delivers unacknowledged messages and
// gives up after MaxDeliver attempts, which turns "operator re-sends manually"
// into an at-least-once contract.
func (c *NATSClient) QueueSubscribeDurable(
	stream, subject, queueGroup, durable string,
	handler func(*nats.Msg) error,
) (*nats.Subscription, error) {
	return c.queueSubscribeDurable(stream, subject, queueGroup, durable, handler)
}

// QueueSubscribeDurableNew is kept for call-site compatibility; both wrappers now
// share the same (correct) delivery policy — see queueSubscribeDurable.
func (c *NATSClient) QueueSubscribeDurableNew(
	stream, subject, queueGroup, durable string,
	handler func(*nats.Msg) error,
) (*nats.Subscription, error) {
	return c.queueSubscribeDurable(stream, subject, queueGroup, durable, handler)
}

// queueSubscribeDurable implements the FR-4.1 at-least-once contract by BINDING to
// the pre-created durable PULL consumer and draining it with a fetch loop.
//
// Pull (not push) is load-bearing here. A push consumer created by the js helpers is
// DELETED when the subscription closes, so every service restart silently started a
// brand-new consumer (Deliver Policy: New) and skipped everything published during
// downtime — measured live 2026-09-29: a 10-message backlog published while the
// worker was stopped was never persisted (delta 0). The pull consumer lives in the
// server, is created once with DeliverNewPolicy (no 48-hour history replay, which
// would otherwise feed stale fixes into the odometer/trip accumulator), and RESUMES
// from its acknowledged position on the next start.
//
// MaxAckPending/MaxDeliver/AckWait are owned by the stored consumer config: nats.go
// rejects a requested value that differs from the stored one ("configuration
// requests max ack pending to be 100, but consumer's value is 1000") and that
// rejection silently downgraded the B8 command path to core NATS.
func (c *NATSClient) queueSubscribeDurable(
	stream, subject, queueGroup, durable string,
	handler func(*nats.Msg) error,
) (*nats.Subscription, error) {
	sub, err := c.js.PullSubscribe(subject, durable, nats.BindStream(stream), nats.ManualAck())
	if err != nil {
		return nil, fmt.Errorf("durable pull subscribe %s (%s/%s): %w", subject, queueGroup, durable, err)
	}
	go c.drainPull(sub, subject, queueGroup, durable, handler)
	return sub, nil
}

// drainPull consumes a pull subscription until it is unsubscribed. Every message is
// Ack-ed on success and Nak-ed on failure (at-least-once, never silent-drop).
func (c *NATSClient) drainPull(
	sub *nats.Subscription, subject, queueGroup, durable string,
	handler func(*nats.Msg) error,
) {
	const batchSize = 64
	for {
		msgs, err := sub.Fetch(batchSize, nats.MaxWait(2*time.Second))
		if err != nil {
			switch {
			case errors.Is(err, nats.ErrTimeout):
				continue // idle: nothing pending right now
			case errors.Is(err, nats.ErrBadSubscription), errors.Is(err, nats.ErrConnectionClosed):
				return // shutdown: the caller unsubscribed / the connection is gone
			default:
				slog.Warn("nats pull fetch failed", "subject", subject,
					"durable", durable, "error", err)
				time.Sleep(200 * time.Millisecond)
				continue
			}
		}
		for _, msg := range msgs {
			if herr := handler(msg); herr != nil {
				// NAK makes the message eligible for redelivery instead of silently
				// dropping it; the error is logged either way (rule §8).
				slog.Error("nats durable handler failed", "subject", msg.Subject,
					"queue", queueGroup, "durable", durable, "error", herr)
				_ = msg.Nak()
			} else if aerr := msg.Ack(); aerr != nil {
				slog.Warn("nats durable ack failed", "subject", msg.Subject,
					"durable", durable, "error", aerr)
			}
			if c.consumed != nil {
				c.consumed.WithLabelValues(subject, queueGroup).Inc()
			}
		}
	}
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
