package controllers

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"time"

	"adatrack_gps/worker-alert/models"
)

// offlineDedupKey is the DB dedup identity of the OFFLINE alert (one open alert
// per vehicle, PRD §5.9.7).
func offlineDedupKey(company string, vehicleID int64) string {
	return "offline:" + company + ":" + strconv.FormatInt(vehicleID, 10)
}

// sweepOffline raises OFFLINE alerts for vehicles whose live state is missing
// or stale beyond OFFLINE_AFTER_MINUTES (PRD §5.9.7). `vehicles` comes from the
// per-company vehicle cache (active vehicles only).
func (w *Worker) sweepOffline(ctx context.Context, company string, vehicles []VehicleRef, now time.Time) {
	after := time.Duration(w.cfg.Alert.OfflineAfterMinutes) * time.Minute
	if after <= 0 {
		after = 3 * time.Minute
	}
	for _, v := range vehicles {
		if w.stateFresh(ctx, company, v.IMEI, after, now) {
			continue
		}
		alert := &models.Alert{
			Type:        models.AlertOffline,
			Severity:    models.SeverityMedium,
			VehicleID:   v.ID,
			IMEI:        v.IMEI,
			CompanyCode: company,
			DedupKey:    offlineDedupKey(company, v.ID),
			Metadata: map[string]any{
				"offline_after_minutes": int(after.Minutes()),
			},
		}
		if _, err := w.engine.RaiseAlert(ctx, alert); err != nil {
			slog.Error("worker-alert: offline alert failed", "vehicle_id", v.ID, "error", err)
		}
	}
}

// stateFresh reports whether a device's live state exists and is recent enough
// (within the OFFLINE threshold). A missing key counts as stale.
func (w *Worker) stateFresh(ctx context.Context, company, imei string, after time.Duration, now time.Time) bool {
	raw, err := w.red.Get(ctx, w.red.LiveStateKey(company, imei))
	if err != nil {
		slog.Warn("worker-alert: offline sweep read failed", "imei", imei, "error", err)
		return true // fail-safe: never raise on transport errors
	}
	if raw == "" {
		return false
	}
	var st struct {
		LastSeen int64 `json:"last_seen"`
	}
	if err := json.Unmarshal([]byte(raw), &st); err != nil || st.LastSeen <= 0 {
		return false
	}
	return now.Sub(time.Unix(st.LastSeen, 0)) <= after
}
