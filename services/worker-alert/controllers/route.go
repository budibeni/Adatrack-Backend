package controllers

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"adatrack_gps/worker-alert/models"
)

// detectRouteDeviation evaluates in-progress route assignments: distance to the
// nearest waypoint beyond the threshold raises ROUTE_DEVIATION (high) whose
// metadata keeps the MAX deviation (PRD §5.9.2).
func (w *Worker) detectRouteDeviation(ctx context.Context, t models.TelemetryMessage, now time.Time) {
	if t.Lat == 0 && t.Lon == 0 {
		return
	}
	threshold := w.cfg.Alert.RouteDeviationThresholdM
	if threshold <= 0 {
		threshold = 200
	}
	for _, a := range w.cachedAssignments(ctx, t.CompanyCode) {
		if a.VehicleID != t.VehicleID || len(a.Waypoints) == 0 {
			continue
		}
		pts := make([][2]float64, len(a.Waypoints))
		for i, wp := range a.Waypoints {
			pts[i] = [2]float64{wp.Lat, wp.Lon}
		}
		dist, _ := NearestWaypointM(t.Lat, t.Lon, pts)
		if dist <= threshold {
			continue
		}
		alert := &models.Alert{
			Type:        models.AlertRouteDeviation,
			Severity:    models.SeverityHigh,
			VehicleID:   t.VehicleID,
			IMEI:        t.IMEI,
			CompanyCode: t.CompanyCode,
			Lat:         t.Lat,
			Lon:         t.Lon,
			Speed:       t.Speed,
			DedupKey: "route:" + t.CompanyCode + ":" + strconv.FormatInt(t.VehicleID, 10) +
				":" + strconv.FormatInt(a.ID, 10),
			Metadata: map[string]any{
				"assignment_id":    a.ID,
				"deviation_meters": dist,
				"threshold_meters": threshold,
				"waypoint_count":   len(pts),
			},
		}
		raised, err := w.engine.RaiseAlert(ctx, alert)
		if err != nil {
			slog.Error("worker-alert: route deviation alert failed", "vehicle_id", t.VehicleID, "error", err)
			continue
		}
		if raised == nil {
			// Open alert exists — keep the max deviation up to date (PRD §5.9.2).
			w.engine.UpdateRouteDeviation(ctx, alert, dist)
		}
	}
}
