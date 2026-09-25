package controllers

// driver.go — B8 driver behaviour: device-reported harsh events + derived
// overspeed episodes → td_driver_events + th_driver_scores + `driver_event` alert
// (PRD §21.2 row 4, FR-2.7).
//
// Design rules (same honesty rules as the rest of the pipeline):
//   - A harsh acceleration/braking/cornering event is recorded ONLY when the
//     frame carried it (GT06 alarm reason 0x29/0x30, Teltonika IO 253/254/240).
//     worker-alert never derives them from speed differences.
//   - A speeding episode is DERIVED server-side (the device reports no duration),
//     and it is only closed — and therefore written — when the vehicle returns to
//     or below the configured limit, so `duration_seconds` is a measured value.
//   - The score is a transparent arithmetic penalty over the day's events; the
//     formula lives in `scoreFromCounts` and is covered by a unit test.

import (
	"context"
	"log/slog"
	"math"
	"strconv"
	"sync"
	"time"

	"adatrack_gps/worker-alert/models"
)

// driverEpisode tracks one open overspeed episode per vehicle.
type driverEpisode struct {
	startedAt time.Time
	limitKMH  float64
}

// driverTracker holds the per-vehicle episode state (guarded by its own mutex so
// the hot path never contends with the cache/company maps).
type driverTracker struct {
	mu       sync.Mutex
	episodes map[string]*driverEpisode // key: company + ":" + vehicleID
}

// newDriverTracker builds the tracker.
func newDriverTracker() *driverTracker {
	return &driverTracker{episodes: map[string]*driverEpisode{}}
}

func episodeKey(company string, vehicleID int64) string {
	return company + ":" + strconv.FormatInt(vehicleID, 10)
}

// detectDriver runs the B8 detectors for one telemetry message. `now` is only a
// fallback clock; the device timestamp drives the measurements.
func (w *Worker) detectDriver(ctx context.Context, t models.TelemetryMessage, now time.Time) {
	w.recordDevicePulses(ctx, t, now)
	w.trackSpeedingEpisode(ctx, t, now)
}

// recordDevicePulses persists the harsh-event flags the device itself reported.
func (w *Worker) recordDevicePulses(ctx context.Context, t models.TelemetryMessage, now time.Time) {
	pulses := []struct {
		flag    bool
		kind    string
		code    int
		sev     string
		source  string
		message string
	}{
		{t.HarshAccel, models.DriverHarshAcceleration, 0x29, models.SeverityHigh, "device_alarm", "harsh acceleration"},
		{t.HarshBraking, models.DriverHarshBraking, 0x30, models.SeverityHigh, "device_alarm", "harsh braking"},
		{t.HarshCornering, models.DriverHarshCornering, 0x00, models.SeverityMedium, "io_event", "harsh cornering"},
	}
	for _, p := range pulses {
		if !p.flag {
			continue
		}
		ev := &models.DriverEvent{
			CompanyCode: t.CompanyCode, VehicleID: t.VehicleID, IMEI: t.IMEI,
			EventType: p.kind, Severity: p.sev, SpeedKMH: t.Speed,
			Lat: t.Lat, Lon: t.Lon, Source: p.source, RawCode: p.code,
			Timestamp: deviceTime(t, now),
		}
		w.persistDriverEvent(ctx, ev, p.message)
	}
}

// deviceTime prefers the device timestamp (seconds) and falls back to `now`
// when the frame carried none — the measured duration stays device-accurate.
func deviceTime(t models.TelemetryMessage, now time.Time) time.Time {
	if t.Timestamp > 0 {
		return time.Unix(t.Timestamp, 0).UTC()
	}
	return now.UTC()
}

