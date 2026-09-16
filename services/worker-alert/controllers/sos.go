package controllers

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"ajb_gps/worker-alert/models"
)

// sosDedupKeyPrefix is the Redis namespace of the per-device SOS cooldown
// (SOS_COOLDOWN_SECONDS — repeated alarm frames from the same device collapse).
const sosDedupKeyPrefix = "alert:sos:cooldown:"

// detectSOS raises a CRITICAL SOS alert for device alarm frames:
// GT06 0x26/0x27 (alarm reason byte in `alarm_code`) and 0x19 (LBS alarm,
// `alarm_lbs` marker) — PRD §5.9.5.
func (w *Worker) detectSOS(ctx context.Context, t models.TelemetryMessage, now time.Time) {
	if t.AlarmCode == 0 && !t.AlarmLBS {
		return
	}
	cooldown := time.Duration(w.cfg.Alert.SOSCooldownSeconds) * time.Second
	if cooldown <= 0 {
		cooldown = 60 * time.Second
	}
	ok, err := w.red.SetNX(ctx, sosDedupKeyPrefix+t.CompanyCode+":"+t.IMEI, "1", cooldown)
	if err != nil {
		slog.Warn("worker-alert: SOS cooldown check failed", "imei", t.IMEI, "error", err)
	} else if !ok {
		alertsDeduped.WithLabelValues(models.AlertSOS, t.CompanyCode).Inc()
		return
	}

	alarmSource := "gt06_0x" + strconv.FormatUint(uint64(t.AlarmCode), 16)
	if t.AlarmLBS {
		alarmSource = "gt06_0x19_lbs"
	}
	alert := &models.Alert{
		Type:        models.AlertSOS,
		Severity:    models.SeverityCritical,
		VehicleID:   t.VehicleID,
		IMEI:        t.IMEI,
		CompanyCode: t.CompanyCode,
		Lat:         t.Lat,
		Lon:         t.Lon,
		Speed:       t.Speed,
		DedupKey: "sos:" + t.CompanyCode + ":" + strconv.FormatInt(t.VehicleID, 10) +
			":" + t.IMEI,
		Metadata: map[string]any{
			"alarm_source":  alarmSource,
			"alarm_code":    t.AlarmCode,
			"lbs_alarm":     t.AlarmLBS,
			"battery_level": t.Battery,
			"gsm_signal":    t.GsmSignal,
		},
	}
	if _, err := w.engine.RaiseAlert(ctx, alert); err != nil {
		slog.Error("worker-alert: SOS alert failed", "vehicle_id", t.VehicleID, "error", err)
	}
}
