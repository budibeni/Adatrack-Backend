package controllers

import (
	"encoding/json"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"

	"adatrack_gps/worker-live/models"
)

// handleMessage decodes one telemetry message into the live-state buffer and
// feeds the B7 fleet accumulators (odometer/engine hours + trip detection).
func (w *Worker) handleMessage(msg *nats.Msg) error {
	var t models.TelemetryMessage
	if err := json.Unmarshal(msg.Data, &t); err != nil {
		slog.Error("worker-live: invalid telemetry payload", "subject", msg.Subject, "error", err)
		return nil // malformed payloads are already counted at ingestion
	}
	if t.Timestamp <= 0 {
		t.Timestamp = time.Now().Unix()
	}

	// B7: pure in-memory accumulation (no I/O) so the live-state cadence is
	// untouched; the flusher persists the drained deltas (FR-2.5/FR-2.6).
	now := time.Now().UTC()
	w.fleet.Observe(t, now)
	if w.fleet.Pending() >= w.fleet.p.flushBatch {
		w.pokeFleet()
	}

	key := w.red.LiveStateKey(t.CompanyCode, t.IMEI)
	state := w.buildState(key, t)

	payload, err := json.Marshal(state)
	if err != nil {
		slog.Error("worker-live: failed to encode live state", "imei", t.IMEI, "error", err)
		return err
	}

	w.mu.Lock()
	w.buffer[key] = string(payload)
	full := len(w.buffer) >= w.cfg.Live.MaxBatch
	w.mu.Unlock()
	vehicleStateUpdates.Inc()

	// Fan-out for service-websocket (consumed by the `websocket` group in B2).
	if err := w.publishLive(t.IMEI, state); err != nil {
		slog.Warn("worker-live: live publish failed", "imei", t.IMEI, "error", err)
	}

	if full {
		w.poke()
	}
	return nil
}

// buildState merges a partial (fuel-only) message with the existing state and
// computes the connection status (FR-2.2).
func (w *Worker) buildState(key string, t models.TelemetryMessage) models.LiveState {
	if isFuelOnly(t) {
		return w.mergeFuelState(key, t)
	}
	return models.LiveState{
		IMEI:        t.IMEI,
		CompanyCode: t.CompanyCode,
		VehicleID:   t.VehicleID,
		Lat:         t.Lat,
		Lon:         t.Lon,
		Speed:       t.Speed,
		Heading:     t.Heading,
		Satellites:  t.Satellites,
		Altitude:    t.Altitude,
		Battery:     t.Battery,
		GsmSignal:   t.GsmSignal,
		// B6: the DEVICE ACC is projected verbatim — nil stays nil (absent in
		// the JSON), so a client can distinguish "ignition off" from "the frame
		// never reported ACC" instead of reading an inferred `false`.
		ACC:       t.ACC,
		Mileage:   t.Mileage,
		Fix:       t.Fix,
		Status:    CalculateStatus(t.Speed, 0, w.idleAfter(), w.offlineAfter()),
		LastSeen:  time.Now().Unix(),
		Timestamp: t.Timestamp,
		FuelLevel: t.FuelLevel,
		FuelTempC: t.FuelTempC,
	}
}

// isFuelOnly reports whether a message carries fuel data but no GPS fix — those
// must not overwrite position/speed (partial merge, FR-2.3).
func isFuelOnly(t models.TelemetryMessage) bool {
	return t.FuelLevel != nil && t.Lat == 0 && t.Lon == 0 && t.Speed == 0
}

// mergeFuelState keeps the existing position and only updates fuel + LastSeen.
func (w *Worker) mergeFuelState(key string, t models.TelemetryMessage) models.LiveState {
	var st models.LiveState
	if raw, err := w.red.Get(w.ctx, key); err == nil && raw != "" {
		if uerr := json.Unmarshal([]byte(raw), &st); uerr != nil {
			slog.Warn("worker-live: existing live state unreadable; writing fresh state",
				"key", key, "error", uerr)
			st = models.LiveState{}
		}
	}
	st.IMEI = t.IMEI
	st.CompanyCode = t.CompanyCode
	st.VehicleID = t.VehicleID
	st.FuelLevel = t.FuelLevel
	st.FuelVolume = t.FuelVolume
	st.FuelTempC = t.FuelTempC
	st.LastSeen = time.Now().Unix()
	if st.Status == "" {
		st.Status = CalculateStatus(0, 0, w.idleAfter(), w.offlineAfter())
	}
	return st
}

// publishLive fans a live update out to `telemetry.live.<IMEI>`.
func (w *Worker) publishLive(imei string, state models.LiveState) error {
	payload, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err := w.nats.Publish(w.nats.Subject("live", imei), payload); err != nil {
		return err
	}
	livePublished.Inc()
	return nil
}

// CalculateStatus implements the FR-2.2 state machine for a FRESH message:
//
//	speed > 0                     → ONLINE (moving)
//	stationary but within idle    → ONLINE
//	stationary beyond idle        → IDLE
//
// `age` is how long ago the last message arrived (0 for an inbound message).
// OFFLINE is only reached through age > offlineAfter, which the staleness
// sweeper (offline.go) evaluates for parked vehicles.
func CalculateStatus(speed float64, age time.Duration, idleAfter, offlineAfter time.Duration) string {
	if offlineAfter > 0 && age > offlineAfter {
		return models.StatusOffline
	}
	if speed > 0 {
		return models.StatusOnline
	}
	if idleAfter > 0 && age > idleAfter {
		return models.StatusIdle
	}
	return models.StatusOnline
}
