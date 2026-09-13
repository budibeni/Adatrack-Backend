package controllers

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"ajb_gps/worker-alert/models"
)

// offlineThreshold is how long a device must be silent before OFFLINE fires
// (env OFFLINE_AFTER_MINUTES, default 3 menit — selaras FR-1.3).
func (wa *WorkerAlert) offlineThreshold() time.Duration {
	return durationFromEnv("OFFLINE_AFTER_MINUTES", 3*time.Minute)
}

// checkOfflineMonitors scans every warmed tenant for silent devices. A device
// is OFFLINE when its Redis live-state key is missing/stale beyond threshold
// and no open OFFLINE alert exists yet (dedup).
func (wa *WorkerAlert) checkOfflineMonitors() {
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-wa.ctx.Done():
			return
		case <-tick.C:
			for _, c := range companiesFn(wa.tm) {
				if !c.IsActive {
					continue
				}
				wa.offlineScanCompany(c.Code)
			}
		}
	}
}

// vehicleRow is a minimal (id, imei) pair for the scan.
type vehicleRow struct {
	id   uint64
	imei string
}

// offlineScanCompany checks all active vehicles of one tenant.
func (wa *WorkerAlert) offlineScanCompany(code string) {
	st, err := newStoreFn(wa, code)
	if err != nil {
		wa.metrics.incError(code, "offline_pool")
		return
	}
	cs, ok := st.(*companyStore)
	if !ok {
		return
	}
	// READ path (replica): daftar vehicle aktif untuk scan OFFLINE berkala.
	rows, err := cs.ro.Query(
		`SELECT id, imei FROM vehicles WHERE deleted_at IS NULL AND status = 'active' AND COALESCE(imei,'') <> ''`)
	if err != nil {
		slog.Error("offline scan query failed", "company", code, "error", err)
		wa.metrics.incError(code, "offline_scan")
		return
	}
	vehicles := make([]vehicleRow, 0, 64)
	for rows.Next() {
		var v vehicleRow
		if err := rows.Scan(&v.id, &v.imei); err == nil {
			vehicles = append(vehicles, v)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		wa.metrics.incError(code, "offline_scan")
		return
	}

	for _, v := range vehicles {
		select {
		case <-wa.ctx.Done():
			return
		default:
		}
		if !wa.deviceSilent(code, v.imei) {
			continue
		}
		open, err := st.HasOpenAlert(v.id, models.AlertTypeOffline)
		if err != nil {
			slog.Error("offline dedup check failed", "company", code, "vehicle_id", v.id, "error", err)
			wa.metrics.incError(code, "offline_dedup")
			continue
		}
		if open {
			continue
		}
		desc := fmt.Sprintf("Device offline for more than %s", wa.offlineThreshold())
		rec := models.AlertRecord{
			VehicleID:   v.id,
			AlertType:   models.AlertTypeOffline,
			Severity:    "high",
			Description: desc,
			Status:      models.AlertStatusOpen,
		}
		id, err := st.InsertAlert(rec)
		if err != nil {
			slog.Error("offline alert insert failed", "company", code, "vehicle_id", v.id, "error", err)
			wa.metrics.incError(code, "insert")
			continue
		}
		rec.ID = id
		wa.metrics.incCreated(code, rec.AlertType, normalizeSeverity("high"))
		tm := models.TelemetryMessage{IMEI: v.imei}
		wa.publishAlert(code, v.imei, rec, tm)
		wa.notifyAlert(st, code, tm, rec)
		slog.Warn("offline alert created", "company", code, "alert_id", id, "imei", v.imei)
	}
}

// deviceSilent reports whether the live-state key for the IMEI is missing or
// older than the offline threshold. Keys follow worker-live:
// {prefix}{company}:vehicle:state:{imei}.
func (wa *WorkerAlert) deviceSilent(company, imei string) bool {
	ctx, cancel := contextWithTimeout(3 * time.Second)
	defer cancel()
	val, err := wa.redis.Client().Get(ctx, wa.redisStateKey(company, imei)).Result()
	if err != nil {
		return true // missing key → silent
	}
	state, err := decodeState(val)
	if err != nil {
		return true
	}
	ts, ok := state["timestamp"].(float64)
	if !ok || ts <= 0 {
		return true
	}
	return time.Since(time.Unix(int64(ts), 0)) > wa.offlineThreshold()
}

// decodeState parses the JSON object stored by worker-live.
func decodeState(val string) (map[string]interface{}, error) {
	var m map[string]interface{}
	if len(val) == 0 || val[0] != '{' {
		return nil, errDecodeState
	}
	if err := json.Unmarshal([]byte(val), &m); err != nil {
		return nil, err
	}
	return m, nil
}

var errDecodeState = errors.New("state payload not a JSON object")
