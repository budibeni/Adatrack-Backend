package controllers

import (
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"adatrack_gps/internal"
	"adatrack_gps/worker-persistence/models"
)

// fuelMessage builds a fuel-bearing payload (B5a FR-7.4/FR-7.6).
func fuelMessage(imei, company string, level float64) models.TelemetryMessage {
	return models.TelemetryMessage{
		IMEI: imei, CompanyCode: company, VehicleID: 1,
		FuelLevel: &level, ACC: true, Timestamp: time.Now().Unix(),
	}
}

// TestFlushWithEmptyBuffersIsNoop documents that a flush without buffered rows
// never touches the database (the ticker fires every BATCH_TIMEOUT_SEC).
func TestFlushWithEmptyBuffersIsNoop(t *testing.T) {
	p, captured := newTestPersister(func(string) (*internal.DBPool, error) {
		t.Error("routing must not be attempted for an empty flush")
		return nil, errors.New("unreachable")
	})
	p.flush()
	p.wg.Wait()
	if captured.count() != 0 {
		t.Errorf("empty flush published %d dead letters", captured.count())
	}
}

// TestDefaultSeamsDeadLetterWithoutInfra covers the production default seams:
// with no tenant manager and no NATS client every row is still dead-lettered
// (counted in metrics) instead of being dropped silently — and nothing panics.
func TestDefaultSeamsDeadLetterWithoutInfra(t *testing.T) {
	cfg := internal.LoadConfig()
	cfg.Persistence.BatchSize = 2
	cfg.Persistence.BatchTimeout = time.Hour // the ticker must not fire here
	p := New(cfg, nil, nil)                  // no tenants, no nats: default seams

	for _, imei := range []string{"86001", "86002"} {
		if err := p.handleMessage(telemetryMsg(t, positionMessage(imei, "DEV001"))); err != nil {
			t.Fatalf("handleMessage: %v", err)
		}
	}
	p.flush()
	p.wg.Wait()

	p.mu.Lock()
	pending := len(p.pending)
	p.mu.Unlock()
	if pending != 0 {
		t.Errorf("buffer not drained after flush: %d rows", pending)
	}
}

// TestStopDrainsPendingRows covers the graceful-shutdown drain (FR-3.1): rows
// buffered at shutdown are persisted before Stop returns.
func TestStopDrainsPendingRows(t *testing.T) {
	p, captured := newTestPersister(func(string) (*internal.DBPool, error) {
		return nil, errors.New("tenant not found")
	})

	for _, imei := range []string{"86001", "86002"} {
		if err := p.handleMessage(telemetryMsg(t, positionMessage(imei, "GHOST"))); err != nil {
			t.Fatalf("handleMessage: %v", err)
		}
	}
	p.Stop()
	p.wg.Wait()

	if captured.count() != 2 {
		t.Fatalf("shutdown drain published %d dead letters, want 2 (%v)",
			captured.count(), captured.reasons())
	}
	for _, reason := range captured.reasons() {
		if reason != "tenant:routing" {
			t.Errorf("dead-letter reason = %q, want tenant:routing", reason)
		}
	}
}

// TestFlushPersistsFuelGroups covers the B5a fuel split: fuel-bearing rows are
// grouped per tenant and persisted independently of the telemetry batch.
func TestFlushPersistsFuelGroups(t *testing.T) {
	p, captured := newTestPersister(func(string) (*internal.DBPool, error) {
		return nil, errors.New("tenant not found")
	})

	// BatchSize is 3 in the test config: three fuel rows must buffer + poke.
	for _, imei := range []string{"86001", "86002", "86003"} {
		if err := p.handleMessage(telemetryMsg(t, fuelMessage(imei, "GHOST", 42.5))); err != nil {
			t.Fatalf("handleMessage: %v", err)
		}
	}

	p.mu.Lock()
	fuelBuffered := len(p.fuelPending)
	p.mu.Unlock()
	if fuelBuffered != 3 {
		t.Fatalf("buffered %d fuel rows, want 3", fuelBuffered)
	}
	if len(p.flushCh) != 1 {
		t.Errorf("reaching BATCH_SIZE must poke the flusher (pending signals = %d)", len(p.flushCh))
	}

	p.flush()
	p.wg.Wait()

	p.mu.Lock()
	fuelLeft := len(p.fuelPending)
	p.mu.Unlock()
	if fuelLeft != 0 {
		t.Errorf("fuel buffer not drained: %d rows", fuelLeft)
	}
	if captured.count() != 3 {
		t.Fatalf("fuel flush published %d dead letters, want 3 (%v)",
			captured.count(), captured.reasons())
	}
}

