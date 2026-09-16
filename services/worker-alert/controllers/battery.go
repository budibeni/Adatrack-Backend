package controllers

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"ajb_gps/worker-alert/models"
)

// detectBatteryLow raises BATTERY_LOW when the device battery falls below the
// threshold (default 20 %, PRD §5.9.6). battery_level == 0 means "not reported"
// and never triggers.
func (w *Worker) detectBatteryLow(ctx context.Context, t models.TelemetryMessage, now time.Time) {
	threshold := w.cfg.Alert.BatteryLowPercent
	if threshold <= 0 {
		threshold = 20
	}
	if t.Battery == 0 || int(t.Battery) >= threshold {
		return
	}
	severity := models.SeverityLow
	if int(t.Battery) <= threshold/2 {
		severity = models.SeverityMedium
	}
	alert := &models.Alert{
		Type:        models.AlertBatteryLow,
		Severity:    severity,
		VehicleID:   t.VehicleID,
		IMEI:        t.IMEI,
		CompanyCode: t.CompanyCode,
		Lat:         t.Lat,
		Lon:         t.Lon,
		DedupKey:    "battery:" + t.CompanyCode + ":" + strconv.FormatInt(t.VehicleID, 10),
		Metadata: map[string]any{
			"battery_level":     t.Battery,
			"threshold_percent": threshold,
		},
	}
	if _, err := w.engine.RaiseAlert(ctx, alert); err != nil {
		slog.Error("worker-alert: battery alert failed", "vehicle_id", t.VehicleID, "error", err)
	}
}
