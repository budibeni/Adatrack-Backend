package controllers

// commanddispatch.go — B8 downlink delivery (PRD §21.2 row 1).
//
// Flow:
//
//	api-vehicle  --INSERT pending-->  td_device_commands
//	             --NATS-->            command.request.<company>
//	                                      |
//	                        ingestion-tcp dispatcher
//	                                      |
//	                 live connection?  --no-->  status=offline
//	                                      |yes
//	                        encode per protocol + write to socket
//	                                      |
//	                        status=sent  + wait for the device reply
//	                                      |
//	                 0x21/0x15 reply -->  status=acked (or failed)
//	                 no reply in time -->  status=timeout
//	                                      |
//	                             command.result.<company>  (fan-out)
//
// Every transition is persisted, so "ACK device tercatat" (B8 acceptance) is
// verifiable from the database alone.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"

	"adatrack_gps/ingestion-tcp/models"
	"adatrack_gps/internal"
	"adatrack_gps/internal/tenant"
)

// pendingCommand is a command already written to a device socket that is still
// waiting for its online-command reply.
type pendingCommand struct {
	cmd    models.DeviceCommand
	sentAt time.Time
	proto  models.Protocol
}

// commandGateway owns the downlink path (registry lookup, encoding, ACK capture,
// persistence and result fan-out).
type commandGateway struct {
	srv        *Server
	tenants    *tenant.Manager
	nats       *internal.NATSClient
	persistent bool

	ackTimeout time.Duration
	// maxAge drops a command that is older than this before it ever reaches a
	// device. It exists because the durable consumer redelivers the whole stream
	// when JetStream recreates it (config change / new consumer): without a bound,
	// an `engine_cut` issued hours ago would be re-sent to a moving vehicle — a
	// safety hazard, not just noise.
	maxAge time.Duration

	// onResult is an optional sink for the final outcome (tests / diagnostics).
	onResult func(models.CommandResult)

	mu      sync.Mutex
	pending map[string]*pendingCommand // key: request_id
}

// StartCommandDispatch subscribes to `command.request.>` (queue group `command`)
// and starts the ACK-timeout sweeper. It returns the subscription so main can
// unsubscribe on shutdown.
func (s *Server) StartCommandDispatch() (*nats.Subscription, error) {
	g := &commandGateway{
		srv:        s,
		tenants:    s.tenants,
		nats:       s.nats,
		persistent: s.tenants != nil,
		ackTimeout: time.Duration(envInt("COMMAND_ACK_TIMEOUT_SECONDS", 30)) * time.Second,
		maxAge:     time.Duration(envInt("COMMAND_MAX_AGE_SECONDS", 300)) * time.Second,
		pending:    make(map[string]*pendingCommand),
	}
	s.gateway = g
	go g.timeoutLoop(s.ctx)

	// Preferred path: a DURABLE JetStream consumer, so a command published while this
	// service was restarting is delivered instead of lost (B8 gap "core NATS").
	if sub, err := s.nats.QueueSubscribeDurable(internal.StreamCommand,
		"command.request.>", "command", "ingestion-command-dispatch", g.handleRequest); err == nil {
		slog.Info("downlink command dispatcher started",
			"subject", "command.request.>", "queue", "command", "durable", "ingestion-command-dispatch",
			"ack_timeout_s", g.ackTimeout.Seconds(), "max_age_s", g.maxAge.Seconds(),
			"persist", g.persistent, "delivery", "jetstream-durable")
		return sub, nil
	} else {
		slog.Warn("downlink: durable consumer unavailable, falling back to core NATS",
			"error", err)
	}

	sub, err := s.nats.Subscribe("command.request.>", "command", g.handleRequest)
	if err != nil {
		return nil, err
	}
	slog.Info("downlink command dispatcher started",
		"subject", "command.request.>", "queue", "command",
		"ack_timeout_s", g.ackTimeout.Seconds(), "max_age_s", g.maxAge.Seconds(),
		"persist", g.persistent, "delivery", "core-nats")
	return sub, nil
}

