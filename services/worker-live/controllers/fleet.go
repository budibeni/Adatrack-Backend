package controllers

import (
	"math"
	"strings"
	"time"

	"adatrack_gps/internal"
	"adatrack_gps/internal/geo"
	"adatrack_gps/worker-live/models"
)

// fleetKey identifies one vehicle inside one tenant — the routing key of every
// fleet persistence write (master `tm_vehicle_imei_map` → company schema).
type fleetKey struct {
	CompanyCode string
	VehicleID   int64
}

// meteringDelta is the pending odometer/engine-hour increment of one vehicle.
// Deltas (never absolute values) are what the flusher adds to `tm_vehicles`, so
// the DB counter can never be rolled back by a restart (FR-2.5 anti-rollback).
type meteringDelta struct {
	OdometerKM  float64
	EngineHours float64
}

// fleetParams are the FR-2.5/FR-2.6 thresholds resolved once at boot.
type fleetParams struct {
	flushEvery     time.Duration
	flushBatch     int
	maxJumpKM      float64
	maxGap         time.Duration
	engineMaxGap   time.Duration
	stopGrace      time.Duration
	minStop        time.Duration
	maxStop        time.Duration
	movingSpeedKMH float64
}

// fleetParamsFromConfig maps the shared configuration onto the thresholds.
func fleetParamsFromConfig(cfg *internal.Config) fleetParams {
	if cfg == nil {
		return fleetParams{}.withDefaults()
	}
	return fleetParams{
		flushEvery:     cfg.Fleet.FlushEvery,
		flushBatch:     cfg.Fleet.FlushBatch,
		maxJumpKM:      cfg.Fleet.MaxJumpKM,
		maxGap:         cfg.Fleet.MaxGap,
		engineMaxGap:   cfg.Fleet.EngineMaxGap,
		stopGrace:      cfg.Fleet.StopGrace,
		minStop:        cfg.Fleet.MinStop,
		maxStop:        cfg.Fleet.MaxStop,
		movingSpeedKMH: cfg.Fleet.MovingSpeedKMH,
	}.withDefaults()
}

// withDefaults fills unset thresholds with the PRD defaults (FR-2.5/FR-2.6), so
// a partially configured service still accumulates per specification.
func (p fleetParams) withDefaults() fleetParams {
	if p.flushEvery <= 0 {
		p.flushEvery = 30 * time.Second
	}
	if p.flushBatch <= 0 {
		p.flushBatch = 100
	}
	if p.maxJumpKM <= 0 {
		p.maxJumpKM = 5
	}
	if p.maxGap <= 0 {
		p.maxGap = 5 * time.Minute
	}
	if p.engineMaxGap <= 0 {
		p.engineMaxGap = 5 * time.Minute
	}
	if p.stopGrace <= 0 {
		p.stopGrace = 30 * time.Second
	}
	if p.minStop <= 0 {
		p.minStop = time.Minute
	}
	if p.maxStop <= 0 {
		p.maxStop = time.Hour
	}
	if p.maxStop < p.minStop {
		p.maxStop = p.minStop
	}
	return p
}

// tripState is the FR-2.6 MOVING ↔ STOPPED machine for ONE vehicle (at most one
// open trip at a time).
type tripState struct {
	open               bool
	startedAt          time.Time
	startLat, startLon float64
	// endLat/endLon is the last known MOVING position (used when the trip is
	// closed without a confirmed stop, e.g. after device silence).
	endLat, endLon float64
	distanceKM     float64
	maxSpeedKMH    float64
	// lastMoveAt is the device time of the last fix with speed > 0.
	lastMoveAt time.Time
	stops      []StopRecord

	// Pending stop candidate: a stationary fix that becomes a confirmed stop
	// once it lasts at least TRIP_STOP_GRACE_SECONDS.
	stopStartedAt time.Time
	stopLat       float64
	stopLon       float64
	stopLastSeen  time.Time
	stopConfirmed bool
}

// vehicleFleetState is the per-vehicle accumulator: the FR-2.5 metering plus the
// FR-2.6 trip machine.
type vehicleFleetState struct {
	imei string
	// last accepted position (odometer + trip continuity).
	lastLat, lastLon float64
	// lastFixAt is the DEVICE time of the last accepted fix.
	lastFixAt time.Time
	// observedAt is the SERVER time of the last accepted fix, used for the
	// auto-close staleness check (server and device clocks are never compared).
	observedAt time.Time
	hasFix     bool
	// accOn is the last CONFIRMED device ACC state, used only to credit engine
	// hours (the live state keeps exposing the tri-state value, B6).
	accOn bool

	// pending metering, flushed to `tm_vehicles` as deltas.
	odoKM       float64
	engineHours float64

	trip tripState
}

