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
	score, grade := scoreFromCounts(counts)
	sc := &models.DriverScore{
		VehicleID:              ev.VehicleID,
		PeriodStart:            day.Format("2006-01-02"),
		PeriodEnd:              day.Format("2006-01-02"),
		HarshAccelerationCount: counts.HarshAcceleration,
		HarshBrakingCount:      counts.HarshBraking,
		HarshCorneringCount:    counts.HarshCornering,
		SpeedingCount:          counts.Speeding,
		SpeedingSeconds:        counts.SpeedingSeconds,
		Score:                  score,
		Grade:                  grade,
	}
	if err := w.store.UpsertDriverScore(ctx, ev.CompanyCode, sc); err != nil {
		slog.Warn("worker-alert: driver score upsert failed",
			"company", ev.CompanyCode, "vehicle_id", ev.VehicleID, "error", err)
		return
	}
	driverScore.WithLabelValues(grade).Set(score)
}

// scoreFromCounts maps the day's event counts to a 0..100 score and a grade.
//
// Weights: harsh acceleration/braking = 10 points each, harsh cornering = 5, each
// speeding episode = 5. The formula is intentionally simple and auditable;
// normalising per 100 km needs the B7.2 trip aggregates and belongs to the Safety
// module of B12 (documented in docs/B8-B10-VERIFICATION.md).
func scoreFromCounts(c models.DriverEventCounts) (float64, string) {
	score := 100.0 -
		10*float64(c.HarshAcceleration) -
		10*float64(c.HarshBraking) -
		5*float64(c.HarshCornering) -
		5*float64(c.Speeding)
	if score < 0 {
		score = 0
	}
	switch {
	case score >= 90:
		return score, "A"
	case score >= 80:
		return score, "B"
	case score >= 70:
		return score, "C"
	case score >= 60:
		return score, "D"
	default:
		return score, "E"
	}
}
