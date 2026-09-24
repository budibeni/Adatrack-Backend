package controllers

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"adatrack_gps/internal"
	"adatrack_gps/worker-persistence/models"
)

// newTestPersister builds a persister with stubbed routing/publishing so the
// batch + retry + dead-letter logic is testable without live infrastructure.
func newTestPersister(resolve func(string) (*internal.DBPool, error)) (*Persister, *capturePublisher) {
	cfg := internal.LoadConfig()
	cfg.Persistence.BatchSize = 3
	cfg.Persistence.BatchTimeout = 50 * time.Millisecond

	p := New(cfg, nil, nil)
	p.resolveCompanyDB = resolve
	captured := &capturePublisher{}
	p.publishError = captured.publish
	return p, captured
}

// capturePublisher records dead-letter publications.
type capturePublisher struct {
	mu       sync.Mutex
	messages []capturedMessage
}

// capturedMessage is one dead-letter publication.
type capturedMessage struct {
	IMEI    string
	Payload string
}

// publish records a payload.
func (c *capturePublisher) publish(imei string, payload []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.messages = append(c.messages, capturedMessage{IMEI: imei, Payload: string(payload)})
}

// count returns the number of recorded messages.
func (c *capturePublisher) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.messages)
}

// reasons returns the recorded payloads.
func (c *capturePublisher) reasons() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.messages))
	for _, m := range c.messages {
		out = append(out, m.Payload)
	}
	return out
}

// telemetryMsg builds a NATS message carrying a telemetry payload.
func telemetryMsg(t *testing.T, msg models.TelemetryMessage) *nats.Msg {
	t.Helper()
	payload, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal telemetry: %v", err)
	}
	return &nats.Msg{Subject: "telemetry.raw." + msg.IMEI, Data: payload}
}

// positionMessage is a valid position payload.
func positionMessage(imei, company string) models.TelemetryMessage {
	return models.TelemetryMessage{
		IMEI: imei, CompanyCode: company, VehicleID: 1,
		Lat: -6.2088, Lon: 106.8456, Speed: 40, ACC: models.BoolPtr(true),
		Battery: 12, Timestamp: time.Now().Unix(),
	}
}

// TestHandleMessageBuffersPositionRows verifies the batch buffer grows (FR-3.1).
func TestHandleMessageBuffersPositionRows(t *testing.T) {
	p, captured := newTestPersister(func(string) (*internal.DBPool, error) {
		return nil, errors.New("unused in this test")
	})

	if err := p.handleMessage(telemetryMsg(t, positionMessage("86001", "DEV001"))); err != nil {
		t.Fatalf("handleMessage: %v", err)
	}
	if err := p.handleMessage(telemetryMsg(t, positionMessage("86002", "DEV001"))); err != nil {
		t.Fatalf("handleMessage: %v", err)
	}

	p.mu.Lock()
	buffered := len(p.pending)
	p.mu.Unlock()
	if buffered != 2 {
		t.Errorf("buffered %d rows, want 2", buffered)
	}
	if captured.count() != 0 {
		t.Errorf("no dead-letter expected while buffering, got %v", captured.reasons())
	}
}

// TestHandleMessageSkipsPositionlessRows documents FR-3.4: heartbeat/fuel-only
// packets never reach th_telemetry_logs.
func TestHandleMessageSkipsPositionlessRows(t *testing.T) {
	p, captured := newTestPersister(func(string) (*internal.DBPool, error) {
		return nil, errors.New("unreachable")
	})

	heartbeat := models.TelemetryMessage{IMEI: "86001", CompanyCode: "DEV001", Timestamp: time.Now().Unix()}
	if err := p.handleMessage(telemetryMsg(t, heartbeat)); err != nil {
		t.Fatalf("handleMessage: %v", err)
	}

	p.mu.Lock()
	buffered := len(p.pending)
	p.mu.Unlock()
	if buffered != 0 {
		t.Errorf("positionless row was buffered (%d rows)", buffered)
	}
	if captured.count() != 0 {
		t.Errorf("a positionless row is skipped (counted via metrics), got %v", captured.reasons())
	}
}

// TestHandleMessageDeadLettersMissingTenant ensures a payload without tenant
// context is dead-lettered instead of being dropped (no silent drop).
func TestHandleMessageDeadLettersMissingTenant(t *testing.T) {
	p, captured := newTestPersister(func(string) (*internal.DBPool, error) {
		return nil, errors.New("unreachable")
	})

	if err := p.handleMessage(telemetryMsg(t, positionMessage("86001", ""))); err != nil {
		t.Fatalf("handleMessage: %v", err)
	}

	if captured.count() != 1 {
		t.Fatalf("expected 1 dead-letter, got %d (%v)", captured.count(), captured.reasons())
	}
	if got := captured.reasons()[0]; got != "tenant:missing" {
		t.Errorf("dead-letter reason = %q, want tenant:missing", got)
	}
}

// TestHandleMessageDeadLettersMalformedPayload covers undecodable JSON.
func TestHandleMessageDeadLettersMalformedPayload(t *testing.T) {
	p, captured := newTestPersister(func(string) (*internal.DBPool, error) {
		return nil, errors.New("unreachable")
	})

	if err := p.handleMessage(&nats.Msg{Subject: "telemetry.raw.bad", Data: []byte("{not json")}); err != nil {
		t.Fatalf("handleMessage: %v", err)
	}
	if captured.count() != 1 {
		t.Fatalf("expected 1 dead-letter for invalid JSON, got %d", captured.count())
	}
}

// TestPersistDeadLettersWhenTenantUnknown verifies the routing-failure path:
// every row is dead-lettered with a clear reason (no infinite retry).
func TestPersistDeadLettersWhenTenantUnknown(t *testing.T) {
	p, captured := newTestPersister(func(string) (*internal.DBPool, error) {
		return nil, errors.New("tenant not found")
	})

	rows := []models.Row{
		models.ToRow(positionMessage("86001", "GHOST")),
		models.ToRow(positionMessage("86002", "GHOST")),
	}
	p.persist(companyGroups(rows))

	if captured.count() != 2 {
		t.Fatalf("expected 2 dead-letters, got %d (%v)", captured.count(), captured.reasons())
	}
	for _, reason := range captured.reasons() {
		if reason != "tenant:routing" {
			t.Errorf("dead-letter reason = %q, want tenant:routing", reason)
		}
	}
}