// pendingTrip is a closed trip on its way to `th_vehicle_trips` (+ its stops).
// `tripID` is set once the row exists, so a failed stop insert retries ONLY the
// stops instead of duplicating the trip header.
type pendingTrip struct {
	key    fleetKey
	trip   TripRecord
	stops  []StopRecord
	tripID int64
}

// fleetAccumulator implements FR-2.5 (odometer & engine hours) and FR-2.6
// (trip & stop detection) in ONE pass over the telemetry stream. It performs no
// I/O at all: the worker keeps its 100 ms live-state cadence and the flusher
// persists the drained deltas/trips.
//
// It is NOT safe for concurrent use; the worker owns one instance and calls
// Observe from the single NATS subscription callback.
type fleetAccumulator struct {
	p      fleetParams
	states map[fleetKey]*vehicleFleetState

	// pendingTrips holds the trips closed since the last Drain.
	pendingTrips []pendingTrip

	// Diagnostics counters (logged by the flusher).
	skippedJumps uint64
	skippedGaps  uint64
	skippedNoIDs uint64
}

// newFleetAccumulator builds an accumulator with the resolved thresholds.
func newFleetAccumulator(p fleetParams) *fleetAccumulator {
	return &fleetAccumulator{p: p.withDefaults(), states: map[fleetKey]*vehicleFleetState{}}
}

// stateFor returns (creating on demand) the accumulator of one vehicle.
func (a *fleetAccumulator) stateFor(key fleetKey) *vehicleFleetState {
	st, ok := a.states[key]
	if !ok {
		st = &vehicleFleetState{}
		a.states[key] = st
	}
	return st
}

// Observe folds one telemetry message into the accumulators.
//
// Guards of FR-2.5: fuel-only/positionless messages, VehicleID=0 (unrouted),
// GPS jumps > ODOMETER_MAX_JUMP_KM and intervals longer than the configured gap
// contribute nothing, while an ACC-less frame never invents engine time (B6).
func (a *fleetAccumulator) Observe(t models.TelemetryMessage, now time.Time) {
	if a == nil {
		return
	}
	company := strings.ToUpper(strings.TrimSpace(t.CompanyCode))
	if company == "" || t.VehicleID <= 0 {
		a.skippedNoIDs++
		return // cannot be persisted per tenant/vehicle
	}
	if t.Lat == 0 && t.Lon == 0 {
		return // positionless (fuel-only / heartbeat) — FR-2.5 guard
	}

	ts := now.UTC()
	if t.Timestamp > 0 {
		ts = time.Unix(t.Timestamp, 0).UTC()
	}
	key := fleetKey{CompanyCode: company, VehicleID: t.VehicleID}
	st := a.stateFor(key)
	if t.IMEI != "" {
		st.imei = t.IMEI
	}
	cur := geo.Point{Lat: t.Lat, Lon: t.Lon}

	// --- FR-2.5: odometer + engine hours, guarded ----------------------------
	deltaKM := 0.0
	if st.hasFix {
		gap := ts.Sub(st.lastFixAt)
		switch {
		case gap <= 0:
			// Out-of-order or same-second frame: no interval to credit.
			a.skippedGaps++
		default:
			prev := geo.Point{Lat: st.lastLat, Lon: st.lastLon}
			switch {
			case geo.DistanceKM(prev, cur) > a.p.maxJumpKM:
				a.skippedJumps++ // GPS jump, not real movement
			case gap > a.p.maxGap:
				a.skippedGaps++ // interval too long to credit a distance delta
			default:
				deltaKM = geo.DistanceKM(prev, cur)
				st.odoKM += deltaKM
			}
			// Engine hours follow TIME, not distance: credited for the interval
			// between two fixes when the previous fix confirmed ACC ON.
			if st.accOn && gap <= a.p.engineMaxGap {
				st.engineHours += gap.Hours()
			}
		}
	}

	// --- FR-2.6: MOVING ↔ STOPPED ------------------------------------------
	a.advanceTrip(st, key, ts, cur, t.Speed, deltaKM, t.Speed > a.p.movingSpeedKMH)

	// Remember the fix (device + server clock) and the confirmed ACC state.
	st.lastLat, st.lastLon = t.Lat, t.Lon
	st.lastFixAt, st.observedAt, st.hasFix = ts, now.UTC(), true
	if t.ACC != nil {
		st.accOn = *t.ACC
	}
}

