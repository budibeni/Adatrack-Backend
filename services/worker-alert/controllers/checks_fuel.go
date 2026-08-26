package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"ajb_gps/worker-alert/models"
)

// fuelReading is the Redis-persisted last fuel observation per device
// ({prefix}{company}:fuel_state:{imei}, B5a).
type fuelReading struct {
	Level     float64 `json:"level"`     // fuel_level saat pembacaan terakhir
	Timestamp int64   `json:"timestamp"` // unix epoch pembacaan
	Volume    float64 `json:"volume,omitempty"`
}

// checkFuel (B5a) detects FUEL_DROP (critical) and REFUEL (medium) by
// comparing the current reading against the last one stored in Redis
// {prefix}{company}:fuel_state:{imei}. Thresholds come from fuel_configs
// (vehicle-specific row wins over global default); a disabled / missing
// config disables detection entirely. ACC ON is required for FUEL_DROP so
// normal engine-off consumption never triggers it.
func (wa *WorkerAlert) checkFuel(s store, company string, tm models.TelemetryMessage, vehicleID uint64) {
	if tm.FuelLevel == nil {
		return
	}
	cfg, ok, err := s.FuelConfigFor(vehicleID)
	if err != nil {
		slog.Error("fuel config lookup failed", "company", company, "imei", tm.IMEI, "error", err)
		wa.metrics.incError(company, "fuel_config")
		return
	}
	if ok && !cfg.Enabled {
		return // deteksi dimatikan untuk vehicle/company ini
	}
	dropThreshold, refuelThreshold := wa.cfg.GetFuelDropThreshold(), wa.cfg.GetFuelRefuelThreshold()
	window := wa.cfg.GetFuelWindow()
	if ok {
		dropThreshold = cfg.DropThreshold
		refuelThreshold = cfg.RefuelThreshold
		if cfg.WindowSeconds > 0 {
			window = time.Duration(cfg.WindowSeconds) * time.Second
		}
	}

	ctx, cancel := contextWithTimeout(3 * time.Second)
	defer cancel()

	key := wa.fuelStateKey(company, tm.IMEI)
	var prev fuelReading
	if raw, err := wa.redis.Client().Get(ctx, key).Result(); err == nil && raw != "" {
		if err := jsonUnmarshal([]byte(raw), &prev); err != nil {
			slog.Warn("fuel state unreadable; treating as first reading",
				"company", company, "imei", tm.IMEI, "error", err)
			prev = fuelReading{}
		}
	} else if err != nil {
		// redis miss (belum ada state) → perlakukan sebagai pembacaan awal;
		// error non-nil lain tetap dicatat tapi tidak memblok alur.
		if ctx.Err() == nil {
			slog.Debug("no prior fuel state", "company", company, "imei", tm.IMEI)
		}
	}

	now := time.Now().Unix()
	cur := fuelReading{Level: *tm.FuelLevel, Timestamp: now, Volume: volumeOf(tm)}
	saveErr := wa.saveFuelState(ctx, key, cur)

	// Pembacaan pertama / window belum penuh → simpan & tunggu delta berikutnya.
	first := prev.Timestamp == 0
	tooSoon := !first && now-prev.Timestamp < int64(window.Seconds())
	if first || tooSoon {
		if saveErr != nil {
			wa.logFuelSaveErr(company, tm.IMEI, saveErr)
		}
		return
	}

	delta := cur.Level - prev.Level
	switch {
	case delta <= -dropThreshold:
		wa.raiseFuelAlert(s, company, tm, vehicleID, models.AlertTypeFuelDrop,
			"critical",
			fmt.Sprintf("Fuel dropped %.1f in %ds (from %.1f to %.1f)",
				-delta, now-prev.Timestamp, prev.Level, cur.Level))
	case delta >= refuelThreshold:
		wa.raiseFuelAlert(s, company, tm, vehicleID, models.AlertTypeRefuel,
			"low",
			fmt.Sprintf("Refuel +%.1f in %ds (from %.1f to %.1f)",
				delta, now-prev.Timestamp, prev.Level, cur.Level))
	}
	if saveErr != nil {
		wa.logFuelSaveErr(company, tm.IMEI, saveErr)
	}
}

// raiseFuelAlert inserts the alert row (open-alert guarded), publishes it on
// alert.fuel.<company> and dispatches notifications per preference.
func (wa *WorkerAlert) raiseFuelAlert(s store, company string, tm models.TelemetryMessage,
	vehicleID uint64, alertType, severity, desc string) {

	open, err := s.HasOpenAlert(vehicleID, alertType)
	if err != nil {
		slog.Error("fuel open-alert guard failed", "company", company, "imei", tm.IMEI, "error", err)
		wa.metrics.incError(company, "fuel_guard")
		return
	}
	if open {
		return // dedup: satu alert open per tipe per vehicle
	}

	rec := models.AlertRecord{
		VehicleID:   vehicleID,
		AlertType:   alertType,
		Severity:    severity,
		Description: desc,
		Status:      models.AlertStatusOpen,
	}
	attachPos(&rec, tm)

	id, err := s.InsertAlert(rec)
	if err != nil {
		slog.Error("fuel alert insert failed", "company", company,
			"type", alertType, "imei", tm.IMEI, "error", err)
		wa.metrics.incError(company, "insert")
		return
	}
	rec.ID = id
	wa.metrics.incCreated(company, rec.AlertType, normalizeSeverity(severity))
	wa.publishAlert(company, tm.IMEI, rec, tm)
	wa.notifyAlert(s, company, tm, rec)
	slog.Info("fuel alert created", "company", company, "alert_id", id,
		"type", alertType, "imei", tm.IMEI, "desc", desc)
}

// saveFuelState persists the latest reading (best-effort, TTL 24 jam agar
// state basi tidak menyesatkan setelah device lama offline).
func (wa *WorkerAlert) saveFuelState(ctx context.Context, key string, r fuelReading) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return wa.redis.Client().Set(ctx, key, data, 24*time.Hour).Err()
}

func (wa *WorkerAlert) logFuelSaveErr(company, imei string, err error) {
	slog.Warn("failed to persist fuel state", "company", company, "imei", imei, "error", err)
	wa.metrics.incError(company, "fuel_state")
}

func volumeOf(tm models.TelemetryMessage) float64 {
	if tm.FuelVolume != nil {
		return *tm.FuelVolume
	}
	return 0
}
