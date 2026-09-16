package controllers

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"ajb_gps/worker-alert/models"
)

// effectiveSpeedConfig returns the config that applies to a vehicle:
// vehicle-specific row wins over the global row (PRD §5.9.4).
func effectiveSpeedConfig(configs []models.SpeedConfig, vehicleID int64) *models.SpeedConfig {
	var global *models.SpeedConfig
	for i := range configs {
		c := configs[i]
		if !c.Enabled {
			continue
		}
		if c.VehicleID == vehicleID {
			return &configs[i]
		}
		if c.VehicleID == 0 && global == nil {
			global = &configs[i]
		}
	}
	return global
}

// detectOverspeed evaluates the effective config and raises OVERSPEEDING when
// the speed exceeds limit + grace. Severity escalates to critical beyond 1.5×
// the effective limit (PRD §5.9.4).
func (w *Worker) detectOverspeed(ctx context.Context, t models.TelemetryMessage, now time.Time) {
	configs := w.cachedSpeedConfigs(ctx, t.CompanyCode)
	cfg := effectiveSpeedConfig(configs, t.VehicleID)
	if cfg == nil || t.Speed <= 0 {
		return
	}
	limit := float64(cfg.MaxSpeed) * (1 + float64(cfg.GracePct)/100)
	if t.Speed <= limit {
		return
	}

	severity := cfg.Severity
	if severity == "" {
		severity = models.SeverityMedium
	}
	if t.Speed > 1.5*float64(cfg.MaxSpeed) {
		severity = models.SeverityCritical
	}

	alert := &models.Alert{
		Type:        models.AlertOverspeeding,
		Severity:    severity,
		VehicleID:   t.VehicleID,
		IMEI:        t.IMEI,
		CompanyCode: t.CompanyCode,
		Lat:         t.Lat,
		Lon:         t.Lon,
		Speed:       t.Speed,
		DedupKey: "speed:" + t.CompanyCode + ":" + strconv.FormatInt(t.VehicleID, 10) +
			":" + strconv.FormatInt(cfg.ID, 10),
		Metadata: map[string]any{
			"speed_kmh":      t.Speed,
			"limit_kmh":      cfg.MaxSpeed,
			"grace_percent":  cfg.GracePct,
			"effective_kmh":  limit,
			"config_id":      cfg.ID,
			"vehicle_scoped": cfg.VehicleID != 0,
		},
	}
	if _, err := w.engine.RaiseAlert(ctx, alert); err != nil {
		slog.Error("worker-alert: overspeed alert failed", "vehicle_id", t.VehicleID, "error", err)
	}
}