// handleRequest decodes, validates and dispatches one downlink request.
//
// Validation is repeated here on purpose: the NATS subject is internal, but a
// malformed or hostile payload must never be encoded onto a device socket.
func (g *commandGateway) handleRequest(msg *nats.Msg) error {
	var cmd models.DeviceCommand
	if err := json.Unmarshal(msg.Data, &cmd); err != nil {
		slog.Warn("downlink: undecodable request dropped", "subject", msg.Subject, "error", err)
		return nil // a malformed payload must not be re-delivered forever
	}
	cmd.CompanyCode = strings.ToUpper(strings.TrimSpace(cmd.CompanyCode))
	if err := cmd.Validate(); err != nil {
		slog.Warn("downlink: invalid request rejected", "company", cmd.CompanyCode,
			"imei", cmd.IMEI, "error", err)
		g.finish(context.Background(), cmd, models.CommandStatusFailed, err.Error(), "")
		return nil
	}
	// The subject carries the tenant; a payload claiming another company is
	// rejected (cross-tenant command injection, PRD §9.6).
	if want := models.CommandSubjectRequest(cmd.CompanyCode); msg.Subject != want {
		slog.Error("downlink: subject/company mismatch rejected",
			"subject", msg.Subject, "company", cmd.CompanyCode)
		return nil
	}
	if cmd.RequestID == "" {
		slog.Warn("downlink: request without request_id rejected", "imei", cmd.IMEI)
		return nil
	}
	if cmd.CreatedAt.IsZero() {
		cmd.CreatedAt = time.Now().UTC()
	}

	ctx := context.Background()
	// A redelivered backlog entry (JetStream recreates a consumer → DeliverAll) must
	// never reach a device after its window: an hours-old `engine_cut` re-executed on
	// a moving vehicle is a safety hazard, not just noise. The drop is recorded, so
	// the operator sees why the command did not go out.
	if g.maxAge > 0 {
		if age := time.Since(cmd.CreatedAt); age > g.maxAge {
			slog.Warn("downlink: stale request dropped", "company", cmd.CompanyCode,
				"imei", cmd.IMEI, "command", cmd.Kind, "request_id", cmd.RequestID,
				"age_s", age.Seconds(), "max_age_s", g.maxAge.Seconds())
			g.finish(ctx, cmd, models.CommandStatusFailed,
				fmt.Sprintf("request is older than COMMAND_MAX_AGE_SECONDS (%s) — not sent", g.maxAge), "")
			return nil
		}
	}

	if cmd.VehicleID <= 0 && g.tenants != nil {
		if dev, err := g.tenants.ResolveDeviceByIMEI(ctx, cmd.IMEI); err == nil {
			cmd.VehicleID = dev.VehicleID
		}
	}

	result := g.dispatch(cmd)
	g.finish(ctx, cmd, result.Status, result.Detail, result.ACK)
	return nil
}

// dispatch writes the encoded command to the live device connection and reports
// the immediate outcome (sent / offline / failed). A `sent` outcome registers
// the command in the pending set so the reply or the timeout can complete it.
func (g *commandGateway) dispatch(cmd models.DeviceCommand) models.CommandResult {
	res := models.CommandResult{
		RequestID: cmd.RequestID, CommandID: cmd.ID, CompanyCode: cmd.CompanyCode,
		VehicleID: cmd.VehicleID, IMEI: cmd.IMEI, Kind: cmd.Kind, At: time.Now().UTC(),
	}

	dc, ok := g.srv.Conns().Get(cmd.IMEI)
	if !ok {
		res.Status, res.Detail = models.CommandStatusOffline, "device has no live connection"
		slog.Warn("downlink: device offline", "imei", cmd.IMEI, "command", cmd.Kind)
		return res
	}
	protoName := dc.Protocol.String()

	// One in-flight command per device: a GT06 reply carries no request id, so with
	// two outstanding commands the answer is ambiguous and could ack the wrong one
	// (the live E2E run hit exactly that). The newer command is refused explicitly,
	// never silently dropped, and the device socket is untouched.
	if inflight := g.inflightFor(cmd.IMEI); inflight != nil {
		res.Status = models.CommandStatusFailed
		res.Detail = "device already has a command awaiting its reply (request_id " +
			inflight.cmd.RequestID + ")"
		slog.Warn("downlink: command refused, device busy", "imei", cmd.IMEI,
			"command", cmd.Kind, "request_id", cmd.RequestID,
			"inflight_request_id", inflight.cmd.RequestID)
		return res
	}

	dec, ok := DecoderFor(dc.Protocol)
	if !ok {
		res.Status = models.CommandStatusFailed
		res.Detail = "no decoder registered for protocol " + protoName
		return res
	}
	encoder, ok := dec.(CommandEncoder)
	if !ok {
		res.Status, res.Detail = models.CommandStatusFailed,
			ErrCommandUnsupported.Error()+" ("+protoName+")"
		return res
	}
	frame, err := encoder.EncodeCommand(cmd)
	if err != nil {
		res.Status, res.Detail = models.CommandStatusFailed, "encode: "+err.Error()
		slog.Error("downlink: encode failed", "imei", cmd.IMEI, "protocol", protoName, "error", err)
		return res
	}
	if err := dc.Write(frame); err != nil {
		res.Status, res.Detail = models.CommandStatusFailed, "write: "+err.Error()
		slog.Error("downlink: write failed", "imei", cmd.IMEI, "protocol", protoName, "error", err)
		return res
	}

	g.mu.Lock()
	g.pending[cmd.RequestID] = &pendingCommand{cmd: cmd, sentAt: time.Now().UTC(), proto: dc.Protocol}
	commandsPending.Set(float64(len(g.pending)))
	g.mu.Unlock()

	res.Status = models.CommandStatusSent
	res.Detail = "frame written to " + protoName + " device"
	slog.Info("downlink: command sent", "imei", cmd.IMEI, "protocol", protoName,
		"command", cmd.Kind, "request_id", cmd.RequestID, "frame_bytes", len(frame))
	return res
}

