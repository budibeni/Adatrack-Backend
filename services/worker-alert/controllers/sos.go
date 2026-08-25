package controllers

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"ajb_gps/worker-alert/models"
)

// sosCooldown dedups repeated SOS events per device (PRD B3: dedup/cooldown).
func (wa *WorkerAlert) sosCooldown() time.Duration {
	return durationFromEnv("SOS_COOLDOWN_SECONDS", 60*time.Second)
}

// isSOS reports whether the telemetry sample carries an SOS event. Devices
// signal SOS via the protocol alarm field; ingestion forwards it in "raw"
// or a dedicated flag, so we inspect both defensively.
func isSOS(tm models.TelemetryMessage) bool {
	if strings.Contains(strings.ToLower(tm.Raw), "sos") {
		return true
	}
	return false
}

// handleSOS creates a CRITICAL SOS alert (lifecycle OPEN; ACK via REST API),
// publishes alert.sos.<company>.<IMEI> and dispatches notifications.
func (wa *WorkerAlert) handleSOS(s store, company string, tm models.TelemetryMessage, vehicleID uint64) {
	key := company + "|" + tm.IMEI
	now := time.Now()

	wa.sosMu.Lock()
	if last, ok := wa.sosLast[key]; ok && now.Sub(last) < wa.sosCooldown() {
		wa.sosMu.Unlock()
		slog.Info("SOS cooldown active", "company", company, "imei", tm.IMEI, "last", last)
		return
	}
	wa.sosLast[key] = now
	wa.sosMu.Unlock()

	rec := models.AlertRecord{
		VehicleID:   vehicleID,
		AlertType:   models.AlertTypeSOS,
		Severity:    "critical",
		Description: fmt.Sprintf("SOS triggered by device %s — immediate attention required", tm.IMEI),
		Status:      models.AlertStatusOpen,
	}
	attachPos(&rec, tm)

	id, err := s.InsertAlert(rec)
	if err != nil {
		slog.Error("SOS alert insert failed", "company", company, "imei", tm.IMEI, "error", err)
		wa.metrics.incError(company, "insert")
		return
	}
	rec.ID = id
	wa.metrics.incCreated(company, rec.AlertType, normalizeSeverity("critical"))
	wa.publishAlert(company, tm.IMEI, rec, tm) // alert.sos.<company>.<IMEI>
	wa.notifyAlert(s, company, tm, rec)
	slog.Warn("SOS alert created and published", "company", company, "alert_id", id, "imei", tm.IMEI,
		"lat", tm.Lat, "lon", tm.Lon)
}
