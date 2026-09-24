package controllers

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"adatrack_gps/internal"
	"adatrack_gps/worker-live/models"
)

// TestWorkerAppliesModelDefaults covers the FR-2.2/FR-4.4 fallbacks used when a
// service boots with an incomplete environment (no LIVE_* tuning): the buffer,
// the ONLINE→IDLE and the OFFLINE thresholds must fall back to the documented
// model constants instead of zero values.
func TestWorkerAppliesModelDefaults(t *testing.T) {
	cfg := internal.LoadConfig()
	cfg.Live.MaxBatch = 0
	cfg.Live.BatchInterval = 0
	cfg.Live.IdleAfter = 0
	cfg.Live.OfflineAfterMinutes = 0

	w := New(cfg, nil, nil, nil)
	if w.buffer == nil {
		t.Fatal("buffer map must be allocated (bounded buffer, FR-4.4)")
	}
	if got := w.idleAfter(); got != models.IdleAfter {
		t.Errorf("idleAfter = %s, want the model default %s", got, models.IdleAfter)
	}
	if got := w.offlineAfter(); got != models.OfflineAfter {
		t.Errorf("offlineAfter = %s, want the model default %s", got, models.OfflineAfter)
	}
}

// TestShouldMarkOfflineUsesModelDefault covers the same fallback inside the pure
// staleness predicate (offlineAfter <= 0).
func TestShouldMarkOfflineUsesModelDefault(t *testing.T) {
	now := time.Now().Unix()
	stale := models.LiveState{
		Status:   models.StatusOnline,
		LastSeen: now - int64(models.OfflineAfter.Seconds()) - 1,
	}
	if !shouldMarkOffline(stale, now, 0) {
		t.Error("a state past the model OFFLINE window must be flagged when no threshold is configured")
	}
	fresh := models.LiveState{Status: models.StatusOnline, LastSeen: now - 5}
	if shouldMarkOffline(fresh, now, 0) {
		t.Error("a fresh state must never be flagged OFFLINE")
	}
}

// TestFlushBufferEmptyIsNoop documents that an empty buffer never talks to Redis
// (nil client is safe because the function returns before the MSET).
func TestFlushBufferEmptyIsNoop(t *testing.T) {
	cfg := internal.LoadConfig()
	cfg.Live.MaxBatch = 8
	w := New(cfg, nil, nil, nil)
	w.flushBuffer()
	if len(w.buffer) != 0 {
		t.Errorf("empty flush left %d entries", len(w.buffer))
	}
}

// TestPokeIsNonBlocking covers the buffered-signal contract: the second poke of
// an already-signalled flusher must not block the caller (FR-2.3 fan-in).
func TestPokeIsNonBlocking(t *testing.T) {
	w := New(internal.LoadConfig(), nil, nil, nil)
	w.poke()
	w.poke() // must not block: the channel already holds one signal
	if len(w.flushCh) != 1 {
		t.Errorf("flush signal channel = %d, want exactly one pending signal", len(w.flushCh))
	}
}

// TestRegisterMetricsIsIdempotentPerRegistry covers the /metrics wiring: every
// collector is registered exactly once on the service registry (PRD §10.1).
func TestRegisterMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	RegisterMetrics(reg)
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	want := map[string]bool{
		"vehicle_state_updates_total":       false,
		"redis_batch_size":                  false,
		"live_state_published_total":        false,
		"redis_flush_total":                 false,
		"vehicle_offline_transitions_total": false,
	}
	for _, f := range families {
		if _, ok := want[f.GetName()]; ok {
			want[f.GetName()] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("collector %s not registered", name)
		}
	}
}
