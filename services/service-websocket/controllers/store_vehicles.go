package controllers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"adatrack_gps/service-websocket/models"
)

// vehicleColumns is the projection used by every vehicle read (additive columns
// of later phases are not required by B2).
const vehicleColumns = `id, imei, plate_number, COALESCE(make, ''), COALESCE(model, ''),
	COALESCE(variant, ''), COALESCE(color, ''), COALESCE(fuel_type, ''),
	COALESCE(vehicle_category_code, ''), COALESCE(vehicle_type_code, ''),
	driver_user_id, COALESCE(driver_name, ''), COALESCE(device_model, ''), status,
	to_char(deleted_at, 'YYYY-MM-DD"T"HH24:MI:SSOF')`

// ListVehicles returns the row-level filtered fleet page plus the total count.
func (s *PostgresStore) ListVehicles(ctx context.Context, q VehicleQuery) ([]models.Vehicle, int64, error) {
	pool, err := s.tenantPool(q.CompanyCode)
	if err != nil {
		return nil, 0, err
	}

	where := []string{}
	args := []any{}
	add := func(clause string, value any) {
		args = append(args, value)
		where = append(where, strings.Replace(clause, "?", "$"+itoa(int64(len(args))), 1))
	}

	if !q.IncludeDel {
		where = append(where, "deleted_at IS NULL")
	}
	if !q.AllVehicles {
		// Row-level RBAC: an empty grant list means "no vehicle visible at all"
		// (never "all vehicles" — PRD §9.2 server-side filtering).
		if len(q.AssignedIDs) == 0 {
			return []models.Vehicle{}, 0, nil
		}
		ids := make([]any, 0, len(q.AssignedIDs))
		for _, id := range q.AssignedIDs {
			ids = append(ids, id)
		}
		start := len(args)
		parts := make([]string, 0, len(ids))
		for i := range ids {
			args = append(args, ids[i])
			parts = append(parts, "$"+itoa(int64(start+i+1)))
		}
		where = append(where, "id IN ("+strings.Join(parts, ", ")+")")
	}
	if q.Status != "" {
		add("status = ?", q.Status)
	}
	if q.Search != "" {
		// Three bound parameters, one shared pattern: LIKE metacharacters are
		// escaped so the term can never widen the result set (PRD §8.5).
		pattern := "%" + escapeLike(q.Search) + "%"
		args = append(args, pattern, pattern, pattern)
		n := len(args)
		where = append(where, fmt.Sprintf(
			"(plate_number ILIKE $%d OR imei ILIKE $%d OR COALESCE(driver_name, '') ILIKE $%d)",
			n-2, n-1, n))
	}

	filter := ""
	if len(where) > 0 {
		filter = " WHERE " + strings.Join(where, " AND ")
	}

	var total int64
	if err := pool.DB.QueryRowContext(ctx, "SELECT count(*) FROM tm_vehicles"+filter, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("store: count vehicles: %w", err)
	}

	limit := q.Limit
	offset := (q.Page - 1) * q.Limit
	argsPage := append(append([]any{}, args...), limit, offset)
	query := "SELECT " + vehicleColumns + " FROM tm_vehicles" + filter +
		" ORDER BY plate_number, id LIMIT $" + itoa(int64(len(args)+1)) + " OFFSET $" + itoa(int64(len(args)+2))

	rows, err := pool.DB.QueryContext(ctx, query, argsPage...)
	if err != nil {
		return nil, 0, fmt.Errorf("store: list vehicles: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]models.Vehicle, 0, limit)
	for rows.Next() {
		v, err := scanVehicle(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("store: list vehicles rows: %w", err)
	}
	return out, total, nil
}

// VehicleByID loads one vehicle (nil when absent) so the caller can distinguish
// 404 (not found) from 403 (found but not assigned — PRD §3.1).
func (s *PostgresStore) VehicleByID(ctx context.Context, companyCode string, id int64, includeDeleted bool) (*models.Vehicle, error) {
	pool, err := s.tenantPool(companyCode)
	if err != nil {
		return nil, err
	}
	query := "SELECT " + vehicleColumns + " FROM tm_vehicles WHERE id = $1"
	if !includeDeleted {
		query += " AND deleted_at IS NULL"
	}
	rows, err := pool.DB.QueryContext(ctx, query, id)
	if err != nil {
		return nil, fmt.Errorf("store: vehicle by id: %w", err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("store: vehicle by id rows: %w", err)
		}
		return nil, nil
	}
	v, err := scanVehicle(rows)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// VehicleMeta loads the minimal vehicle identity used by the WebSocket fan-out
// (plate number) without touching the live state.
func (s *PostgresStore) VehicleMeta(ctx context.Context, companyCode string, id int64) (models.Vehicle, error) {
	pool, err := s.tenantPool(companyCode)
	if err != nil {
		return models.Vehicle{}, err
	}
	var v models.Vehicle
	err = pool.DB.QueryRowContext(ctx, `
		SELECT id, imei, plate_number FROM tm_vehicles
		WHERE id = $1 AND deleted_at IS NULL`, id).Scan(&v.ID, &v.IMEI, &v.PlateNumber)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return models.Vehicle{}, nil
		}
		return models.Vehicle{}, fmt.Errorf("store: vehicle meta: %w", err)
	}
	return v, nil
}

// scanVehicle maps a vehicle row set by vehicleColumns.
func scanVehicle(rows *sql.Rows) (models.Vehicle, error) {
	var (
		v         models.Vehicle
		driverID  sql.NullInt64
		deletedAt sql.NullString
	)
	if err := rows.Scan(&v.ID, &v.IMEI, &v.PlateNumber, &v.Make, &v.Model, &v.Variant,
		&v.Color, &v.FuelType, &v.CategoryCode, &v.TypeCode, &driverID, &v.DriverName,
		&v.DeviceModel, &v.Status, &deletedAt); err != nil {
		return models.Vehicle{}, fmt.Errorf("store: scan vehicle: %w", err)
	}
	if driverID.Valid {
		id := driverID.Int64
		v.DriverUserID = &id
	}
	if deletedAt.Valid {
		ts := deletedAt.String
		v.DeletedAt = &ts
	}
	return v, nil
}

// escapeLike neutralises LIKE metacharacters so a search term cannot widen the
// result set (PRD §8.5 whitelist/parameterized input handling).
func escapeLike(s string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(strings.TrimSpace(s))
}
