package controllers

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"adatrack_gps/ingestion-tcp/models"
	"adatrack_gps/internal"
	"adatrack_gps/internal/tenant"
)

// Server owns the per-protocol accept loops, the connection budget and the
// publish path to `telemetry.raw.<IMEI>` (PRD Module 1, FR-1.1..FR-1.6).
type Server struct {
	cfg     *internal.Config
	tenants *tenant.Manager
	nats    *internal.NATSClient

	// connBudget bounds concurrent connections (FR-1.1: max 5000).
	connBudget chan struct{}

	ctx    context.Context
	cancel context.CancelFunc

	// dropped counts frames discarded under backpressure (observability).
	dropped   atomic.Uint64
	closeOnce sync.Once
}

// NewServer wires the ingestion runtime.
func NewServer(cfg *internal.Config, tenants *tenant.Manager, nats *internal.NATSClient) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		cfg:        cfg,
		tenants:    tenants,
		nats:       nats,
		connBudget: make(chan struct{}, cfg.TCP.MaxConnections),
		ctx:        ctx,
		cancel:     cancel,
	}
}

// Config exposes the effective configuration (listeners, timeouts).
func (s *Server) Config() *internal.Config { return s.cfg }

// Shutdown cancels the accept loops and stops handling new frames.
func (s *Server) Shutdown() { s.closeOnce.Do(s.cancel) }

// DroppedFrames reports how many frames were dropped under backpressure.
func (s *Server) DroppedFrames() uint64 { return s.dropped.Load() }

// AcceptLoop accepts connections on a listener bound to one protocol and
// dispatches each connection to its protocol handler. Rejections are counted so
// a connection flood is visible (PRD §9.6 / FR-1.1).
func (s *Server) AcceptLoop(ln net.Listener, proto models.Protocol) {
	protoName := proto.String()
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-s.ctx.Done():
				return
			default:
				slog.Warn("accept error", "protocol", protoName, "error", err)
				continue
			}
		}

		select {
		case s.connBudget <- struct{}{}:
			tcpConnectionsActive.Inc()
			tcpConnectionsTotal.Inc()
			go s.handleConn(conn, proto)
		case <-s.ctx.Done():
			_ = conn.Close()
			return
		default:
			slog.Warn("max connections reached, rejecting", "remote", conn.RemoteAddr(),
				"protocol", protoName, "limit", s.cfg.TCP.MaxConnections)
			rejectedTotal.WithLabelValues("max_conn").Inc()
			_ = conn.Close()
		}
	}
}

// connClose releases the connection budget for one finished connection.
func (s *Server) connClose(c net.Conn, proto string) {
	select {
	case <-s.connBudget:
	default:
	}
	tcpConnectionsActive.Dec()
	_ = c.Close()
	slog.Debug("connection closed", "remote", c.RemoteAddr(), "protocol", proto)
}

// handleConn dispatches a connection to its protocol handler.
func (s *Server) handleConn(c net.Conn, proto models.Protocol) {
	switch proto {
	case models.ProtoTeltonika:
		s.handleTeltonika(c)
	default:
		s.handleGT06(c)
	}
}

// publishTelemetry publishes a decoded message to `telemetry.raw.<IMEI>`
// (FR-1.6) with backpressure signalling (FR-1.5) and publish metrics.
func (s *Server) publishTelemetry(t models.TelemetryMessage, proto string) error {
	company := t.CompanyCode
	if company == "" {
		company = "default"
	}

	// FR-1.5: warn >50% and drop >90% of the stream budget; every drop is logged
	// and counted (never a silent drop).
	level, percent := s.nats.BackpressureLevel(s.ctx, internal.StreamTelemetryRaw)
	switch level {
	case "warn":
		backpressureWarnings.Inc()
		slog.Warn("nats backpressure warning", "stream", internal.StreamTelemetryRaw,
			"used_percent", percent, "imei", t.IMEI)
	case "drop":
		s.dropped.Add(1)
		backpressureDrops.WithLabelValues(internal.StreamTelemetryRaw).Inc()
		slog.Error("nats backpressure DROP: telemetry discarded",
			"stream", internal.StreamTelemetryRaw, "used_percent", percent,
			"imei", t.IMEI, "company", company, "dropped_total", s.dropped.Load())
		return nil
	}

	payload, err := json.Marshal(t)
	if err != nil {
		return err
	}

	subject := s.nats.Subject("raw", t.IMEI)
	start := time.Now()
	err = s.nats.Publish(subject, payload)
	natsPublishDuration.WithLabelValues(s.nats.Subject("raw")).
		Observe(float64(time.Since(start).Microseconds()) / 1000.0)
	if err != nil {
		natsPublishErrors.WithLabelValues(company).Inc()
		return err
	}
	messagesPublished.WithLabelValues(proto).Inc()
	return nil
}

// readDeadlined reads one GT06 packet with the idle timeout applied (FR-1.3).
func (s *Server) readDeadlined(c net.Conn, r *bufio.Reader) (Packet, error) {
	_ = c.SetReadDeadline(time.Now().Add(s.cfg.TCP.IdleTimeout))
	return ReadPacket(r)
}