// trackSpeedingEpisode measures how long a vehicle stays above its configured
// limit and writes exactly one event per closed episode.
func (w *Worker) trackSpeedingEpisode(ctx context.Context, t models.TelemetryMessage, now time.Time) {
	cfg := effectiveSpeedConfig(w.cachedSpeedConfigs(ctx, t.CompanyCode), t.VehicleID)
	key := episodeKey(t.CompanyCode, t.VehicleID)
	at := deviceTime(t, now)

	limit := 0.0
	if cfg != nil {
		limit = float64(cfg.MaxSpeed) // the configured limit, without the grace margin
	}
	overLimit := limit > 0 && t.Speed > limit

	w.driver.mu.Lock()
	ep := w.driver.episodes[key]
	if overLimit {
		if ep == nil {
			w.driver.episodes[key] = &driverEpisode{startedAt: at, limitKMH: limit}
			speedingEpisodesOpen.Inc()
		}
		w.driver.mu.Unlock()
		return
	}
	if ep == nil {
		w.driver.mu.Unlock()
		return
	}
	delete(w.driver.episodes, key)
	speedingEpisodesOpen.Dec()
	w.driver.mu.Unlock()

	// The episode is closed: only a behaviour long enough to matter is recorded
	// (DRIVER_SPEEDING_MIN_SECONDS), and the duration comes from device time.
	duration := int(at.Sub(ep.startedAt).Seconds())
	if duration < w.driverMinSpeeding || duration <= 0 {
		return
	}
	ev := &models.DriverEvent{
		CompanyCode: t.CompanyCode, VehicleID: t.VehicleID, IMEI: t.IMEI,
		EventType: models.DriverSpeeding, Severity: models.SeverityMedium,
		SpeedKMH: t.Speed, SpeedLimitKMH: ep.limitKMH, DurationSeconds: duration,
		Lat: t.Lat, Lon: t.Lon, Source: "derived", Timestamp: ep.startedAt,
	}
	w.persistDriverEvent(ctx, ev, "speeding episode")
}

// persistDriverEvent writes the event, raises the alert and refreshes the daily
// score. A storage failure is logged, never swallowed.
func (w *Worker) persistDriverEvent(ctx context.Context, ev *models.DriverEvent, message string) {
	if err := w.store.InsertDriverEvent(ctx, ev.CompanyCode, ev); err != nil {
		slog.Error("worker-alert: driver event insert failed",
			"company", ev.CompanyCode, "vehicle_id", ev.VehicleID, "event", ev.EventType, "error", err)
	}

	alert := &models.Alert{
		Type:        models.AlertDriverEvent,
		Severity:    ev.Severity,
		VehicleID:   ev.VehicleID,
		IMEI:        ev.IMEI,
		CompanyCode: ev.CompanyCode,
		Lat:         ev.Lat,
		Lon:         ev.Lon,
		Speed:       ev.SpeedKMH,
		DedupKey:    "driver:" + ev.CompanyCode + ":" + strconv.FormatInt(ev.VehicleID, 10) + ":" + ev.EventType,
		Metadata: map[string]any{
			"event_type":       ev.EventType,
			"message":          message,
			"speed_kmh":        ev.SpeedKMH,
			"speed_limit_kmh":  ev.SpeedLimitKMH,
			"duration_seconds": ev.DurationSeconds,
			"source":           ev.Source,
		},
		DetectedAt: ev.Timestamp,
	}
	if _, err := w.engine.RaiseAlert(ctx, alert); err != nil {
		slog.Error("worker-alert: driver event alert failed",
			"vehicle_id", ev.VehicleID, "event", ev.EventType, "error", err)
	}
	driverEvents.WithLabelValues(ev.EventType, ev.Source).Inc()
	w.refreshDriverScore(ctx, ev)
}

// refreshDriverScore recomputes the daily score of the affected vehicle.
func (w *Worker) refreshDriverScore(ctx context.Context, ev *models.DriverEvent) {
	day := ev.Timestamp.UTC()
	counts, err := w.store.DailyDriverEventCounts(ctx, ev.CompanyCode, ev.VehicleID, day)
	if err != nil {
		slog.Warn("worker-alert: driver event counts failed",
			"company", ev.CompanyCode, "vehicle_id", ev.VehicleID, "error", err)
		return
	}
	// Distance normalisation (migration 025): the same event count means very
	// different driving in 10 km and in 400 km. When the B7.2 trips of the day give
	// enough distance the score becomes a rate; otherwise (or when the trips are not
	// available yet) the documented count-based formula is used.
	distance, derr := w.store.DailyDriverDistanceKM(ctx, ev.CompanyCode, ev.VehicleID, day)
	if derr != nil {
		slog.Warn("worker-alert: driver distance lookup failed (scoring by counts)",
			"company", ev.CompanyCode, "vehicle_id", ev.VehicleID, "error", derr)
		distance = 0
	}
	byCounts, _ := scoreFromCounts(counts)
	score, grade, rate := scoreFromDistance(counts, distance, w.driverMinDistanceKM)

	sc := &models.DriverScore{
		VehicleID:              ev.VehicleID,
		PeriodStart:            day.Format("2006-01-02"),
		PeriodEnd:              day.Format("2006-01-02"),
		HarshAccelerationCount: counts.HarshAcceleration,
		HarshBrakingCount:      counts.HarshBraking,
		HarshCorneringCount:    counts.HarshCornering,
		SpeedingCount:          counts.Speeding,
		SpeedingSeconds:        counts.SpeedingSeconds,
		DistanceKM:             distance,
		EventsPer100KM:         rate,
		ScoreByCounts:          &byCounts,
		Score:                  score,
		Grade:                  grade,
	}
	if err := w.store.UpsertDriverScore(ctx, ev.CompanyCode, sc); err != nil {
		slog.Warn("worker-alert: driver score upsert failed",
			"company", ev.CompanyCode, "vehicle_id", ev.VehicleID, "error", err)
		return
	}
	mode := "per_distance"
	if rate == nil {
		mode = "per_counts"
	}
	driverScoreMode.WithLabelValues(mode).Inc()
	driverScore.WithLabelValues(grade).Set(score)
}

