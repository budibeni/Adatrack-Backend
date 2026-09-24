package controllers

import (
	"context"
	"log/slog"
	"time"
)

// fleetFallbackInterval is the FR-2.5 flush cadence used when the configured
// value is missing (defensive: loadFleetConfig always sets 30 s).
const fleetFallbackInterval = 30 * time.Second

// pokeFleet signals the fleet flusher to run immediately (non-blocking).
func (w *Worker) pokeFleet() {
	select {
	case w.fleetCh <- struct{}{}:
	default:
	}
}

// fleetFlusher persists the B7 accumulators on the FR-2.5 cadence (30 s) or as
// soon as the high-water mark pokes it (≥100 vehicles).
func (w *Worker) fleetFlusher() {
	interval := w.fleet.p.flushEvery
	if interval <= 0 {
		interval = fleetFallbackInterval
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-w.fleetCh:
			w.flushFleet()
		case <-tick.C:
			w.flushFleet()
		case <-w.ctx.Done():
			return
		}
	}
}

// flushFleet drains the accumulators and persists them. Every failure is logged,
// counted and RE-BUFFERED so the next flush retries it (never a silent drop).
func (w *Worker) flushFleet() {
	if w == nil || w.fleet == nil {
		return
	}
	deltas, trips := w.fleet.Drain(time.Now().UTC())
	if len(deltas) == 0 && len(trips) == 0 {
		return
	}
	tripStopFlushSize.Set(float64(len(deltas) + len(trips)))
	if w.store == nil {
		slog.Warn("worker-live: fleet store unavailable; buffering metering/trips in memory",
			"vehicles", len(deltas), "trips", len(trips))
		w.fleet.RequeueTrips(trips)
		for key, d := range deltas {
			w.fleet.RequeueMetering(key, d)
		}
		return
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(w.ctx), 15*time.Second)
	defer cancel()

	failed := make(map[fleetKey]meteringDelta)
	for key, d := range deltas {
		if err := w.store.AddMetering(ctx, key.CompanyCode, key.VehicleID, d.OdometerKM, d.EngineHours); err != nil {
			fleetFlushErrors.WithLabelValues("metering").Inc()
			slog.Error("worker-live: fleet metering flush failed",
				"company", key.CompanyCode, "vehicle_id", key.VehicleID, "error", err)
			failed[key] = d
			continue
		}
		if d.OdometerKM > 0 {
			odometerUpdates.Inc()
		}
		if d.EngineHours > 0 {
			engineHoursUpdates.Inc()
		}
	}
	for key, d := range failed {
		w.fleet.RequeueMetering(key, d)
	}

	var retry []pendingTrip
	for _, p := range trips {
		tripID := p.tripID
		if tripID <= 0 {
			id, err := w.store.InsertTrip(ctx, p.key.CompanyCode, p.trip)
			if err != nil {
				fleetFlushErrors.WithLabelValues("trip").Inc()
				slog.Error("worker-live: trip insert failed",
					"company", p.key.CompanyCode, "vehicle_id", p.key.VehicleID, "error", err)
				retry = append(retry, p)
				continue
			}
			tripID = id
		}
		if len(p.stops) > 0 {
			if err := w.store.InsertStops(ctx, p.key.CompanyCode, tripID, p.stops); err != nil {
				fleetFlushErrors.WithLabelValues("stop").Inc()
				slog.Error("worker-live: stop insert failed",
					"company", p.key.CompanyCode, "trip_id", tripID, "error", err)
				// The trip row is stored already: carry the id so the next flush
				// retries ONLY the stops (never a duplicate trip row).
				p.tripID = tripID
				retry = append(retry, p)
			}
		}
	}
	w.fleet.RequeueTrips(retry)

	slog.Debug("worker-live: fleet accumulators flushed",
		"vehicles", len(deltas), "trips", len(trips), "retried", len(retry))
}
