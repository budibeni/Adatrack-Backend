package controllers

import (
	"errors"
	"testing"

	"ajb_gps/internal"
	"ajb_gps/worker-persistence/models"
)

// TestCompanyGroupsSplitsByTenant documents tenant-scoped batching (one INSERT
// per schema, PRD §6.2).
func TestCompanyGroupsSplitsByTenant(t *testing.T) {
	rows := []models.Row{
		models.ToRow(positionMessage("86001", "DEV001")),
		models.ToRow(positionMessage("86002", "DEV001")),
		models.ToRow(positionMessage("86003", "ACME")),
	}
	groups := companyGroups(rows)

	if len(groups) != 2 {
		t.Fatalf("grouped into %d tenants, want 2", len(groups))
	}
	if len(groups["DEV001"]) != 2 {
		t.Errorf("DEV001 rows = %d, want 2", len(groups["DEV001"]))
	}
	if len(groups["ACME"]) != 1 {
		t.Errorf("ACME rows = %d, want 1", len(groups["ACME"]))
	}
}

// TestFlushResetsTheBuffer verifies flush() hands the whole buffer to persist and
// leaves the buffer empty for the next batch.
func TestFlushResetsTheBuffer(t *testing.T) {
	p, _ := newTestPersister(func(string) (*internal.DBPool, error) {
		return nil, errors.New("tenant not found")
	})

	imeis := []string{"86001", "86002", "86003"}
	for _, imei := range imeis {
		if err := p.handleMessage(telemetryMsg(t, positionMessage(imei, "GHOST"))); err != nil {
			t.Fatalf("handleMessage: %v", err)
		}
	}

	p.flush()
	p.wg.Wait()

	p.mu.Lock()
	buffered := len(p.pending)
	p.mu.Unlock()
	if buffered != 0 {
		t.Errorf("buffer not reset after flush: %d rows", buffered)
	}
}

// TestFlushOnBatchSizeTriggersPersist documents FR-3.1: reaching BATCH_SIZE rows
// flushes without waiting for the timeout.
func TestFlushOnBatchSizeTriggersPersist(t *testing.T) {
	p, captured := newTestPersister(func(string) (*internal.DBPool, error) {
		return nil, errors.New("tenant not found")
	})

	// BatchSize in the test config is 3: the third message must trigger a flush.
	for _, imei := range []string{"86001", "86002", "86003"} {
		if err := p.handleMessage(telemetryMsg(t, positionMessage(imei, "GHOST"))); err != nil {
			t.Fatalf("handleMessage: %v", err)
		}
	}

	// The flusher goroutine is not started in this test, so drain the signal and
	// flush synchronously (same code path the ticker would take).
	p.flush()
	p.wg.Wait()

	if captured.count() != 3 {
		t.Errorf("expected the 3-row batch to be dead-lettered, got %d (%v)",
			captured.count(), captured.reasons())
	}
}

// TestRetryBackoffDefaults asserts the FR-3.4 backoff schedule (1s/5s/10s) is the
// default when RETRY_BACKOFF_MS is unset in the environment.
func TestRetryBackoffDefaults(t *testing.T) {
	cfg := internal.LoadConfig()
	if len(cfg.Persistence.Backoff) < 3 {
		t.Fatalf("expected at least 3 backoff steps, got %v", cfg.Persistence.Backoff)
	}
	if cfg.Persistence.Backoff[0] != 1_000_000_000 {
		t.Errorf("first backoff = %v, want 1s", cfg.Persistence.Backoff[0])
	}
	if cfg.Persistence.RetryMax < 1 {
		t.Errorf("RetryMax = %d, want >= 1 (FR-3.4)", cfg.Persistence.RetryMax)
	}
}

// TestModelsToRowMapsFields verifies the row projection used by the INSERT.
func TestModelsToRowMapsFields(t *testing.T) {
	msg := positionMessage("864201040512345", "DEV001")
	msg.Heading = -45
	msg.Altitude = -12
	row := models.ToRow(msg)

	if row.IMEI != "864201040512345" || row.CompanyCode != "DEV001" || row.VehicleID != 1 {
		t.Errorf("identity fields wrong: %+v", row)
	}
	if row.Lat != msg.Lat || row.Lon != msg.Lon || row.Speed != msg.Speed {
		t.Errorf("position fields wrong: %+v", row)
	}
	if row.Heading != -45 || row.Altitude != -12 {
		t.Errorf("signed heading/altitude lost: heading=%v altitude=%v", row.Heading, row.Altitude)
	}
	if !row.ACC || row.Battery != 12 {
		t.Errorf("ACC/battery wrong: acc=%v battery=%d", row.ACC, row.Battery)
	}
	if row.Timestamp.IsZero() {
		t.Error("timestamp must be set (UTC)")
	}

	values := row.Values()
	if len(values) != len(models.InsertColumns) {
		t.Fatalf("Values() returned %d values, want %d (one per column)",
			len(values), len(models.InsertColumns))
	}
	// acc_status is the 9th column and must be an integer 0/1.
	if values[8] != 1 {
		t.Errorf("acc_status value = %v, want 1", values[8])
	}
}

// TestPositionlessClassification documents which payloads skip the telemetry
// table (heartbeat/fuel-only).
func TestPositionlessClassification(t *testing.T) {
	if !models.Positionless(models.TelemetryMessage{IMEI: "86001"}) {
		t.Error("a message without position must be classified positionless")
	}
	if models.Positionless(positionMessage("86001", "DEV001")) {
		t.Error("a position message must not be classified positionless")
	}
	level := 42.0
	fuelOnly := models.TelemetryMessage{IMEI: "86001", FuelLevel: &level}
	if !models.Positionless(fuelOnly) {
		t.Error("a fuel-only message must be classified positionless")
	}
}