// Penalty weights per event (points). They are the same numbers the count formula
// used, so the two modes stay comparable.
const (
	driverPenaltyHarshAccel = 10.0
	driverPenaltyHarshBrake = 10.0
	driverPenaltyCornering  = 5.0
	driverPenaltySpeeding   = 5.0
)

// driverPenaltyPoints is the weighted penalty of one day of events.
func driverPenaltyPoints(c models.DriverEventCounts) float64 {
	return driverPenaltyHarshAccel*float64(c.HarshAcceleration) +
		driverPenaltyHarshBrake*float64(c.HarshBraking) +
		driverPenaltyCornering*float64(c.HarshCornering) +
		driverPenaltySpeeding*float64(c.Speeding)
}

// scoreFromDistance normalises the day's events per 100 km when the vehicle drove
// at least `minKM`; below that threshold (or without distance) it falls back to the
// count-based formula. The returned rate is nil when the fallback was used, which
// is what the caller records in `events_per_100km`.
//
// Buckets are chosen so the score lands in the matching grade:
//
//	≤5 points/100 km  → 100 (A)     a conservative fleet driver
//	≤15               →  85 (B)
//	≤25               →  75 (C)
//	≤40               →  65 (D)
//	>40               → 65 − (rate−40)·0.5 (floored at 0, E)
//
// A day without events scores 100 regardless of distance.
func scoreFromDistance(c models.DriverEventCounts, distanceKM, minKM float64) (score float64, grade string, rate *float64) {
	if minKM <= 0 {
		minKM = defaultDriverMinDistanceKM
	}
	if distanceKM < minKM {
		s, g := scoreFromCounts(c)
		return s, g, nil
	}
	per100 := driverPenaltyPoints(c) / distanceKM * 100
	switch {
	case per100 <= 5:
		score = 100
	case per100 <= 15:
		score = 85
	case per100 <= 25:
		score = 75
	case per100 <= 40:
		score = 65
	default:
		// Beyond the last bucket the score keeps falling linearly so a very bad day
		// is distinguishable from a barely-bad one.
		score = 65 - (per100-40)*0.5
		if score < 0 {
			score = 0
		}
	}
	score = math.Round(score*100) / 100
	rate = &per100
	return score, gradeFor(score), rate
}

// defaultDriverMinDistanceKM is the distance below which normalising per distance
// would amplify noise (also the fallback when the env override is absent).
const defaultDriverMinDistanceKM = 5.0

// gradeFromScore is kept for callers that only have the final score.
func gradeFor(score float64) string {
	switch {
	case score >= 90:
		return "A"
	case score >= 80:
		return "B"
	case score >= 70:
		return "C"
	case score >= 60:
		return "D"
	default:
		return "E"
	}
}

// scoreFromCounts maps the day's event counts to a 0..100 score and a grade.
//
// Weights: harsh acceleration/braking = 10 points each, harsh cornering = 5, each
// speeding episode = 5 (see driverPenaltyPoints). It is the fallback used when the
// day's distance is too small to normalise per 100 km (scoreFromDistance) — both
// modes share the weights so their numbers stay comparable.
func scoreFromCounts(c models.DriverEventCounts) (float64, string) {
	score := 100.0 - driverPenaltyPoints(c)
	if score < 0 {
		score = 0
	}
	return score, gradeFor(score)
}
