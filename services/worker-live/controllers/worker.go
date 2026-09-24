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
// to `telemetry.live.<IMEI>` for service-websocket. It also maintains the B7
// fleet accumulators (odometer/engine hours + trip/stop detection).
type Worker struct {
	cfg  *internal.Config
	red  *internal.RedisClient
	nats *internal.NATSClient

	// store persists the FR-2.5/FR-2.6 accumulators (nil = persistence disabled,
	// in which case the live-state path keeps working exactly as before).
	store FleetStore
	fleet *fleetAccumulator
	// fleetCh pokes the fleet flusher (buffer high-water mark reached).
	fleetCh chan struct{}

	// buffer holds key → JSON payload, flushed as ONE MSET (FR-2.3).
	mu     sync.Mutex
	buffer map[string]string

	flushCh chan struct{}
	wg      sync.WaitGroup
	fleetWg sync.WaitGroup

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
	// B7 fleet accumulators (PRD §10.1 worker-live).
	odometerUpdates = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "odometer_updates_total",
		Help: "Successful odometer flushes to tm_vehicles (FR-2.5)",
	})
	engineHoursUpdates = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "engine_hours_updates_total",
		Help: "Successful engine-hour flushes to tm_vehicles (FR-2.5)",
	})
	tripEvents = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "trip_events_total",
		Help: "Trip state-machine events (FR-2.6)",
	}, []string{"company_code", "event_type"})
	stopEvents = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "stop_events_total",
		Help: "Stops recorded in td_vehicle_stops (FR-2.6)",
	}, []string{"company_code"})
	tripStopFlushSize = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "trip_stop_flush_size",
		Help: "Vehicles whose odometer/trip data was flushed in the last batch (FR-2.5)",
	})
	fleetFlushErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "fleet_flush_errors_total",
		Help: "Fleet accumulator flush failures per stage (retried at the next flush)",
	}, []string{"stage"})
)

// RegisterMetrics registers the worker-live collectors.
func RegisterMetrics(reg *prometheus.Registry) {
	reg.MustRegister(vehicleStateUpdates, redisBatchSize, livePublished, stateFlushes,
		offlineTransitions, odometerUpdates, engineHoursUpdates, tripEvents, stopEvents,
		tripStopFlushSize, fleetFlushErrors)
}

// New builds the worker and its bounded buffer. `store` may be nil, which keeps
// the live-state pipeline running while the fleet accumulators stay in memory.
func New(cfg *internal.Config, red *internal.RedisClient, nats *internal.NATSClient, store FleetStore) *Worker {
	ctx, cancel := context.WithCancel(context.Background())
	max := cfg.Live.MaxBatch
	if max <= 0 {
		max = models.MaxBuffer
	}
	return &Worker{
		cfg:     cfg,
		red:     red,
		nats:    nats,
		store:   store,
		fleet:   newFleetAccumulator(fleetParamsFromConfig(cfg)),
		fleetCh: make(chan struct{}, 1),
		buffer:  make(map[string]string, max),
		flushCh: make(chan struct{}, 1),
		ctx:     ctx,
		cancel:  cancel,
	}
}

// Fleet exposes the accumulator (tests assert on the buffered deltas).
func (w *Worker) Fleet() *fleetAccumulator { return w.fleet }

// Start subscribes to the raw telemetry subject and launches the flush loops.
func (w *Worker) Start() (*nats.Subscription, error) {
	go w.flusher()
	go w.sweeper()
	w.fleetWg.Add(1)
	go func() {
		defer w.fleetWg.Done()
		w.fleetFlusher()
	}()
	return w.nats.Subscribe(w.nats.Subject("raw", ">"), "live", w.handleMessage)
}

// Stop cancels the background loops and drains the remaining buffers (graceful
// shutdown: no buffered live update and no accumulated fleet delta is lost).
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

	fleetDone := make(chan struct{})
	go func() {
		w.fleetWg.Wait()
		close(fleetDone)
	}()
	select {
	case <-fleetDone:
	case <-time.After(5 * time.Second):
		slog.Warn("worker-live: timed out waiting for the fleet flusher")
	}
	w.flushFleet() // final synchronous flush of odometer/trips
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
