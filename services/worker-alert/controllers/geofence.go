package controllers

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"time"

	"ajb_gps/worker-alert/models"
)

// geofenceStateKey is the per-device entry/exit state map (PRD §5.9.1:
// `{prefix}{company}:geofence_state:{imei}`).
func (w *Worker) geofenceStateKey(company, imei string) string {
	return w.red.KeyPrefix() + company + ":geofence_state:" + imei
}

// loadGeofenceState reads the persisted inside/outside map for one device.
func (w *Worker) loadGeofenceState(ctx context.Context, company, imei string) map[int64]bool {
	raw, err := w.red.Get(ctx, w.geofenceStateKey(company, imei))
	if err != nil || raw == "" {
		return map[int64]bool{}
	}
	var m map[int64]bool
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		slog.Warn("worker-alert: unreadable geofence state; starting fresh",
			"imei", imei, "error", err)
		return map[int64]bool{}
	}
	return m
}

// saveGeofenceState persists the inside/outside map for one device.
func (w *Worker) saveGeofenceState(ctx context.Context, company, imei string, state map[int64]bool) {
	payload, err := json.Marshal(state)
	if err != nil {
		slog.Warn("worker-alert: geofence state encode failed", "imei", imei, "error", err)
		return
	}
	// TTL one week: a device that disappears keeps no state forever.
	if err := w.red.Set(ctx, w.geofenceStateKey(company, imei), string(payload), 7*24*time.Hour); err != nil {
		slog.Warn("worker-alert: geofence state write failed", "imei", imei, "error", err)
	}
}

// detectGeofences evaluates every zone mapped to the vehicle (multi-zone: one
// alert per zone per transition, PRD §5.9.1) and raises entry/exit alerts.
func (w *Worker) detectGeofences(ctx context.Context, t models.TelemetryMessage, now time.Time) {
	zones := w.cachedGeofences(ctx, t.CompanyCode)
	if len(zones) == 0 {
		return
	}
	state := w.loadGeofenceState(ctx, t.CompanyCode, t.IMEI)
	changed := false

	for _, z := range zones {
		if !z.VehicleIDs[t.VehicleID] {
			continue
		}
		inside := false
		switch z.AreaType {
		case "circle":
			inside = HaversineM(t.Lat, t.Lon, z.CenterLat, z.CenterLon) <= float64(z.RadiusM)
		case "polygon":
			inside = PointInPolygon(t.Lat, t.Lon, z.Boundary)
		}
		wasInside := state[z.ID]
		if inside == wasInside {
			continue // no transition
		}
		state[z.ID] = inside
		changed = true

		var direction string
		switch {
		case inside && z.OnEntry:
			direction = "entry"
		case !inside && z.OnExit:
			direction = "exit"
		default:
			continue // transition not armed for this zone
		}

		alert := &models.Alert{
			Type:        models.AlertGeofenceBreach,
			Severity:    z.Severity,
			VehicleID:   t.VehicleID,
			IMEI:        t.IMEI,
			CompanyCode: t.CompanyCode,
			Lat:         t.Lat,
			Lon:         t.Lon,
			Speed:       t.Speed,
			DedupKey: "geofence:" + t.CompanyCode + ":" + strconv.FormatInt(t.VehicleID, 10) +
				":" + strconv.FormatInt(z.ID, 10) + ":" + direction,
			Metadata: map[string]any{
				"geofence_id": z.ID,
				"zone_name":   z.Name,
				"direction":   direction,
				"area_type":   z.AreaType,
			},
		}
		if _, err := w.engine.RaiseAlert(ctx, alert); err != nil {
			slog.Error("worker-alert: geofence alert failed", "zone", z.ID, "error", err)
		}
	}
	if changed {
		w.saveGeofenceState(ctx, t.CompanyCode, t.IMEI, state)
	}
}
