package controllers

import (
	"context"
	"log/slog"
	"time"

	"ajb_gps/worker-live/models"
)

// poke signals the flusher to run immediately (non-blocking).
func (w *Worker) poke() {
	select {
	case w.flushCh <- struct{}{}:
	default:
	}
}

// flusher writes the buffer every LIVE_BATCH_INTERVAL_MS or when poked
// (FR-2.3: 100 ms batching → ~100× fewer Redis round trips).
func (w *Worker) flusher() {
	interval := w.cfg.Live.BatchInterval
	if interval <= 0 {
		interval = models.FlushInterval
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-w.flushCh:
			w.flushBuffer()
		case <-tick.C:
			w.mu.Lock()
			nonEmpty := len(w.buffer) > 0
			w.mu.Unlock()
			if nonEmpty {
				w.flushBuffer()
			}
		case <-w.ctx.Done():
			return
		}
	}
}

// flushBuffer writes the whole buffer in ONE MSET with the state TTL (FR-2.3).
func (w *Worker) flushBuffer() {
	w.mu.Lock()
	if len(w.buffer) == 0 {
		w.mu.Unlock()
		return
	}
	snapshot := w.buffer
	w.buffer = make(map[string]string, w.cfg.Live.MaxBatch)
	w.mu.Unlock()

	ttl := w.cfg.Redis.TTL
	if ttl <= 0 {
		ttl = models.StateTTL
	}

	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		redisBatchSize.Set(float64(len(snapshot)))
		if err := w.red.MSetBatch(context.WithoutCancel(w.ctx), snapshot, ttl); err != nil {
			slog.Error("worker-live: redis MSET failed", "keys", len(snapshot), "error", err)
			return
		}
		stateFlushes.Inc()
		slog.Debug("worker-live: flushed live state", "keys", len(snapshot))
	}()
}
