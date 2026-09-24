package controllers

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/nats-io/nats.go"

	"adatrack_gps/internal"
	"adatrack_gps/worker-alert/models"
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

	// FuelStash is the in-memory ring of recent fuel readings per vehicle
	// (IMEI-based dedup across re-connects), used by the alert detector so a
	// single device reconnect does not reset the sliding window.
	fuelStashMu sync.Mutex
	fuelStash   map[string]*fuelStash // key: company + ":" + imei

	// --- B8 driver behaviour + maintenance -------------------------------
	// driver tracks the open speeding episodes; driverMinSpeeding is the
	// shortest episode that becomes an event (DRIVER_SPEEDING_MIN_SECONDS).
	driver            *driverTracker
	driverMinSpeeding int
}

// fuelStash is the per-device fuel window used by the B5a detector.
type fuelStash struct {
	// history is the recent readings, oldest first. Only the last N are kept
	// (EngineFuelWindow default 10) to keep the ring small.
	history []models.TelemetryMessage
	// lastAt is the timestamp of the most recent reading (used for freshness).
	lastAt int64
	// accHistory tracks recent ACC transitions within EngineFuelAccWindowSec.
	accHistory []int64
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
		fuelStash: map[string]*fuelStash{}, // first fuel frame must not panic (nil map write)
		driver:    newDriverTracker(),
		ctx:       ctx,
		cancel:    cancel,
	}
}

// WithDriverConfig sets the B8 driver-behaviour thresholds (called by main so the
// worker keeps a dependency-free constructor for the tests).
func (w *Worker) WithDriverConfig(minSpeedingSeconds int) *Worker {
	if minSpeedingSeconds <= 0 {
		minSpeedingSeconds = 10
	}
	w.driverMinSpeeding = minSpeedingSeconds
	return w
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
	// B8: the maintenance reminder sweep runs on its own cadence (odometer /
	// engine-hours / calendar thresholds, PRD §21.2 row 5).
	w.launch(w.maintenanceLoop, w.cfg.Driver.MaintenanceSweepInterval)

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
	// B8: device-reported harsh events + the derived speeding episode.
	w.detectDriver(ctx, t, now)
	if t.HasFuel() {
		w.detFuel(ctx, t, now)
	}
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

// detFuel is the B5a fuel-sensor detector: it reads the vehicle's fuel config
// (per-vehicle override or tenant-wide default), evaluates the FUEL_DROP / REFUEL
// thresholds inside the sliding window, applies the ACC-gate (strict only when
// FUEL_DROP_REQUIRE_ACC=true), and raises a deduped alert via the engine.
func (w *Worker) detFuel(ctx context.Context, t models.TelemetryMessage, now time.Time) {
	company := t.CompanyCode
	vehicleID := t.VehicleID
	imei := t.IMEI

	configs, err := w.store.FuelConfigs(ctx, company)
	if err != nil {
		slog.Warn("worker-alert: fuel config load failed", "company", company, "error", err)
		return
	}
	cfg := fuelConfigFor(configs, vehicleID)
	if cfg == nil || !cfg.Enabled {
		return
	}

	stashKey := company + ":" + imei
	w.fuelStashMu.Lock()
	stash, ok := w.fuelStash[stashKey]
	if !ok {
		stash = &fuelStash{}
		w.fuelStash[stashKey] = stash
	}
	w.fuelStashMu.Unlock()

	// Apply ACC-gate for FUEL_DROP (strict literal only when FUEL_DROP_REQUIRE_ACC=true).
	if cfg.DropThresholdPct > 0 && w.cfg.Fuel.RequireACC {
		if !AccOn(t.ACC) {
			w.fuelStashMu.Lock()
			stash.accHistory = append(stash.accHistory, now.Unix())
			if len(stash.accHistory) > w.cfg.Fuel.ACCStaleSeconds {
				stash.accHistory = stash.accHistory[1:]
			}
			w.fuelStashMu.Unlock()
			return
		}
	}

	// Update the in-memory ring of recent fuel readings (for the sliding window).
	w.fuelStashMu.Lock()
	stash.history = append(stash.history, t)
	if len(stash.history) > 10 {
		stash.history = stash.history[len(stash.history)-10:]
	}
	stash.lastAt = now.Unix()
	w.fuelStashMu.Unlock()

	// Evaluate the sliding window against config.WindowSeconds (FR-7.6).
	window := time.Duration(cfg.WindowSeconds) * time.Second
	if window <= 0 {
		window = 5 * time.Minute
	}
	windowStart := now.Add(-window)

	var dropDetected, refuelDetected bool
	var minLevel, maxLevel float64
	first := true
	for i := range stash.history {
		r := stash.history[i]
		if r.Timestamp < windowStart.Unix() || r.Timestamp == t.Timestamp {
			continue
		}
		lvl := 0.0
		if r.FuelLevel != nil {
			lvl = *r.FuelLevel
		} else if r.FuelVolume != nil {
			lvl = *r.FuelVolume
		}
		if first {
			minLevel = lvl
			maxLevel = lvl
			first = false
		} else {
			if lvl < minLevel {
				minLevel = lvl
			}
			if lvl > maxLevel {
				maxLevel = lvl
			}
		}
	}

	if !first && cfg.DropThresholdPct > 0 {
		dropPct := (maxLevel - minLevel) / maxLevel * 100
		if dropPct >= float64(cfg.DropThresholdPct) {
			dropDetected = true
		}
	}
	if !first && cfg.RefuelThresholdPct > 0 {
		refuelPct := (maxLevel - minLevel) / minLevel * 100
		if refuelPct >= float64(cfg.RefuelThresholdPct) && minLevel > 0 {
			refuelDetected = true
		}
	}

	if dropDetected {
		alert := &models.Alert{
			Type:        models.AlertFuelDrop,
			Severity:    w.cfg.Fuel.Severity,
			VehicleID:   vehicleID,
			IMEI:        imei,
			CompanyCode: company,
			DedupKey:    "fuel:drop:" + strconv.FormatInt(vehicleID, 10),
			DetectedAt:  now,
		}
		if _, err := w.engine.RaiseAlert(ctx, alert); err != nil {
			slog.Error("worker-alert: fuel drop alert failed", "vehicle_id", vehicleID, "error", err)
		}
	}
	if refuelDetected {
		alert := &models.Alert{
			Type:        models.AlertRefuel,
			Severity:    models.SeverityLow,
			VehicleID:   vehicleID,
			IMEI:        imei,
			CompanyCode: company,
			DedupKey:    "fuel:refuel:" + strconv.FormatInt(vehicleID, 10),
			DetectedAt:  now,
		}
		if _, err := w.engine.RaiseAlert(ctx, alert); err != nil {
			slog.Error("worker-alert: fuel refuel alert failed", "vehicle_id", vehicleID, "error", err)
		}
	}
}

// fuelConfigFor resolves the effective fuel config: vehicle-specific row wins,
// otherwise the tenant-wide default (vehicle_id 0). Disabled rows are skipped.
func fuelConfigFor(configs []models.FuelConfig, vehicleID int64) *models.FuelConfig {
	var global *models.FuelConfig
	for i := range configs {
		c := &configs[i]
		if !c.Enabled {
			continue
		}
		if c.VehicleID == vehicleID {
			return c
		}
		if c.VehicleID == 0 && global == nil {
			global = c
		}
	}
	return global
}
