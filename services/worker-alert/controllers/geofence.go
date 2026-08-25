package controllers

import (
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"ajb_gps/worker-alert/models"
)

// checkGeofence detects entry/exit transitions for every active geofence of
// the vehicle. State (inside/outside per zone) disimpan di Redis hash
// {prefix}{company}:geofence_state:{imei} sesuai PRD B3; bila Redis gagal,
// worker tetap mendeteksi transisi pertama (state dianggap "outside") tanpa
// drop pesan.
func (wa *WorkerAlert) checkGeofence(s store, company string, tm models.TelemetryMessage, vehicleID uint64) {
	if tm.Lat == 0 && tm.Lon == 0 {
		return
	}
	defs, err := s.ActiveGeofences(vehicleID)
	if err != nil {
		slog.Error("geofence lookup failed", "company", company, "imei", tm.IMEI, "error", err)
		wa.metrics.incError(company, "geofence_lookup")
		return
	}
	if len(defs) == 0 {
		return
	}

	prev := wa.loadGeofenceState(company, tm.IMEI)
	changed := make(map[string]string, 2) // redis field → value

	for _, def := range defs {
		var inside bool
		switch def.AreaType {
		case "circle":
			inside = withinRadius(def.CenterLat, def.CenterLon, def.RadiusMeters, tm.Lat, tm.Lon)
		case "polygon":
			inside = len(def.Boundary) >= 3 && pointInPolygon(tm.Lat, tm.Lon, def.Boundary)
		default:
			continue
		}

		wasInside := prev[def.ID]
		field := strconv.FormatUint(def.ID, 10)
		if inside != wasInside {
			changed[field] = boolToInt(inside)
		}

		entryBreach := inside && !wasInside
		exitBreach := !inside && wasInside
		if !entryBreach && !exitBreach {
			continue
		}

		desc := fmt.Sprintf("Vehicle %s geofence '%s'", breachVerb(entryBreach), def.Name)
		rec := models.AlertRecord{
			VehicleID:   vehicleID,
			AlertType:   models.AlertTypeGeofence,
			Severity:    "warning",
			Description: desc,
			Status:      models.AlertStatusOpen,
		}
		attachPos(&rec, tm)

		id, err := s.InsertAlert(rec)
		if err != nil {
			slog.Error("geofence alert insert failed", "company", company, "imei", tm.IMEI,
				"geofence_id", def.ID, "error", err)
			wa.metrics.incError(company, "insert")
			continue
		}
		rec.ID = id
		wa.metrics.incCreated(company, rec.AlertType, normalizeSeverity("warning"))
		wa.publishAlert(company, tm.IMEI, rec, tm)
		wa.notifyAlert(s, company, tm, rec)
		slog.Info("geofence alert created", "company", company, "alert_id", id, "imei", tm.IMEI,
			"geofence", def.Name, "entry", entryBreach)
	}

	if len(changed) > 0 {
		wa.saveGeofenceState(company, tm.IMEI, changed)
	}
}

func breachVerb(entry bool) string {
	if entry {
		return "entered"
	}
	return "exited"
}

func boolToInt(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// loadGeofenceState reads the Redis hash into a map (missing key = all false).
func (wa *WorkerAlert) loadGeofenceState(company, imei string) map[uint64]bool {
	out := make(map[uint64]bool)
	ctx, cancel := contextWithTimeout(2 * time.Second)
	defer cancel()
	vals, err := wa.redis.Client().HGetAll(ctx, wa.geofenceStateKey(company, imei)).Result()
	if err != nil {
		slog.Warn("geofence state read failed (fallback outside)", "company", company, "imei", imei, "error", err)
		return out
	}
	for field, v := range vals {
		id, perr := strconv.ParseUint(field, 10, 64)
		if perr != nil {
			continue
		}
		out[id] = v == "1" || v == "true"
	}
	return out
}

// saveGeofenceState persists transition fields to the Redis hash.
func (wa *WorkerAlert) saveGeofenceState(company, imei string, fields map[string]string) {
	ctx, cancel := contextWithTimeout(2 * time.Second)
	defer cancel()
	if err := wa.redis.Client().HSet(ctx, wa.geofenceStateKey(company, imei), fields).Err(); err != nil {
		slog.Warn("geofence state write failed", "company", company, "imei", imei, "error", err)
	}
}
