// Package controllers implements the live-state worker: it consumes
// `telemetry.raw.>` and maintains the Redis live state (PRD Module 2 / FR-2.x).
package controllers

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus"

	"adatrack_gps/internal"
	"adatrack_gps/worker-live/models"
)

// Worker consumes telemetry and writes the Redis live state, fanning updates out
// to `telemetry.live.<IMEI>` for service-websocket.
type Worker struct {
	cfg  *internal.Config
	red  *internal.RedisClient
	nats *internal.NATSClient

	// buffer holds key → JSON payload, flushed as ONE MSET (FR-2.3).
	mu     sync.Mutex
	buffer map[string]string

	flushCh chan struct{}
	wg      sync.WaitGroup

	ctx    context.Context
	cancel context.CancelFunc
}

// Metrics (PRD §10.1 worker-live).
var (
	vehicleStateUpdates = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "vehicle_state_updates_total",
		Help: "Live-state updates buffered for Redis",
	})
	redisBatchSize = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "redis_batch_size",
		Help: "Keys written per Redis MSET flush",
	})
	livePublished = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "live_state_published_total",
		Help: "telemetry.live.<IMEI> messages published for service-websocket",
	})
	stateFlushes = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "redis_flush_total",
		Help: "Redis MSET flushes performed",
	})
	offlineTransitions = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "vehicle_offline_transitions_total",
		Help: "Live states transitioned to OFFLINE by the staleness sweeper (FR-2.2)",
	})
)

// RegisterMetrics registers the worker-live collectors.
func RegisterMetrics(reg *prometheus.Registry) {
	reg.MustRegister(vehicleStateUpdates, redisBatchSize, livePublished, stateFlushes,
		offlineTransitions)
}

// New builds the worker and its bounded buffer.
func New(cfg *internal.Config, red *internal.RedisClient, nats *internal.NATSClient) *Worker {
	ctx, cancel := context.WithCancel(context.Background())
	max := cfg.Live.MaxBatch
	if max <= 0 {
		max = models.MaxBuffer
	}
	return &Worker{
		cfg:     cfg,
		red:     red,
		nats:    nats,
		buffer:  make(map[string]string, max),
		flushCh: make(chan struct{}, 1),
		ctx:     ctx,
		cancel:  cancel,
	}
}

// Start subscribes to the raw telemetry subject and launches the flush loop and
// the OFFLINE sweeper.
func (w *Worker) Start() (*nats.Subscription, error) {
	go w.flusher()
	go w.sweeper()
	return w.nats.Subscribe(w.nats.Subject("raw", ">"), "live", w.handleMessage)
}

// Stop cancels the background loops and drains the remaining buffer (graceful
// shutdown: no buffered update is lost).
func (w *Worker) Stop() {
	w.cancel()
	w.poke()
	done := make(chan struct{})
	go func() {
		w.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		slog.Warn("worker-live: timed out draining the live-state buffer")
	}
	w.flushBuffer() // final synchronous flush
}

// idleAfter returns the configured ONLINE → IDLE threshold.
func (w *Worker) idleAfter() time.Duration {
	if w.cfg.Live.IdleAfter > 0 {
		return w.cfg.Live.IdleAfter
	}
	return models.IdleAfter
}

// offlineAfter returns the configured OFFLINE threshold.
func (w *Worker) offlineAfter() time.Duration {
	if w.cfg.Live.OfflineAfterMinutes > 0 {
		return time.Duration(w.cfg.Live.OfflineAfterMinutes) * time.Minute
	}
	return models.OfflineAfter
}