// TestFuelOnlyRowsSkipTheTelemetryBuffer documents FR-3.4: a fuel-only packet
// goes to td_fuel_logs and never to th_telemetry_logs.
func TestFuelOnlyRowsSkipTheTelemetryBuffer(t *testing.T) {
	p, _ := newTestPersister(func(string) (*internal.DBPool, error) {
		return nil, errors.New("unreachable")
	})
	if err := p.handleMessage(telemetryMsg(t, fuelMessage("86001", "DEV001", 10))); err != nil {
		t.Fatalf("handleMessage: %v", err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.pending) != 0 {
		t.Errorf("fuel-only packet buffered %d telemetry rows, want 0", len(p.pending))
	}
	if len(p.fuelPending) != 1 {
		t.Errorf("fuel-only packet buffered %d fuel rows, want 1", len(p.fuelPending))
	}
}

// TestPokeIsNonBlocking covers the bounded signal channel: the second poke of an
// already-signalled flusher must not block the NATS callback goroutine.
func TestPokeIsNonBlocking(t *testing.T) {
	p, _ := newTestPersister(func(string) (*internal.DBPool, error) { return nil, nil })
	p.poke()
	p.poke()
	if len(p.flushCh) != 1 {
		t.Errorf("flush signals = %d, want exactly 1 pending", len(p.flushCh))
	}
}

// TestFlusherTickerFlushesBufferedRows covers the BATCH_TIMEOUT_SEC path of the
// flush loop: rows buffered without reaching BATCH_SIZE are written by the
// ticker (FR-3.1).
func TestFlusherTickerFlushesBufferedRows(t *testing.T) {
	p, captured := newTestPersister(func(string) (*internal.DBPool, error) {
		return nil, errors.New("tenant not found")
	})
	p.cfg.Persistence.BatchTimeout = 20 * time.Millisecond

	if err := p.handleMessage(telemetryMsg(t, positionMessage("86001", "GHOST"))); err != nil {
		t.Fatalf("handleMessage: %v", err)
	}
	p.flushCh = make(chan struct{}, 1) // drop the batch-size poke: only the ticker remains
	go p.flusher()
	t.Cleanup(p.Stop)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if captured.count() == 1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("ticker flush did not run (dead letters=%d)", captured.count())
}

// TestDefaultResolveCompanyDBRequiresTenants covers the routing guard and the
// publish no-op when no NATS client is wired (dev/test boot).
func TestDefaultResolveCompanyDBRequiresTenants(t *testing.T) {
	p := New(internal.LoadConfig(), nil, nil)
	if _, err := p.defaultResolveCompanyDB("DEV001"); err == nil {
		t.Error("routing without a tenant manager must fail (no silent fallback)")
	}
	p.defaultPublishError("86001", []byte("x")) // no NATS client: must be a no-op
}

// TestOrUnknownSubjectFallback covers the subject-safe IMEI fallback.
func TestOrUnknownSubjectFallback(t *testing.T) {
	if got := orUnknown(""); got != "unknown" {
		t.Errorf("orUnknown(\"\") = %q, want unknown", got)
	}
	if got := orUnknown("86001"); got != "86001" {
		t.Errorf("orUnknown(86001) = %q, want 86001", got)
	}
}

// TestFuelRowProjection verifies the td_fuel_logs projection + NULL binding.
func TestFuelRowProjection(t *testing.T) {
	level := 55.5
	msg := models.TelemetryMessage{
		IMEI: "86001", CompanyCode: "DEV001", VehicleID: 7,
		FuelLevel: &level, Lat: -6.2, Lon: 106.8, ACC: true,
		Timestamp: time.Now().Unix(),
	}
	if !msg.HasFuel() {
		t.Fatal("HasFuel must report true for a fuel-bearing payload")
	}
	row := models.ToFuelRow(msg)
	if row.FuelLevel == nil || *row.FuelLevel != level {
		t.Errorf("fuel level lost: %+v", row)
	}
	if row.FuelVolume != nil || row.FuelTempC != nil {
		t.Errorf("absent readings must stay NULL (absent ≠ zero): %+v", row)
	}
	values := row.Values()
	if len(values) != len(models.FuelInsertColumns) {
		t.Fatalf("Values() returned %d values, want %d", len(values), len(models.FuelInsertColumns))
	}
	if values[8] != 1 {
		t.Errorf("acc_status = %v, want 1", values[8])
	}
	// A nil *float64 binds as a typed nil: SQL NULL requires exactly that.
	if vol, ok := values[4].(*float64); !ok || vol != nil {
		t.Errorf("fuel_volume = %v, want a nil *float64 (NULL binding)", values[4])
	}
	if !row.Timestamp.Equal(time.Unix(msg.Timestamp, 0).UTC()) {
		t.Errorf("timestamp = %v, want the UTC device time", row.Timestamp)
	}
}

// TestFuelRowTimestampFallback covers the server-time fallback of a fuel row.
func TestFuelRowTimestampFallback(t *testing.T) {
	row := models.ToFuelRow(models.TelemetryMessage{IMEI: "86001", Timestamp: 0})
	if row.Timestamp.IsZero() {
		t.Error("a missing device timestamp must fall back to the server time")
	}
	if models.ToRow(models.TelemetryMessage{IMEI: "86001", Timestamp: 0}).Timestamp.IsZero() {
		t.Error("a telemetry row without a device timestamp must use the server time")
	}
}

// TestRegisterMetrics covers the /metrics wiring of the persistence collectors.
// Label-carrying vectors only export a family once a child exists, so one child
// is created before gathering (exactly what the worker does on its first batch).
func TestRegisterMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	RegisterMetrics(reg)
	messagesProcessed.WithLabelValues("ITCOV").Inc()
	batchInsertErrors.WithLabelValues("ITCOV").Inc()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	seen := map[string]bool{}
	for _, f := range families {
		seen[f.GetName()] = true
	}
	for _, name := range []string{
		"messages_processed_total", "batch_insert_size", "batch_insert_errors_total",
		"retry_attempts_total", "tenant_routing_duration_ms", "persistence_deadletter_total",
		"positionless_rows_total", "persistence_pending_rows", "persistence_fuel_pending_rows",
	} {
		if !seen[name] {
			t.Errorf("collector %s not registered", name)
		}
	}
}
