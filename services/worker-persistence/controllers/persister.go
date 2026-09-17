// Package controllers implements the persistence worker: it batches telemetry
// messages and writes them to the tenant `th_telemetry_logs` partition
// (PRD Module 3 / FR-3.1..FR-3.4).
package controllers

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus"

	"adatrack_gps/internal"
	"adatrack_gps/internal/tenant"
	"adatrack_gps/worker-persistence/models"
)

// Persister buffers inbound telemetry and flushes it in batches.
type Persister struct {
	cfg     *internal.Config
	tenants *tenant.Manager
	nats    *internal.NATSClient

	mu          sync.Mutex
	pending     []models.Row
	fuelPending []models.FuelRow

	flushCh chan struct{}
	wg      sync.WaitGroup

	ctx    context.Context
	cancel context.CancelFunc

	// test seams: unit tests stub routing/publishing without live infrastructure.
	resolveCompanyDB func(companyCode string) (*internal.DBPool, error)
	publishError     func(imei string, payload []byte)
}

// Metrics (PRD §10.1 worker-persistence).
var (
	messagesProcessed = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "messages_processed_total",
		Help: "Telemetry rows successfully persisted per company",
	}, []string{"company_code"})
	batchInsertSize = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "batch_insert_size",
		Help:    "Rows per batch insert",
		Buckets: prometheus.ExponentialBuckets(10, 2, 8), // 10..1280
	})
	batchInsertErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "batch_insert_errors_total",
		Help: "Failed batch inserts per company",
	}, []string{"company_code"})
	retryAttempts = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "retry_attempts_total",
		Help: "Insert retry attempts (FR-3.4 step 5)",
	})
	tenantRoutingDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "tenant_routing_duration_ms",
		Help:    "company_code → DB pool resolution latency in milliseconds",
		Buckets: prometheus.ExponentialBuckets(0.05, 2, 10),
	})
	deadLettered = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "persistence_deadletter_total",
		Help: "Rows routed to telemetry.error.<IMEI> after retries were exhausted",
	})
	positionlessRows = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "positionless_rows_total",
		Help: "Rows without position (heartbeat/fuel-only) not written to th_telemetry_logs",
	})
	pendingRows = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "persistence_pending_rows",
		Help: "Rows currently buffered awaiting the next flush",
	})
	fuelRowsPending = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "persistence_fuel_pending_rows",
		Help: "td_fuel_logs rows currently buffered awaiting the next flush (B5a)",
	})
)

// RegisterMetrics registers the persistence collectors.
func RegisterMetrics(reg *prometheus.Registry) {
	reg.MustRegister(messagesProcessed, batchInsertSize, batchInsertErrors, retryAttempts,
		tenantRoutingDuration, deadLettered, positionlessRows, pendingRows, fuelRowsPending)
}

// New builds the persister.
func New(cfg *internal.Config, tenants *tenant.Manager, nats *internal.NATSClient) *Persister {
	ctx, cancel := context.WithCancel(context.Background())
	batchSize := cfg.Persistence.BatchSize
	if batchSize <= 0 {
		batchSize = 500
	}
	p := &Persister{
		cfg:     cfg,
		tenants: tenants,
		nats:    nats,
		pending: make([]models.Row, 0, batchSize),
		flushCh: make(chan struct{}, 1),
		ctx:     ctx,
		cancel:  cancel,
	}
	p.resolveCompanyDB = p.defaultResolveCompanyDB
	p.publishError = p.defaultPublishError
	return p
}

// Start subscribes to `telemetry.raw.>` (queue group "persistence") and starts
// the batch flush loop.
func (p *Persister) Start() (*nats.Subscription, error) {
	go p.flusher()
	return p.nats.Subscribe(p.nats.Subject("raw", ">"), "persistence", p.handleMessage)
}

// Stop drains the buffer: the final batch is written before returning so an
// acknowledged message is never lost on shutdown (graceful shutdown, FR-3.1).
func (p *Persister) Stop() {
	p.cancel()
	p.mu.Lock()
	snapshot := p.pending
	p.pending = nil
	p.mu.Unlock()
	if len(snapshot) > 0 {
		p.persist(companyGroups(snapshot))
	}
	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		slog.Warn("worker-persistence: timed out draining in-flight batches")
	}
}

// poke signals the flusher to run immediately (non-blocking).
func (p *Persister) poke() {
	select {
	case p.flushCh <- struct{}{}:
	default:
	}
}

// flusher flushes on BATCH_TIMEOUT_SEC or when poked (FR-3.1: 500 rows or 5 s).
func (p *Persister) flusher() {
	timeout := p.cfg.Persistence.BatchTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	tick := time.NewTicker(timeout)
	defer tick.Stop()
	for {
		select {
		case <-p.flushCh:
			p.flush()
		case <-tick.C:
			p.mu.Lock()
			nonEmpty := len(p.pending) > 0
			p.mu.Unlock()
			if nonEmpty {
				p.flush()
			}
		case <-p.ctx.Done():
			return
		}
	}
}

// companyGroups groups rows by company code so each tenant gets its own INSERT
// against its own schema (tenant routing, PRD §6.2).
func companyGroups(rows []models.Row) map[string][]models.Row {
	groups := make(map[string][]models.Row)
	for _, r := range rows {
		groups[r.CompanyCode] = append(groups[r.CompanyCode], r)
	}
	return groups
}