// advanceTrip runs the FR-2.6 state machine for a positioned message.
func (a *fleetAccumulator) advanceTrip(st *vehicleFleetState, key fleetKey, ts time.Time, cur geo.Point, speed, deltaKM float64, moving bool) {
	tr := &st.trip

	if !moving {
		if !tr.open {
			return // parked with no trip in progress
		}
		// Still driving towards the stop: the interval counts as trip distance
		// until the stop is confirmed.
		tr.distanceKM += deltaKM
		if tr.stopStartedAt.IsZero() {
			tr.stopStartedAt, tr.stopLat, tr.stopLon = ts, cur.Lat, cur.Lon
		}
		tr.stopLastSeen = ts
		if !tr.stopConfirmed && ts.Sub(tr.stopStartedAt) >= a.p.stopGrace {
			tr.stopConfirmed = true
		}
		return
	}

	if !tr.open {
		a.openTrip(key, st, ts, cur, speed, deltaKM)
		return
	}

	if tr.stopConfirmed {
		// Movement resumed after a confirmed stop: a stop lasting at least
		// TRIP_MIN_STOP_SECONDS becomes a `td_vehicle_stops` row and splits the
		// trip; a shorter one is a blip and the trip continues.
		if stop, ok := tr.takeStop(a.p.minStop); ok {
			tr.stops = append(tr.stops, stop)
			a.closeTrip(key, st, tr.stopStartedAt, geo.Point{Lat: stop.Lat, Lon: stop.Lon})
			a.openTrip(key, st, ts, cur, speed, 0)
			return
		}
		tr.stopStartedAt, tr.stopLat, tr.stopLon = time.Time{}, 0, 0
		tr.stopLastSeen, tr.stopConfirmed = time.Time{}, false
	}

	tr.distanceKM += deltaKM
	if speed > tr.maxSpeedKMH {
		tr.maxSpeedKMH = speed
	}
	tr.lastMoveAt = ts
	tr.endLat, tr.endLon = cur.Lat, cur.Lon
}

// openTrip starts a trip (emitting the FR-2.6 trip_start metric).
func (a *fleetAccumulator) openTrip(key fleetKey, st *vehicleFleetState, ts time.Time, cur geo.Point, speed, deltaKM float64) {
	st.trip = tripState{
		open:        true,
		startedAt:   ts,
		startLat:    cur.Lat,
		startLon:    cur.Lon,
		endLat:      cur.Lat,
		endLon:      cur.Lon,
		distanceKM:  deltaKM,
		maxSpeedKMH: speed,
		lastMoveAt:  ts,
	}
	tripEvents.WithLabelValues(key.CompanyCode, string(TripEventStart)).Inc()
}

// closeTrip finalizes the open trip into the pending buffer (with its stops) and
// emits the FR-2.6 trip_end / stop metrics.
func (a *fleetAccumulator) closeTrip(key fleetKey, st *vehicleFleetState, end time.Time, endPoint geo.Point) {
	tr := &st.trip
	if !tr.open {
		return
	}
	if end.Before(tr.startedAt) {
		end = tr.startedAt
	}
	if endPoint.Lat == 0 && endPoint.Lon == 0 {
		endPoint = geo.Point{Lat: tr.endLat, Lon: tr.endLon}
	}

	duration := int(end.Sub(tr.startedAt).Seconds())
	if duration < 0 {
		duration = 0
	}
	trip := TripRecord{
		CompanyCode:     key.CompanyCode,
		VehicleID:       key.VehicleID,
		IMEI:            st.imei,
		StartTime:       tr.startedAt,
		EndTime:         end,
		StartLat:        tr.startLat,
		StartLon:        tr.startLon,
		EndLat:          endPoint.Lat,
		EndLon:          endPoint.Lon,
		DistanceKM:      round3(tr.distanceKM),
		MaxSpeedKMH:     round2(tr.maxSpeedKMH),
		StopCount:       len(tr.stops),
		DurationSeconds: duration,
	}
	if duration > 0 {
		trip.AvgSpeedKMH = round2(trip.DistanceKM / (float64(duration) / 3600))
	}

	a.pendingTrips = append(a.pendingTrips, pendingTrip{key: key, trip: trip, stops: tr.stops})
	tripEvents.WithLabelValues(key.CompanyCode, string(TripEventEnd)).Inc()
	if len(tr.stops) > 0 {
		stopEvents.WithLabelValues(key.CompanyCode).Add(float64(len(tr.stops)))
	}
	*tr = tripState{}
}