// inflightFor returns the command currently awaiting the device's reply for an
// IMEI (nil when the device is free). Only commands inside the ACK window count: an
// expired entry is the sweeper's responsibility, not a permanent lock.
func (g *commandGateway) inflightFor(imei string) *pendingCommand {
	g.mu.Lock()
	defer g.mu.Unlock()
	cutoff := time.Now().UTC().Add(-g.ackTimeout)
	for _, p := range g.pending {
		if p.cmd.IMEI == imei && p.sentAt.After(cutoff) {
			return p
		}
	}
	return nil
}

// Ack records the device's online-command reply (GT06 0x21/0x15). It is called
// from the protocol handler with the content the terminal echoed back.
func (g *commandGateway) Ack(imei, content string) {
	g.mu.Lock()
	var (
		found     *pendingCommand
		requestID string
	)
	for id, p := range g.pending {
		if p.cmd.IMEI != imei {
			continue
		}
		if found == nil || p.sentAt.Before(found.sentAt) {
			found, requestID = p, id
		}
	}
	if found != nil {
		delete(g.pending, requestID)
		commandsPending.Set(float64(len(g.pending)))
	}
	g.mu.Unlock()

	if found == nil {
		slog.Debug("downlink: unsolicited device reply", "imei", imei, "content", content)
		return
	}

	ok, detail := parseGT06CommandReply(content)
	status := models.CommandStatusAcked
	result := "ok"
	if !ok {
		status, result = models.CommandStatusFailed, "error"
	}
	commandsAcked.WithLabelValues(result).Inc()
	slog.Info("downlink: device acknowledged command", "imei", imei,
		"command", found.cmd.Kind, "request_id", requestID, "status", status, "detail", detail)
	g.finish(context.Background(), found.cmd, status, detail, content)
}

// timeoutLoop expires commands whose device never replied (FR-1.3 style
// keep-alive discipline: an unanswered command must not stay `sent` forever).
func (g *commandGateway) timeoutLoop(ctx context.Context) {
	every := time.Duration(envInt("COMMAND_SWEEP_SECONDS", 5)) * time.Second
	if every <= 0 {
		every = 5 * time.Second
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			g.sweepExpired()
		}
	}
}

// sweepExpired finalises every pending command older than the ACK window.
func (g *commandGateway) sweepExpired() {
	cutoff := time.Now().UTC().Add(-g.ackTimeout)

	g.mu.Lock()
	var expired []*pendingCommand
	for id, p := range g.pending {
		if p.sentAt.Before(cutoff) {
			expired = append(expired, p)
			delete(g.pending, id)
		}
	}
	commandsPending.Set(float64(len(g.pending)))
	g.mu.Unlock()

	for _, p := range expired {
		slog.Warn("downlink: device reply timeout", "imei", p.cmd.IMEI,
			"command", p.cmd.Kind, "request_id", p.cmd.RequestID,
			"waited_s", time.Since(p.sentAt).Seconds())
		g.finish(context.Background(), p.cmd, models.CommandStatusTimeout,
			"no device reply within the ACK window", "")
	}
}

// finish publishes the result and persists it (best effort — a database outage
// never blocks device control, but it is always logged, never swallowed).
func (g *commandGateway) finish(ctx context.Context, cmd models.DeviceCommand, status, detail, ack string) {
	commandsDispatched.WithLabelValues(status).Inc()

	res := models.CommandResult{
		RequestID: cmd.RequestID, CommandID: cmd.ID, CompanyCode: cmd.CompanyCode,
		VehicleID: cmd.VehicleID, IMEI: cmd.IMEI, Kind: cmd.Kind,
		Status: status, Detail: detail, ACK: ack, At: time.Now().UTC(),
	}
	if !cmd.CreatedAt.IsZero() {
		res.LatencyMS = res.At.Sub(cmd.CreatedAt).Milliseconds()
	}
	// The NATS client is required in production (main refuses to boot without it);
	// the guard keeps the gateway panic-free when it is constructed bare (tests).
	if g.nats != nil {
		if payload, err := json.Marshal(res); err == nil {
			if err := g.nats.PublishJetStream(models.CommandSubjectResult(cmd.CompanyCode), payload); err != nil {
				slog.Warn("downlink: result publish failed", "company", cmd.CompanyCode,
					"request_id", cmd.RequestID, "error", err)
			}
		}
	}
	g.persist(ctx, cmd, res)

	// Test/observability hook: nothing is attached in production, but it lets a test
	// assert the recorded outcome without a database (see commands_test.go).
	if g.onResult != nil {
		g.onResult(res)
	}
}
