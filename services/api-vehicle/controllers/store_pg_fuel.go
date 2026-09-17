package controllers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"adatrack_gps/api-vehicle/models"
)

// fuelConfigColumns is the projection of tm_fuel_configs (migration 013).
const fuelConfigColumns = `id, vehicle_id, drop_threshold_percent,
refuel_threshold_percent, window_seconds, alert_severity, require_acc,
acc_stale_seconds, enabled, created_at, updated_at, deleted_at`

// scanFuelConfig scans one tm_fuel_configs row.
func scanFuelConfig(row interface{ Scan(...any) error }) (models.FuelConfig, error) {
	var (
		f         models.FuelConfig
		vehicleID sql.NullInt64
		deletedAt sql.NullTime
	)
	err := row.Scan(&f.ID, &vehicleID, &f.DropThresholdPct, &f.RefuelThresholdPct,
		&f.WindowSeconds, &f.Severity, &f.RequireACC, &f.ACCStaleSeconds,
		&f.Enabled, &f.CreatedAt, &f.UpdatedAt, &deletedAt)
	if err != nil {
		return f, err
	}
	if vehicleID.Valid {
		v := vehicleID.Int64
		f.VehicleID = &v
	}
	if deletedAt.Valid {
		s := deletedAt.Time.UTC().Format(time.RFC3339)
		f.DeletedAt = &s
	}
	return f, nil
}

