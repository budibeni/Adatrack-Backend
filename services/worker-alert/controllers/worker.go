package controllers

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/nats-io/nats.go"

	"ajb_gps/internal"
	"ajb_gps/worker-alert/models"
)

// Worker consumes `telemetry.raw.>` (queue group `alert`), runs the detectors
// and drives the OFFLINE/SOS sweepers (PRD §5.9, .agent/01-global-rules.md §10).
type Worker struct {
	cfg    *internal.Config
	red    *internal.RedisClient
	nats   *internal.NATSClient
	store  Store
	engine *Engine

	mu        sync.Mutex
	caches    map[string]*companyCache // key: company code
	companies map[string]bool          // companies seen since boot + master seed

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New builds the worker.
func New(cfg *internal.Config, red *internal.RedisClient, nats *internal.NATSClient, store Store) *Worker {
	ctx, cancel := context.WithCancel(context.Background())
	return &Worker{
		cfg:       cfg,
		red:       red,
		nats:      nats,
		store:     store,
		engine:    NewEngine(cfg, red, nats, store),
		caches:    map[string]*companyCache{},
		companies: map[string]bool{},
		ctx:       ctx,
		cancel:    cancel,
	}
}

// Start subscribes to raw telemetry and launches the background sweepers.
func (w *Worker) Start() (*nats.Subscription, error) {
	// Seed the company set from master so OFFLINE/SOS sweeps cover companies
	// that have not sent telemetry since this boot.
	if codes, err := w.store.CompanyCodes(w.ctx); err != nil {
		slog.Warn("worker-alert: company seed failed", "error", err)
	} else {
		w.mu.Lock()
		for _, c := range codes {
			w.companies[c] = true
		}
		w.mu.Unlock()
	}

	w.launch(w.sweepLoop, w.cfg.Alert.OfflineSweepInterval)
	w.launch(w.escalationLoop, w.cfg.Alert.SOSEscalationInterval)
	w.launch(w.cacheRefreshLoop, w.cfg.Alert.GeoFenceRefresh)

	return w.nats.Subscribe(w.nats.Subject("raw", ">"), "alert", w.handleMessage)
}

// Stop cancels the background loops and waits for them to drain.
func (w *Worker) Stop() {
	w.cancel()
	done := make(chan struct{})
	go func() {
		w.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		slog.Warn("worker-alert: sweeper drain timed out")
	}
}

// launch runs fn periodically until the worker is stopped.
func (w *Worker) launch(fn func(context.Context), every time.Duration) {
	if every <= 0 {
		every = 30 * time.Second
	}
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-w.ctx.Done():
				return
			case <-ticker.C:
				fn(w.ctx)
			}
		}
	}()
}

// handleMessage decodes and evaluates one telemetry message (pipeline:
// SOS → geofence → overspeed → battery → route → offline resolve).
func (w *Worker) handleMessage(msg *nats.Msg) error {
	start := time.Now()
	defer func() { alertEngineLatency.Observe(time.Since(start).Seconds()) }()

	var t models.TelemetryMessage
	if err := json.Unmarshal(msg.Data, &t); err != nil {
		slog.Error("worker-alert: invalid telemetry payload", "subject", msg.Subject, "error", err)
		return nil // malformed payloads are counted at ingestion
	}
	if t.CompanyCode == "" || t.VehicleID <= 0 {
		slog.Debug("worker-alert: telemetry without tenant binding", "subject", msg.Subject)
		return nil
	}
	w.rememberCompany(t.CompanyCode)
	w.evaluate(w.ctx, t)
	return nil
}

// evaluate runs every detector for one telemetry message.
func (w *Worker) evaluate(ctx context.Context, t models.TelemetryMessage) {
	now := time.Now().UTC()
	w.detectSOS(ctx, t, now)
	if t.Fix || t.Lat != 0 || t.Lon != 0 {
		w.detectGeofences(ctx, t, now)
	}
	w.detectOverspeed(ctx, t, now)
	w.detectBatteryLow(ctx, t, now)
	w.detectRouteDeviation(ctx, t, now)
	w.engine.resolveOffline(ctx, t)
}

// rememberCompany tracks the tenants the sweepers must cover.
func (w *Worker) rememberCompany(code string) {
	w.mu.Lock()
	w.companies[code] = true
	w.mu.Unlock()
}

// companiesSnapshot returns the tracked company codes.
func (w *Worker) companiesSnapshot() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]string, 0, len(w.companies))
	for c := range w.companies {
		out = append(out, c)
	}
	return out
}

// sweepLoop raises OFFLINE alerts for stale vehicles (PRD §5.9.7).
func (w *Worker) sweepLoop(ctx context.Context) {
	for _, company := range w.companiesSnapshot() {
		vehicles := w.cachedVehicles(ctx, company)
		if len(vehicles) == 0 {
			continue
		}
		w.sweepOffline(ctx, company, vehicles, time.Now().UTC())
	}
}

// escalationLoop re-notifies open SOS alerts older than SOS_ESCALATION_MINUTES,
// capping at SOS_ESCALATION_MAX re-notifications per alert (PRD §5.9.5). The
// Redis counter guards against double escalation across worker restarts.
func (w *Worker) escalationLoop(ctx context.Context) {
	minutes := time.Duration(w.cfg.Alert.SOSEscalationMinutes) * time.Minute
	maxCount := w.cfg.Alert.SOSEscalationMax
	if maxCount <= 0 {
		return // escalation disabled
	}
	for _, company := range w.companiesSnapshot() {
		alerts, err := w.store.OpenSOAlerts(ctx, company, time.Now().UTC().Add(-minutes), maxCount)
		if err != nil {
			slog.Warn("worker-alert: SOS escalation scan failed", "company", company, "error", err)
			continue
		}
		for i := range alerts {
			a := &alerts[i]
			// Cross-restart guard + cap in one atomic counter.
			n, err := w.red.Incr(ctx, "alert:sos:esc:"+company+":"+strconv.FormatInt(a.ID, 10))
			if err == nil && n > int64(maxCount) {
				continue
			}
			if err := w.store.EscalateAlert(ctx, company, a.ID, int(n)); err != nil {
				slog.Warn("worker-alert: SOS escalation update failed", "alert_id", a.ID, "error", err)
				continue
			}
			a.EscalationCount = int(n)
			if err := w.engine.Notify(ctx, a); err != nil {
				slog.Warn("worker-alert: SOS escalation notify failed", "alert_id", a.ID, "error", err)
				continue
			}
			sosEscalations.Inc()
			slog.Warn("worker-alert: SOS escalated", "alert_id", a.ID, "company", company,
				"vehicle_id", a.VehicleID, "escalation", n, "max", maxCount)
		}
	}
}

// cacheRefreshLoop refreshes every cached company's lookups.
func (w *Worker) cacheRefreshLoop(ctx context.Context) {
	for _, company := range w.companiesSnapshot() {
		w.cachedGeofences(ctx, company)
		w.cachedSpeedConfigs(ctx, company)
		w.cachedAssignments(ctx, company)
		w.cachedVehicles(ctx, company)
	}
}
