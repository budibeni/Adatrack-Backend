package controllers

import (
	"context"
	"log/slog"
	"time"

	"ajb_gps/worker-alert/models"
)

// VehicleRef identifies one active vehicle of a company (offline sweep input).
type VehicleRef struct {
	ID   int64
	IMEI string
}

// companyCache bundles the per-tenant lookups the detectors need, refreshed on
// a cadence (GEOFENCE_REFRESH_SEC / ROUTE_DEVIATION_REFRESH_SEC) so the hot
// path never blocks on a database round trip.
type companyCache struct {
	geofences   []models.Geofence
	speeds      []models.SpeedConfig
	assignments []models.Assignment
	vehicles    []VehicleRef
	geofencedAt time.Time
	speedsAt    time.Time
	assignedAt  time.Time
	vehiclesAt  time.Time
}

// cache returns (and creates) the cache record of one company.
func (w *Worker) cache(company string) *companyCache {
	w.mu.Lock()
	defer w.mu.Unlock()
	c, ok := w.caches[company]
	if !ok {
		c = &companyCache{}
		w.caches[company] = c
	}
	return c
}

// fresh reports whether a cached lookup is still within its refresh cadence.
func fresh(at time.Time, every time.Duration) bool {
	if every <= 0 {
		every = 30 * time.Second
	}
	return !at.IsZero() && time.Since(at) < every
}

// cachedGeofences loads active zones + mapping with a refresh cadence.
func (w *Worker) cachedGeofences(ctx context.Context, company string) []models.Geofence {
	c := w.cache(company)
	if fresh(c.geofencedAt, w.cfg.Alert.GeoFenceRefresh) {
		return c.geofences
	}
	zones, err := w.store.LoadGeofences(ctx, company)
	if err != nil {
		slog.Warn("worker-alert: geofence load failed", "company", company, "error", err)
		return c.geofences // serve the stale copy (availability over freshness)
	}
	c.geofences, c.geofencedAt = zones, time.Now()
	return zones
}

// cachedSpeedConfigs loads enabled speed configs with a refresh cadence.
func (w *Worker) cachedSpeedConfigs(ctx context.Context, company string) []models.SpeedConfig {
	c := w.cache(company)
	if fresh(c.speedsAt, w.cfg.Alert.GeoFenceRefresh) {
		return c.speeds
	}
	cfgs, err := w.store.LoadSpeedConfigs(ctx, company)
	if err != nil {
		slog.Warn("worker-alert: speed config load failed", "company", company, "error", err)
		return c.speeds
	}
	c.speeds, c.speedsAt = cfgs, time.Now()
	return cfgs
}

// cachedAssignments loads in-progress route assignments with a refresh cadence.
func (w *Worker) cachedAssignments(ctx context.Context, company string) []models.Assignment {
	c := w.cache(company)
	if fresh(c.assignedAt, w.cfg.Alert.RouteDeviationRefresh) {
		return c.assignments
	}
	assignments, err := w.store.LoadAssignments(ctx, company)
	if err != nil {
		slog.Warn("worker-alert: assignment load failed", "company", company, "error", err)
		return c.assignments
	}
	c.assignments, c.assignedAt = assignments, time.Now()
	return assignments
}

// cachedVehicles loads the active vehicle list with a refresh cadence.
func (w *Worker) cachedVehicles(ctx context.Context, company string) []VehicleRef {
	c := w.cache(company)
	if fresh(c.vehiclesAt, w.cfg.Alert.GeoFenceRefresh) {
		return c.vehicles
	}
	vehicles, err := w.store.ActiveVehicles(ctx, company)
	if err != nil {
		slog.Warn("worker-alert: vehicle load failed", "company", company, "error", err)
		return c.vehicles
	}
	c.vehicles, c.vehiclesAt = vehicles, time.Now()
	return vehicles
}
