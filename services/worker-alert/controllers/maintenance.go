package controllers

// maintenance.go — B8 maintenance scheduling reminder engine (PRD §21.2 row 5,
// §5.10 item 1.4 "Maintenance menyambung B8").
//
// A schedule fires when ANY configured dimension is reached:
//
//	km          usage.OdometerKM     >= base_odometer + interval_km   - reminder_km_before
//	engine hrs  usage.EngineHours    >= base_hours    + interval_hours - reminder_hours_before
//	calendar    today                >= last_service_at + interval_days - reminder_days_before
//
// The usage snapshot comes from `tm_vehicles.odometer_km` / `engine_hours` (the
// B7.1 accumulators maintained by worker-live), the schedule rows from
// `tm_maintenance_schedules` (migration 023). The reminder is delivered through
// the normal alert pipeline (`maintenance_due` + notify fan-out) and the same
// schedule stays quiet for MAINTENANCE_REMINDER_COOLDOWN_HOURS after firing, so a
// vehicle that is overdue does not generate a storm of notifications.

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"time"

	"adatrack_gps/worker-alert/models"
)

// maintenanceLoop evaluates the schedules of every known company.
func (w *Worker) maintenanceLoop(ctx context.Context) {
	for _, company := range w.companiesSnapshot() {
		if err := w.sweepMaintenance(ctx, company, time.Now().UTC()); err != nil {
			slog.Warn("worker-alert: maintenance sweep failed", "company", company, "error", err)
		}
	}
}

// sweepMaintenance evaluates every active schedule of one company.
func (w *Worker) sweepMaintenance(ctx context.Context, company string, now time.Time) error {
	schedules, err := w.store.MaintenanceSchedules(ctx, company)
	if err != nil {
		return err
	}
	if len(schedules) == 0 {
		return nil
	}
	usage, err := w.store.VehicleUsage(ctx, company)
	if err != nil {
		return err
	}

	for i := range schedules {
		sc := &schedules[i]
		use, ok := usage[sc.VehicleID]
		if !ok {
			continue // deleted/inactive vehicle: nothing to measure
		}
		reason, due := maintenanceDue(sc, use, now)
		if !due {
			continue
		}
		if sc.LastReminderAt != nil && now.Sub(*sc.LastReminderAt) < w.cfg.Driver.MaintenanceCooldown {
			continue // already reminded recently (cooldown)
		}
		alert := &models.Alert{
			Type:        models.AlertMaintenanceDue,
			Severity:    models.SeverityMedium,
			VehicleID:   sc.VehicleID,
			IMEI:        use.IMEI,
			CompanyCode: company,
			DedupKey:    "maintenance:" + company + ":" + strconv.FormatInt(sc.VehicleID, 10) + ":" + strconv.FormatInt(sc.ID, 10),
			Metadata: map[string]any{
				"schedule_id":      sc.ID,
				"name":             sc.Name,
				"maintenance_type": sc.MaintenanceType,
				"reason":           reason,
				"odometer_km":      use.OdometerKM,
				"engine_hours":     use.EngineHours,
			},
			DetectedAt: now,
		}
		if _, err := w.engine.RaiseAlert(ctx, alert); err != nil {
			slog.Error("worker-alert: maintenance reminder failed",
				"company", company, "schedule_id", sc.ID, "error", err)
			continue
		}
		if err := w.store.TouchMaintenanceReminder(ctx, company, sc.ID, now); err != nil {
			slog.Warn("worker-alert: maintenance reminder timestamp failed",
				"company", company, "schedule_id", sc.ID, "error", err)
		}
		maintenanceReminders.WithLabelValues(sc.MaintenanceType).Inc()
		slog.Info("maintenance reminder raised", "company", company, "vehicle_id", sc.VehicleID,
			"schedule_id", sc.ID, "reason", reason)
	}
	return nil
}

// maintenanceDue reports whether a schedule has reached one of its thresholds and
// returns the human-readable reason (which dimension fired).
func maintenanceDue(sc *models.MaintenanceSchedule, use models.VehicleUsage, now time.Time) (string, bool) {
	if sc.IntervalKM != nil {
		base := 0.0
		if sc.LastServiceOdometerKM != nil {
			base = *sc.LastServiceOdometerKM
		}
		if use.OdometerKM >= base+*sc.IntervalKM-sc.ReminderKMBefore {
			return fmt.Sprintf("odometer %.1f km (due at %.1f km)", use.OdometerKM, base+*sc.IntervalKM), true
		}
	}
	if sc.IntervalEngineHours != nil {
		base := 0.0
		if sc.LastServiceEngineHrs != nil {
			base = *sc.LastServiceEngineHrs
		}
		// The engine-hours margin is expressed in hours; the reference reuses the
		// kilometre margin only as an upper bound so a schedule can never fire
		// earlier than 1/10 of its interval.
		margin := math.Min(sc.ReminderKMBefore, *sc.IntervalEngineHours/10)
		if use.EngineHours >= base+*sc.IntervalEngineHours-margin {
			return fmt.Sprintf("engine hours %.1f h (due at %.1f h)", use.EngineHours, base+*sc.IntervalEngineHours), true
		}
	}
	if sc.IntervalDays != nil {
		base := now
		if sc.LastServiceAt != nil {
			base = *sc.LastServiceAt
		}
		dueAt := base.AddDate(0, 0, *sc.IntervalDays)
		if !now.Before(dueAt.AddDate(0, 0, -sc.ReminderDaysBefore)) {
			return fmt.Sprintf("calendar due %s", dueAt.Format("2006-01-02")), true
		}
	}
	return "", false
}