// ListFuelConfigs returns the fuel configs of a tenant (soft-deleted excluded
// unless includeDeleted — same contract as speed configs).
func (s *PostgresStore) ListFuelConfigs(ctx context.Context, company string, includeDeleted bool) ([]models.FuelConfig, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return nil, err
	}
	query := "SELECT " + fuelConfigColumns + " FROM tm_fuel_configs"
	if !includeDeleted {
		query += " WHERE deleted_at IS NULL"
	}
	query += " ORDER BY id"
	rows, err := pool.DB.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("store: list fuel configs: %w", err)
	}
	defer rows.Close()
	out := []models.FuelConfig{}
	for rows.Next() {
		f, err := scanFuelConfig(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// FuelConfigByID loads one fuel config (nil when absent).
func (s *PostgresStore) FuelConfigByID(ctx context.Context, company string, id int64, includeDeleted bool) (*models.FuelConfig, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return nil, err
	}
	query := "SELECT " + fuelConfigColumns + " FROM tm_fuel_configs WHERE id = $1"
	if !includeDeleted {
		query += " AND deleted_at IS NULL"
	}
	f, err := scanFuelConfig(pool.DB.QueryRowContext(ctx, query, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: fuel config by id: %w", err)
	}
	return &f, nil
}

// CreateFuelConfig inserts one config (vehicle_id null = tenant-wide default,
// PRD §5.9.9; the vehicle row must exist otherwise — checked by the handler).
func (s *PostgresStore) CreateFuelConfig(ctx context.Context, company string, fc *models.FuelConfig, createdBy int64) (int64, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return 0, err
	}
	var id int64
	err = pool.DB.QueryRowContext(ctx, `
INSERT INTO tm_fuel_configs
(vehicle_id, drop_threshold_percent, refuel_threshold_percent, window_seconds,
 alert_severity, require_acc, acc_stale_seconds, enabled, created_by)
VALUES (NULLIF($1, 0), $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
		derefInt64(fc.VehicleID), fc.DropThresholdPct, fc.RefuelThresholdPct,
		fc.WindowSeconds, fc.Severity, fc.RequireACC, fc.ACCStaleSeconds,
		fc.Enabled, createdBy).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("store: create fuel config: %w", err)
	}
	fc.ID = id
	return id, nil
}

// UpdateFuelConfig updates the mutable columns of one config.
func (s *PostgresStore) UpdateFuelConfig(ctx context.Context, company string, fc *models.FuelConfig, updatedBy int64) error {
	pool, err := s.tenantPool(company)
	if err != nil {
		return err
	}
	_, err = pool.DB.ExecContext(ctx, `
UPDATE tm_fuel_configs
SET vehicle_id = NULLIF($2, 0), drop_threshold_percent = $3,
    refuel_threshold_percent = $4, window_seconds = $5, alert_severity = $6,
    require_acc = $7, acc_stale_seconds = $8, enabled = $9,
    updated_by = $10, updated_at = CURRENT_TIMESTAMP
WHERE id = $1 AND deleted_at IS NULL`,
		fc.ID, derefInt64(fc.VehicleID), fc.DropThresholdPct, fc.RefuelThresholdPct,
		fc.WindowSeconds, fc.Severity, fc.RequireACC, fc.ACCStaleSeconds,
		fc.Enabled, updatedBy)
	return err
}

// SoftDeleteFuelConfig flags a config (audit-visible).
func (s *PostgresStore) SoftDeleteFuelConfig(ctx context.Context, company string, id, by int64, reason string) error {
	pool, err := s.tenantPool(company)
	if err != nil {
		return err
	}
	_, err = pool.DB.ExecContext(ctx, `
UPDATE tm_fuel_configs SET deleted_at = CURRENT_TIMESTAMP, deleted_by = $2,
delete_reason = $3, updated_at = CURRENT_TIMESTAMP
WHERE id = $1 AND deleted_at IS NULL`, id, by, reason)
	return err
}

// RestoreFuelConfig reactivates a soft-deleted config.
func (s *PostgresStore) RestoreFuelConfig(ctx context.Context, company string, id int64) error {
	pool, err := s.tenantPool(company)
	if err != nil {
		return err
	}
	_, err = pool.DB.ExecContext(ctx, `
UPDATE tm_fuel_configs SET deleted_at = NULL, deleted_by = NULL, delete_reason = NULL,
updated_at = CURRENT_TIMESTAMP
WHERE id = $1 AND deleted_at IS NOT NULL`, id)
	return err
}

// ListFuelHistory returns one vehicle's td_fuel_logs inside the [from, to]
// range, newest first, with the total row count for pagination (FR-7.7).
// Rows are always tenant-scoped (company schema) + row-level vehicle.
func (s *PostgresStore) ListFuelHistory(ctx context.Context, company string, vehicleID int64, from, to time.Time, page, limit int) ([]models.FuelLog, int64, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return nil, 0, err
	}
	conds := " WHERE vehicle_id = $1"
	args := []any{vehicleID}
	if !from.IsZero() {
		conds += fmt.Sprintf(" AND \"timestamp\" >= $%d", len(args)+1)
		args = append(args, from)
	}
	if !to.IsZero() {
		conds += fmt.Sprintf(" AND \"timestamp\" <= $%d", len(args)+1)
		args = append(args, to)
	}
	var total int64
	if err := pool.DB.QueryRowContext(ctx,
		"SELECT count(*) FROM td_fuel_logs"+conds, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("store: fuel history count: %w", err)
	}
	query := `SELECT "timestamp", fuel_level, fuel_volume, fuel_temp_c,
latitude, longitude, acc_status FROM td_fuel_logs` + conds +
		fmt.Sprintf(" ORDER BY \"timestamp\" DESC LIMIT %d OFFSET %d", limit, (page-1)*limit)
	rows, err := pool.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("store: fuel history: %w", err)
	}
	defer rows.Close()
	out := []models.FuelLog{}
	for rows.Next() {
		var (
			l                   models.FuelLog
			level, volume, temp sql.NullFloat64
			lat, lon            sql.NullFloat64
			acc                 sql.NullInt64
			ts                  time.Time
		)
		if err := rows.Scan(&ts, &level, &volume, &temp, &lat, &lon, &acc); err != nil {
			return nil, 0, err
		}
		l.Timestamp = ts.UTC().Format(time.RFC3339)
		if level.Valid {
			v := level.Float64
			l.FuelLevel = &v
		}
		if volume.Valid {
			v := volume.Float64
			l.FuelVolume = &v
		}
		if temp.Valid {
			v := temp.Float64
			l.FuelTempC = &v
		}
		if lat.Valid {
			v := lat.Float64
			l.Lat = &v
		}
		if lon.Valid {
			v := lon.Float64
			l.Lon = &v
		}
		if acc.Valid {
			v := acc.Int64 == 1
			l.ACC = &v
		}
		out = append(out, l)
	}
	return out, total, rows.Err()
}
