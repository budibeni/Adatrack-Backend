package controllers

import (
	"fmt"
	"log/slog"
	"time"

	"ajb_gps/worker-alert/models"
)

// attachPos sets the alert's vehicle position from the telemetry sample.
func attachPos(rec *models.AlertRecord, tm models.TelemetryMessage) {
	if tm.Lat != 0 || tm.Lon != 0 {
		lat, lon := tm.Lat, tm.Lon
		rec.VehicleLat = &lat
		rec.VehicleLon = &lon
	}
}

// checkSpeeding triggers OVERSPEEDING when speed exceeds the effective limit
// (speed_configs vehicle-specific → global) plus its grace margin.
func (wa *WorkerAlert) checkSpeeding(s store, company string, tm models.TelemetryMessage, vehicleID uint64) {
	if tm.Speed <= 0 {
		return
	}
	cfg, ok, err := s.SpeedConfigFor(vehicleID)
	if err != nil {
		slog.Error("speed config lookup failed", "company", company, "imei", tm.IMEI, "error", err)
		wa.metrics.incError(company, "speed_config")
		return
	}
	limit := wa.cfg.GetSpeedLimit()
	margin := wa.cfg.GetGraceMargin()
	if ok { // konfigurasi DB menimpa default env
		limit = cfg.SpeedLimitKMH
		margin = cfg.GraceMargin
	}
	effective := limit + margin
	if tm.Speed <= effective {
		return
	}

	severity := "warning"
	if effective > 0 && tm.Speed > effective*1.5 {
		severity = "critical"
	}
	desc := fmt.Sprintf("Speed %.0f km/h exceeds limit %.0f km/h (+%.0f grace)", tm.Speed, limit, margin)
	rec := models.AlertRecord{
		VehicleID:   vehicleID,
		AlertType:   models.AlertTypeSpeed,
		Severity:    severity,
		Description: desc,
		Status:      models.AlertStatusOpen,
	}
	attachPos(&rec, tm)

	id, err := s.InsertAlert(rec)
	if err != nil {
		slog.Error("overspeed alert insert failed", "company", company, "imei", tm.IMEI, "error", err)
		wa.metrics.incError(company, "insert")
		return
	}
	rec.ID = id
	wa.metrics.incCreated(company, rec.AlertType, normalizeSeverity(severity))
	wa.publishAlert(company, tm.IMEI, rec, tm)
	wa.notifyAlert(s, company, tm, rec)
	slog.Info("overspeed alert created", "company", company, "alert_id", id,
		"imei", tm.IMEI, "speed", tm.Speed, "limit", effective)
}

// batteryDedupWindow guards against repeated BATTERY_LOW rows per device.
const batteryDedupWindow = 30 * time.Minute

// batteryThresholdPct is the low-battery threshold (%).
const batteryThresholdPct = 20

// checkBattery triggers BATTERY_LOW below the threshold with an in-memory
// dedup window + open-alert guard.
func (wa *WorkerAlert) checkBattery(s store, company string, tm models.TelemetryMessage, vehicleID uint64) {
	if tm.Battery == 0 || tm.Battery >= batteryThresholdPct {
		return // 0 = field tidak tersedia pada protokol tsb
	}
	key := company + "|" + tm.IMEI
	now := time.Now()
	wa.batteryMu.Lock()
	if last, ok := wa.batteryLast[key]; ok && now.Sub(last) < batteryDedupWindow {
		wa.batteryMu.Unlock()
		return
	}
	wa.batteryLast[key] = now
	wa.batteryMu.Unlock()

	open, err := s.HasOpenAlert(vehicleID, models.AlertTypeBattery)
	if err == nil && open {
		return
	}

	rec := models.AlertRecord{
		VehicleID:   vehicleID,
		AlertType:   models.AlertTypeBattery,
		Severity:    "medium",
		Description: fmt.Sprintf("Battery level low: %d%%", tm.Battery),
		Status:      models.AlertStatusOpen,
	}
	attachPos(&rec, tm)

	id, err := s.InsertAlert(rec)
	if err != nil {
		slog.Error("battery alert insert failed", "company", company, "imei", tm.IMEI, "error", err)
		wa.metrics.incError(company, "insert")
		return
	}
	rec.ID = id
	wa.metrics.incCreated(company, rec.AlertType, normalizeSeverity("medium"))
	wa.publishAlert(company, tm.IMEI, rec, tm)
	wa.notifyAlert(s, company, tm, rec)
	slog.Info("battery low alert created", "company", company, "alert_id", id, "imei", tm.IMEI, "battery", tm.Battery)
}
