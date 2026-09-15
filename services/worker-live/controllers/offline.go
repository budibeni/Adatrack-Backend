package controllers

import (
	"encoding/json"
	"log/slog"
	"time"

	"ajb_gps/internal"
	"ajb_gps/worker-live/models"
)

// sweepInterval is how often the staleness sweeper inspects live states; it is
// deliberately shorter than the OFFLINE threshold so the transition is timely.
const sweepInterval = 30 * time.Second

// shouldMarkOffline reports whether a live state is stale enough to be flagged
// OFFLINE (FR-2.2). It is pure so the state machine is unit-testable without
// Redis: a state already OFFLINE is never re-flagged, and a state must exceed
// the offline window (not merely reach it).
func shouldMarkOffline(st models.LiveState, now int64, offlineAfter time.Duration) bool {
	if st.Status == models.StatusOffline {
		return false
	}
	if offlineAfter <= 0 {
		offlineAfter = models.OfflineAfter
	}
	return now-st.LastSeen > int64(offlineAfter.Seconds())
}

// sweeper marks vehicles OFFLINE once their last message is older than
// OFFLINE_AFTER_MINUTES (FR-2.2). Without it a parked device would keep showing
// the status written at its last message (which is always "fresh" by definition).
//
// The sweep is bounded: keys are scanned in batches and each state is updated
// with a single MSET, so it stays cheap for the ≤5000 device target.
func (w *Worker) sweeper() {
	interval := internal.EnvIntDefault("LIVE_SWEEP_INTERVAL_SEC", int(sweepInterval.Seconds()))
	if interval <= 0 {
		interval = int(sweepInterval.Seconds())
	}
	tick := time.NewTicker(time.Duration(interval) * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-w.ctx.Done():
			return
		case <-tick.C:
			w.sweepOnce()
		}
	}
}

// sweepOnce performs one staleness pass over every tenant live-state key.
func (w *Worker) sweepOnce() {
	pattern := w.red.KeyPrefix() + "*:vehicle:state:*"
	keys, err := w.red.ScanKeys(w.ctx, pattern, 500)
	if err != nil {
		slog.Warn("worker-live: live-state scan failed", "pattern", pattern, "error", err)
		return
	}
	if len(keys) == 0 {
		return
	}

	offline := w.offlineAfter()
	now := time.Now().Unix()
	updates := make(map[string]string)
	transitioned := make([]string, 0, len(keys))

	for _, key := range keys {
		raw, gerr := w.red.Get(w.ctx, key)
		if gerr != nil || raw == "" {
			continue
		}
		var st models.LiveState
		if uerr := json.Unmarshal([]byte(raw), &st); uerr != nil {
			slog.Warn("worker-live: unreadable live state during sweep", "key", key, "error", uerr)
			continue
		}
		if st.Status == models.StatusOffline {
			continue
		}
		if !shouldMarkOffline(st, now, offline) {
			continue
		}

		st.Status = models.StatusOffline
		payload, merr := json.Marshal(st)
		if merr != nil {
			slog.Warn("worker-live: failed to encode OFFLINE state", "key", key, "error", merr)
			continue
		}
		updates[key] = string(payload)
		transitioned = append(transitioned, st.IMEI)
	}

	if len(updates) == 0 {
		return
	}

	ttl := w.cfg.Redis.TTL
	if ttl <= 0 {
		ttl = models.StateTTL
	}
	if err := w.red.MSetBatch(w.ctx, updates, ttl); err != nil {
		slog.Error("worker-live: failed to persist OFFLINE transitions", "keys", len(updates), "error", err)
		return
	}
	offlineTransitions.Add(float64(len(updates)))

	// Notify subscribers so the dashboard sees the transition immediately.
	for _, payload := range updates {
		var st models.LiveState
		if err := json.Unmarshal([]byte(payload), &st); err != nil {
			continue
		}
		if err := w.publishLive(st.IMEI, st); err != nil {
			slog.Warn("worker-live: OFFLINE live publish failed", "imei", st.IMEI, "error", err)
		}
	}

	slog.Info("worker-live: OFFLINE transitions", "count", len(updates),
		"imeis", transitioned, "offline_after_min", offline.Minutes())
}