// takeStop materializes the pending stop candidate: `ok=false` when it is
// shorter than `minStop` (the caller then swallows it and keeps the trip open).
func (tr *tripState) takeStop(minStop time.Duration) (StopRecord, bool) {
	if tr.stopStartedAt.IsZero() {
		return StopRecord{}, false
	}
	// The stop ends at the LAST stationary fix: the resuming fix already drove
	// away, so using it would overstate the duration.
	end := tr.stopLastSeen
	if end.Before(tr.stopStartedAt) {
		end = tr.stopStartedAt
	}
	duration := int(end.Sub(tr.stopStartedAt).Seconds())
	if duration < 0 || time.Duration(duration)*time.Second < minStop {
		return StopRecord{}, false
	}
	return StopRecord{
		StartTime:       tr.stopStartedAt,
		EndTime:         end,
		Lat:             tr.stopLat,
		Lon:             tr.stopLon,
		DurationSeconds: duration,
	}, true
}

// Drain returns the buffered metering deltas (per vehicle) and the closed trips,
// clearing both buffers. `now` (server clock) drives the FR-2.6 auto-close of
// trips that have been standing or silent longer than TRIP_MAX_STOP_SECONDS.
func (a *fleetAccumulator) Drain(now time.Time) (map[fleetKey]meteringDelta, []pendingTrip) {
	if a == nil {
		return nil, nil
	}
	a.autoCloseStale(now.UTC())

	deltas := make(map[fleetKey]meteringDelta)
	for key, st := range a.states {
		if st.odoKM <= 0 && st.engineHours <= 0 {
			continue
		}
		deltas[key] = meteringDelta{OdometerKM: st.odoKM, EngineHours: st.engineHours}
		st.odoKM, st.engineHours = 0, 0
	}
	trips := a.pendingTrips
	a.pendingTrips = nil
	return deltas, trips
}

// autoCloseStale closes trips whose vehicle has been standing (or silent) longer
// than TRIP_MAX_STOP_SECONDS, recording the confirmed stop when there is one.
func (a *fleetAccumulator) autoCloseStale(now time.Time) {
	for key, st := range a.states {
		tr := &st.trip
		if !tr.open || st.observedAt.IsZero() {
			continue
		}
		if now.Sub(st.observedAt) < a.p.maxStop {
			continue
		}
		if tr.stopConfirmed {
			if stop, ok := tr.takeStop(a.p.minStop); ok {
				tr.stops = append(tr.stops, stop)
				a.closeTrip(key, st, tr.stopStartedAt, geo.Point{Lat: stop.Lat, Lon: stop.Lon})
				continue
			}
		}
		// Device silence (or a stop that never reached the grace window): close
		// the trip at the last known movement.
		end := tr.lastMoveAt
		if end.IsZero() {
			end = st.lastFixAt
		}
		a.closeTrip(key, st, end, geo.Point{Lat: tr.endLat, Lon: tr.endLon})
	}
}

// Pending reports how many vehicles still carry buffered data (deltas or a
// closed trip) — the FR-2.5 early-flush trigger (≥100 vehicles).
func (a *fleetAccumulator) Pending() int {
	if a == nil {
		return 0
	}
	n := len(a.pendingTrips)
	for _, st := range a.states {
		if st.odoKM > 0 || st.engineHours > 0 {
			n++
		}
	}
	return n
}

// RequeueMetering merges a failed flush back into the buffer so the next flush
// retries it (no silent loss, FR-4.3 spirit).
func (a *fleetAccumulator) RequeueMetering(key fleetKey, d meteringDelta) {
	if a == nil {
		return
	}
	st := a.stateFor(key)
	st.odoKM += d.OdometerKM
	st.engineHours += d.EngineHours
}

// RequeueTrips puts failed trips back at the front of the buffer.
func (a *fleetAccumulator) RequeueTrips(trips []pendingTrip) {
	if a == nil || len(trips) == 0 {
		return
	}
	a.pendingTrips = append(append([]pendingTrip{}, trips...), a.pendingTrips...)
}

// round2 / round3 keep the persisted values tidy (2–3 decimals).
func round2(v float64) float64 { return math.Round(v*100) / 100 }

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }
