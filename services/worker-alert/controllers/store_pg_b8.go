package controllers

// store_pg_b8.go — PostgreSQL implementation of the B8 driver-behaviour and
// maintenance-recovery store surface (migrations 022/023). Every statement is
// parameterized and company-scoped (PRD §6.2/§9.6).

import (
	"context"
	"fmt"
	"time"

	"adatrack_gps/worker-alert/models"
)

// InsertDriverEvent appends one td_driver_events row.
func (s *PostgresStore) InsertDriverEvent(ctx context.Context, company string, ev *models.DriverEvent) error {
	pool, err := s.tenantPool(company)
	if err != nil {
		return err
	}
	var driverID any
	if ev.DriverID > 0 {
		driverID = ev.DriverID
	}
	source := ev.Source
	if source == "" {
		source = "device_alarm"
	}
	_, err = pool.DB.ExecContext(ctx, `
INSERT INTO td_driver_events
	(company_code, vehicle_id, imei, driver_id, event_type, severity, speed_kmh,
	 speed_limit_kmh, duration_seconds, lat, lon, source, raw_code, "timestamp")
VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, 0)::numeric, NULLIF($8, 0)::numeric,
        $9, NULLIF($10, 0)::numeric, NULLIF($11, 0)::numeric, $12, NULLIF($13, 0), $14)`,
		company, ev.VehicleID, ev.IMEI, driverID, ev.EventType, ev.Severity,
		ev.SpeedKMH, ev.SpeedLimitKMH, ev.DurationSeconds, ev.Lat, ev.Lon,
		source, ev.RawCode, ev.Timestamp.UTC())
	if err != nil {
		return fmt.Errorf("store: insert driver event: %w", err)
	}
	return nil
}

// DailyDriverEventCounts aggregates one day of events for a vehicle.
func (s *PostgresStore) DailyDriverEventCounts(ctx context.Context, company string, vehicleID int64, day time.Time) (models.DriverEventCounts, error) {
	var c models.DriverEventCounts
	pool, err := s.tenantPool(company)
	if err != nil {
		return c, err
	}
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 1)

	row := pool.DB.QueryRowContext(ctx, `
SELECT
	COUNT(*) FILTER (WHERE event_type = 'harsh_acceleration'),
	COUNT(*) FILTER (WHERE event_type = 'harsh_braking'),
	COUNT(*) FILTER (WHERE event_type = 'harsh_cornering'),
	COUNT(*) FILTER (WHERE event_type = 'speeding'),
	COALESCE(SUM(duration_seconds) FILTER (WHERE event_type = 'speeding'), 0)
FROM td_driver_events
WHERE vehicle_id = $1 AND "timestamp" >= $2 AND "timestamp" < $3`,
		vehicleID, start, end)
	if err := row.Scan(&c.HarshAcceleration, &c.HarshBraking, &c.HarshCornering,
		&c.Speeding, &c.SpeedingSeconds); err != nil {
		return c, fmt.Errorf("store: driver event counts: %w", err)
	}
	return c, nil
}

// UpsertDriverScore writes/refreshes the daily th_driver_scores row.
func (s *PostgresStore) UpsertDriverScore(ctx context.Context, company string, sc *models.DriverScore) error {
	pool, err := s.tenantPool(company)
	if err != nil {
		return err
	}
	_, err = pool.DB.ExecContext(ctx, `
INSERT INTO th_driver_scores
	(company_code, vehicle_id, period_start, period_end,
	 harsh_acceleration_count, harsh_braking_count, harsh_cornering_count,
	 speeding_count, speeding_seconds, score, grade, computed_at)
VALUES ($1, $2, $3::date, $4::date, $5, $6, $7, $8, $9, $10, $11, CURRENT_TIMESTAMP)
ON CONFLICT (vehicle_id, period_start) DO UPDATE SET
	period_end               = EXCLUDED.period_end,
	harsh_acceleration_count = EXCLUDED.harsh_acceleration_count,
	harsh_braking_count      = EXCLUDED.harsh_braking_count,
	harsh_cornering_count    = EXCLUDED.harsh_cornering_count,
	speeding_count           = EXCLUDED.speeding_count,
	speeding_seconds         = EXCLUDED.speeding_seconds,
	score                    = EXCLUDED.score,
	grade                    = EXCLUDED.grade,
	computed_at              = CURRENT_TIMESTAMP,
	updated_at               = CURRENT_TIMESTAMP`,
		company, sc.VehicleID, sc.PeriodStart, sc.PeriodEnd,
		sc.HarshAccelerationCount, sc.HarshBrakingCount, sc.HarshCorneringCount,
		sc.SpeedingCount, sc.SpeedingSeconds, sc.Score, sc.Grade)
	if err != nil {
		return fmt.Errorf("store: upsert driver score: %w", err)
	}
	return nil
}

// MaintenanceSchedules lists the active schedules of a company.
func (s *PostgresStore) MaintenanceSchedules(ctx context.Context, company string) ([]models.MaintenanceSchedule, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return nil, err
	}
	rows, err := pool.DB.QueryContext(ctx, `
SELECT id, vehicle_id, name, maintenance_type,
       interval_km, interval_engine_hours, interval_days,
       last_service_at, last_service_odometer_km, last_service_engine_hours,
       reminder_km_before, reminder_days_before, last_reminder_at
FROM tm_maintenance_schedules
WHERE is_active = TRUE AND deleted_at IS NULL`)
	if err != nil {
		return nil, fmt.Errorf("store: maintenance schedules: %w", err)
	}
	defer rows.Close()

	var out []models.MaintenanceSchedule
	for rows.Next() {
		var m models.MaintenanceSchedule
		if err := rows.Scan(&m.ID, &m.VehicleID, &m.Name, &m.MaintenanceType,
			&m.IntervalKM, &m.IntervalEngineHours, &m.IntervalDays,
			&m.LastServiceAt, &m.LastServiceOdometerKM, &m.LastServiceEngineHrs,
			&m.ReminderKMBefore, &m.ReminderDaysBefore, &m.LastReminderAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// VehicleUsage returns the odometer/engine-hours snapshot per vehicle.
func (s *PostgresStore) VehicleUsage(ctx context.Context, company string) (map[int64]models.VehicleUsage, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return nil, err
	}
	rows, err := pool.DB.QueryContext(ctx, `
SELECT id, COALESCE(imei, ''), odometer_km::float8, engine_hours::float8
FROM tm_vehicles
WHERE deleted_at IS NULL AND is_active = TRUE`)
	if err != nil {
		return nil, fmt.Errorf("store: vehicle usage: %w", err)
	}
	defer rows.Close()

	out := map[int64]models.VehicleUsage{}
	for rows.Next() {
		var u models.VehicleUsage
		if err := rows.Scan(&u.VehicleID, &u.IMEI, &u.OdometerKM, &u.EngineHours); err != nil {
			return nil, err
		}
		out[u.VehicleID] = u
	}
	return out, rows.Err()
}

// TouchMaintenanceReminder stamps last_reminder_at after a reminder fired.
func (s *PostgresStore) TouchMaintenanceReminder(ctx context.Context, company string, scheduleID int64, at time.Time) error {
	pool, err := s.tenantPool(company)
	if err != nil {
		return err
	}
	_, err = pool.DB.ExecContext(ctx,
		`UPDATE tm_maintenance_schedules SET last_reminder_at = $1, updated_at = CURRENT_TIMESTAMP
		 WHERE id = $2 AND deleted_at IS NULL`, at.UTC(), scheduleID)
	if err != nil {
		return fmt.Errorf("store: touch maintenance reminder: %w", err)
	}
	return nil
}
