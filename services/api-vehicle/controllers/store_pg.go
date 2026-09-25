package controllers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"adatrack_gps/api-vehicle/models"
	"adatrack_gps/internal"
	"adatrack_gps/internal/tenant"
)

// PostgresStore implements Store on top of the shared tenant manager (master
// pool + one pre-warmed pool per company schema, PRD §6.2).
type PostgresStore struct {
	tenants *tenant.Manager
}

// NewPostgresStore wraps the tenant manager.
func NewPostgresStore(tm *tenant.Manager) *PostgresStore { return &PostgresStore{tenants: tm} }

// tenantPool resolves a company code to its pool (code normalised upstream —
// nothing user-controlled is interpolated into SQL, PRD §9.6).
func (s *PostgresStore) tenantPool(companyCode string) (*internal.DBPool, error) {
	return s.tenants.DB(strings.ToUpper(strings.TrimSpace(companyCode)))
}

// itoa is a tiny helper for placeholder indices.
func itoa(v int) string { return fmt.Sprintf("%d", v) }

// escapeLike escapes LIKE metacharacters (PRD §8.5: search cannot widen).
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "%", "\\%")
	return strings.ReplaceAll(s, "_", "\\_")
}

// nullableString maps "" to SQL NULL.
func nullableString(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}

// UserByID loads the auth authority row (master `tm_users`, PRD §9.1).
func (s *PostgresStore) UserByID(ctx context.Context, id int64) (*UserRecord, error) {
	var u UserRecord
	err := s.tenants.Master().DB.QueryRowContext(ctx, `
SELECT id, COALESCE(company_code, ''), email, global_role, is_active,
			COALESCE(must_change_password, FALSE)
FROM tm_users
WHERE id = $1 AND deleted_at IS NULL`, id).
		Scan(&u.ID, &u.CompanyCode, &u.Email, &u.GlobalRole, &u.IsActive, &u.MustChangePassword)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: user by id: %w", err)
	}
	return &u, nil
}

// TenantAccess reads the membership row of one user in one tenant.
func (s *PostgresStore) TenantAccess(ctx context.Context, companyCode string, userID int64) (string, bool, bool, error) {
	pool, err := s.tenantPool(companyCode)
	if err != nil {
		return "", false, false, err
	}
	var role string
	var active bool
	err = pool.DB.QueryRowContext(ctx, `
SELECT COALESCE(role_override, ''), is_active
FROM tm_user_company_access
WHERE user_id = $1 AND deleted_at IS NULL`, userID).Scan(&role, &active)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, false, nil
	}
	if err != nil {
		return "", false, false, fmt.Errorf("store: tenant access: %w", err)
	}
	return role, active, true, nil
}

// AssignedVehicleIDs returns the row-level grants of one user (PRD §9.2).
func (s *PostgresStore) AssignedVehicleIDs(ctx context.Context, companyCode string, userID int64) ([]int64, error) {
	pool, err := s.tenantPool(companyCode)
	if err != nil {
		return nil, err
	}
	rows, err := pool.DB.QueryContext(ctx, `
SELECT vehicle_id FROM tm_user_vehicles
WHERE user_id = $1 AND deleted_at IS NULL`, userID)
	if err != nil {
		return nil, fmt.Errorf("store: assigned vehicles: %w", err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// vehicleColumns is the projection shared by the vehicle read paths.
const vehicleColumns = `id, imei, plate_number, make, model, year_of_manufacture,
color, fuel_type, vehicle_category_code, vehicle_type_code, driver_user_id,
driver_name, device_model, status, last_seen_at, current_lat, current_lon,
current_speed, created_at, updated_at, deleted_at`

// scanVehicle scans one tm_vehicles row into the DTO.
func scanVehicle(row interface{ Scan(...any) error }) (models.Vehicle, error) {
	var (
		v         models.Vehicle
		lastSeen  sql.NullTime
		createdAt time.Time
		updatedAt time.Time
		deletedAt sql.NullTime
	)
	err := row.Scan(&v.ID, &v.IMEI, &v.PlateNumber, &v.Make, &v.Model, &v.Year,
		&v.Color, &v.FuelType, &v.CategoryCode, &v.TypeCode, &v.DriverUserID,
		&v.DriverName, &v.DeviceModel, &v.Status, &lastSeen, &v.CurrentLat,
		&v.CurrentLon, &v.CurrentSpeed, &createdAt, &updatedAt, &deletedAt)
	if err != nil {
		return v, err
	}
	if lastSeen.Valid {
		s := lastSeen.Time.UTC().Format(time.RFC3339)
		v.LastSeenAt = &s
	}
	v.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	v.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
	if deletedAt.Valid {
		s := deletedAt.Time.UTC().Format(time.RFC3339)
		v.DeletedAt = &s
	}
	return v, nil
}

// ListVehicles returns the fleet page honouring the row-level grants.
func (s *PostgresStore) ListVehicles(ctx context.Context, q VehicleQuery) ([]models.Vehicle, int64, error) {
	// Read/write split (PRD §13): a pure list read goes through the tenant read
	// router — replica first (when configured), primary on failure. Detail
	// lookups (`*ByID`) deliberately stay on the primary because the PATCH
	// handlers reuse them and a stale replica read there could lose an update.
	where := []string{}
	args := []any{}
	add := func(clause string, value any) {
		args = append(args, value)
		where = append(where, strings.Replace(clause, "?", "$"+itoa(len(args)), 1))
	}
	if !q.IncludeDel {
		where = append(where, "deleted_at IS NULL")
	}
	if !q.AllVehicles {
		if len(q.AssignedIDs) == 0 {
			return []models.Vehicle{}, 0, nil
		}
		parts := make([]string, 0, len(q.AssignedIDs))
		for _, id := range q.AssignedIDs {
			args = append(args, id)
			parts = append(parts, "$"+itoa(len(args)))
		}
		where = append(where, "id IN ("+strings.Join(parts, ", ")+")")
	}
	if q.Status != "" {
		add("status = ?", q.Status)
	}
	if q.Search != "" {
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
	if err := s.tenants.ReadQueryRow(ctx, q.CompanyCode,
		"SELECT count(*) FROM tm_vehicles"+filter, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("store: count vehicles: %w", err)
	}
	limit := q.Limit
	offset := (q.Page - 1) * q.Limit
	argsPage := append(append([]any{}, args...), limit, offset)
	query := "SELECT " + vehicleColumns + " FROM tm_vehicles" + filter +
		" ORDER BY plate_number, id LIMIT $" + itoa(len(args)+1) + " OFFSET $" + itoa(len(args)+2)

	rows, err := s.tenants.ReadQuery(ctx, q.CompanyCode, query, argsPage...)
	if err != nil {
		return nil, 0, fmt.Errorf("store: list vehicles: %w", err)
	}
	defer rows.Close()
	out := make([]models.Vehicle, 0, limit)
	for rows.Next() {
		v, err := scanVehicle(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, v)
	}
	return out, total, rows.Err()
}

// VehicleByID loads one vehicle (nil when absent) so the caller can distinguish
// 404 (not found) from 403 (found but not assigned — PRD §3.1).
func (s *PostgresStore) VehicleByID(ctx context.Context, company string, id int64, includeDeleted bool) (*models.Vehicle, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return nil, err
	}
	query := "SELECT " + vehicleColumns + " FROM tm_vehicles WHERE id = $1"
	if !includeDeleted {
		query += " AND deleted_at IS NULL"
	}
	v, err := scanVehicle(pool.DB.QueryRowContext(ctx, query, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: vehicle by id: %w", err)
	}
	return &v, nil
}

// IMEIExists reports whether another ACTIVE vehicle already uses the IMEI.
func (s *PostgresStore) IMEIExists(ctx context.Context, company, imei string, excludeID int64) (bool, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return false, err
	}
	var n int
	err = pool.DB.QueryRowContext(ctx, `
SELECT count(*) FROM tm_vehicles
WHERE imei = $1 AND id <> $2 AND deleted_at IS NULL`, imei, excludeID).Scan(&n)
	return n > 0, err
}

// CreateVehicle inserts one fleet row and syncs the master IMEI map.
func (s *PostgresStore) CreateVehicle(ctx context.Context, company string, v *models.Vehicle, createdBy int64) (int64, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return 0, err
	}
	var id int64
	err = pool.DB.QueryRowContext(ctx, `
INSERT INTO tm_vehicles (imei, plate_number, make, model, year_of_manufacture,
color, fuel_type, vehicle_category_code, vehicle_type_code,
driver_user_id, driver_name, device_model, status, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12,
COALESCE($13, 'active'), $14)
RETURNING id`,
		v.IMEI, v.PlateNumber, v.Make, v.Model, v.Year, v.Color, v.FuelType,
		v.CategoryCode, v.TypeCode, v.DriverUserID, v.DriverName, v.DeviceModel,
		nullableString(v.Status), createdBy).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("store: create vehicle: %w", err)
	}
	v.ID = id
	if err := s.SyncIMEIMap(ctx, v.IMEI, company, id); err != nil {
		return 0, err
	}
	return id, nil
}

// UpdateVehicle updates the mutable columns of one fleet row.
//
// The IMEI is normally immutable (it IS the anti-spoofing identity), but a fleet
// does replace a broken tracker, so a VALIDATED re-point is supported: the caller
// has already checked the 15-digit form and the tenant uniqueness, and here the
// master allowlist map is moved with the vehicle (old row deactivated, new row
// upserted) so the device resolves again immediately (FR-1.4 + B10 gap).
func (s *PostgresStore) UpdateVehicle(ctx context.Context, company string, v *models.Vehicle, updatedBy int64) error {
	pool, err := s.tenantPool(company)
	if err != nil {
		return err
	}
	var currentIMEI string
	err = pool.DB.QueryRowContext(ctx, `
SELECT imei FROM tm_vehicles WHERE id = $1 AND deleted_at IS NULL`, v.ID).Scan(&currentIMEI)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("store: update vehicle (read current imei): %w", err)
	}
	imeiChanged := v.IMEI != "" && v.IMEI != currentIMEI

	const updateSQL = `
UPDATE tm_vehicles SET plate_number = $2, make = $3, model = $4,
year_of_manufacture = $5, color = $6, fuel_type = $7,
vehicle_category_code = $8, vehicle_type_code = $9, driver_user_id = $10,
driver_name = $11, device_model = $12, status = $13, updated_by = $14,
imei = CASE WHEN $15 = '' THEN imei ELSE $15 END,
updated_at = CURRENT_TIMESTAMP
WHERE id = $1 AND deleted_at IS NULL`

	newIMEI := ""
	if imeiChanged {
		newIMEI = v.IMEI
	}

	tag, err := pool.DB.ExecContext(ctx, updateSQL,
		v.ID, v.PlateNumber, v.Make, v.Model, v.Year, v.Color, v.FuelType,
		v.CategoryCode, v.TypeCode, v.DriverUserID, v.DriverName, v.DeviceModel,
		nullableString(v.Status), updatedBy, newIMEI)
	if err != nil {
		return fmt.Errorf("store: update vehicle: %w", err)
	}
	if n, _ := tag.RowsAffected(); n == 0 {
		return nil
	}
	if imeiChanged {
		// Move the allowlist entry: the old IMEI must stop resolving before the new
		// one starts, otherwise two vehicles could answer for the same device.
		if err := s.SoftDeleteIMEIMap(ctx, currentIMEI, company); err != nil {
			return err
		}
	}
	return s.SyncIMEIMap(ctx, v.IMEI, company, v.ID)
}

// SoftDeleteVehicle implements §6.0.1: rows are flagged, never removed; the
// master IMEI mapping is disabled so the device stops resolving (anti-spoof).
func (s *PostgresStore) SoftDeleteVehicle(ctx context.Context, company string, id, by int64, reason string) error {
	pool, err := s.tenantPool(company)
	if err != nil {
		return err
	}
	var imei string
	err = pool.DB.QueryRowContext(ctx, `
UPDATE tm_vehicles SET deleted_at = CURRENT_TIMESTAMP, deleted_by = $2,
delete_reason = $3, updated_at = CURRENT_TIMESTAMP
WHERE id = $1 AND deleted_at IS NULL
RETURNING imei`, id, by, reason).Scan(&imei)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("store: soft delete vehicle: %w", err)
	}
	return s.SoftDeleteIMEIMap(ctx, imei, company)
}

// RestoreVehicle reactivates a soft-deleted vehicle (+ IMEI map).
func (s *PostgresStore) RestoreVehicle(ctx context.Context, company string, id int64) error {
	pool, err := s.tenantPool(company)
	if err != nil {
		return err
	}
	var imei string
	err = pool.DB.QueryRowContext(ctx, `
UPDATE tm_vehicles SET deleted_at = NULL, deleted_by = NULL, delete_reason = NULL,
updated_at = CURRENT_TIMESTAMP
WHERE id = $1 AND deleted_at IS NOT NULL
RETURNING imei`, id).Scan(&imei)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("store: restore vehicle: %w", err)
	}
	return s.SyncIMEIMap(ctx, imei, company, id)
}

// SyncIMEIMap upserts the master anti-spoofing mapping (FR-1.4) — the ONLY
// authority that resolves an IMEI to a tenant + vehicle.
func (s *PostgresStore) SyncIMEIMap(ctx context.Context, imei, company string, vehicleID int64) error {
	_, err := s.tenants.Master().DB.ExecContext(ctx, `
INSERT INTO tm_vehicle_imei_map (imei, company_code, vehicle_id, is_active)
VALUES ($1, $2, $3, TRUE)
ON CONFLICT (imei) DO UPDATE
SET company_code = EXCLUDED.company_code,
vehicle_id = EXCLUDED.vehicle_id,
is_active = TRUE,
deleted_at = NULL,
updated_at = CURRENT_TIMESTAMP`, imei, company, vehicleID)
	if err != nil {
		return fmt.Errorf("store: sync imei map: %w", err)
	}
	return nil
}

// SoftDeleteIMEIMap disables the master mapping of a deleted vehicle.
func (s *PostgresStore) SoftDeleteIMEIMap(ctx context.Context, imei, company string) error {
	_, err := s.tenants.Master().DB.ExecContext(ctx, `
UPDATE tm_vehicle_imei_map SET is_active = FALSE, deleted_at = CURRENT_TIMESTAMP,
updated_at = CURRENT_TIMESTAMP
WHERE imei = $1 AND company_code = $2`, imei, company)
	if err != nil {
		return fmt.Errorf("store: soft delete imei map: %w", err)
	}
	return nil
}

// deref maps a nil *string to "".
func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// geofenceColumns is the projection shared by the geofence read paths.
const geofenceColumns = `g.id, g.name, g.description, g.area_type, g.center_lat,
g.center_lon, g.radius_meters, g.boundary_points, g.severity, g.on_entry,
g.on_exit, g.active, g.created_at, g.updated_at, g.deleted_at`

// scanGeofence scans one geofence row (boundary JSONB + nullable geometry).
func scanGeofence(row interface{ Scan(...any) error }) (models.Geofence, error) {
	var (
		g           models.Geofence
		description sql.NullString
		centerLat   sql.NullFloat64
		centerLon   sql.NullFloat64
		radius      sql.NullInt64
		boundary    []byte
		createdAt   time.Time
		updatedAt   time.Time
		deletedAt   sql.NullTime
	)
	err := row.Scan(&g.ID, &g.Name, &description, &g.AreaType, &centerLat, &centerLon,
		&radius, &boundary, &g.Severity, &g.OnEntry, &g.OnExit, &g.Active,
		&createdAt, &updatedAt, &deletedAt)
	if err != nil {
		return g, err
	}
	if description.Valid {
		s := description.String
		g.Description = &s
	}
	if centerLat.Valid {
		v := centerLat.Float64
		g.CenterLat = &v
	}
	if centerLon.Valid {
		v := centerLon.Float64
		g.CenterLon = &v
	}
	if radius.Valid {
		v := int(radius.Int64)
		g.RadiusM = &v
	}
	if len(boundary) > 0 {
		_ = json.Unmarshal(boundary, &g.Boundary)
	}
	g.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	g.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
	if deletedAt.Valid {
		s := deletedAt.Time.UTC().Format(time.RFC3339)
		g.DeletedAt = &s
	}
	return g, nil
}

// ListGeofences returns the geofence page + enabled vehicle mapping ids.
func (s *PostgresStore) ListGeofences(ctx context.Context, q GeofenceQuery) ([]models.Geofence, int64, error) {
	pool, err := s.tenantPool(q.CompanyCode)
	if err != nil {
		return nil, 0, err
	}
	where := ""
	if !q.IncludeDel {
		where = " WHERE g.deleted_at IS NULL"
	}
	var total int64
	if err := pool.DB.QueryRowContext(ctx,
		`SELECT count(*) FROM tm_geofences g`+where).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("store: count geofences: %w", err)
	}
	rows, err := pool.DB.QueryContext(ctx,
		`SELECT `+geofenceColumns+` FROM tm_geofences g`+where+
			" ORDER BY g.name, g.id LIMIT $1 OFFSET $2", q.Limit, (q.Page-1)*q.Limit)
	if err != nil {
		return nil, 0, fmt.Errorf("store: list geofences: %w", err)
	}
	defer rows.Close()
	out := make([]models.Geofence, 0, q.Limit)
	for rows.Next() {
		g, err := scanGeofence(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if err := s.attachGeofenceVehicles(ctx, pool, out); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// attachGeofenceVehicles fills the enabled vehicle mapping per zone.
func (s *PostgresStore) attachGeofenceVehicles(ctx context.Context, pool *internal.DBPool, zones []models.Geofence) error {
	for i := range zones {
		rows, err := pool.DB.QueryContext(ctx, `
SELECT vehicle_id FROM tm_geofence_vehicles
WHERE geofence_id = $1 AND enabled = TRUE AND deleted_at IS NULL`, zones[i].ID)
		if err != nil {
			return fmt.Errorf("store: geofence vehicles: %w", err)
		}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			zones[i].VehicleIDs = append(zones[i].VehicleIDs, id)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}
	return nil
}

// GeofenceByID loads one zone (nil when absent).
func (s *PostgresStore) GeofenceByID(ctx context.Context, company string, id int64, includeDeleted bool) (*models.Geofence, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return nil, err
	}
	query := "SELECT " + geofenceColumns + " FROM tm_geofences g WHERE g.id = $1"
	if !includeDeleted {
		query += " AND g.deleted_at IS NULL"
	}
	g, err := scanGeofence(pool.DB.QueryRowContext(ctx, query, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: geofence by id: %w", err)
	}
	zones := []models.Geofence{g}
	if err := s.attachGeofenceVehicles(ctx, pool, zones); err != nil {
		return nil, err
	}
	return &zones[0], nil
}

// GeofenceNameExists enforces the unique zone name within the tenant.
func (s *PostgresStore) GeofenceNameExists(ctx context.Context, company, name string, excludeID int64) (bool, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return false, err
	}
	var n int
	err = pool.DB.QueryRowContext(ctx, `
SELECT count(*) FROM tm_geofences
WHERE name = $1 AND id <> $2 AND deleted_at IS NULL`, name, excludeID).Scan(&n)
	return n > 0, err
}

// CreateGeofence inserts one zone + its vehicle mapping.
func (s *PostgresStore) CreateGeofence(ctx context.Context, company string, g *models.Geofence, createdBy int64) (int64, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return 0, err
	}
	boundary, err := json.Marshal(g.Boundary)
	if err != nil {
		return 0, fmt.Errorf("store: encode boundary: %w", err)
	}
	var id int64
	err = pool.DB.QueryRowContext(ctx, `
INSERT INTO tm_geofences (name, description, area_type, center_lat, center_lon,
radius_meters, boundary_points, severity, on_entry, on_exit, active, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8, $9, $10, COALESCE($11, TRUE), $12)
RETURNING id`,
		g.Name, g.Description, g.AreaType, g.CenterLat, g.CenterLon, g.RadiusM,
		string(boundary), nullableString(g.Severity), g.OnEntry, g.OnExit, nil, createdBy).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("store: create geofence: %w", err)
	}
	g.ID = id
	if err := s.ReplaceGeofenceVehicles(ctx, company, id, g.VehicleIDs, createdBy); err != nil {
		return 0, err
	}
	return id, nil
}

// UpdateGeofence updates the mutable columns of one zone (+ mapping replace).
func (s *PostgresStore) UpdateGeofence(ctx context.Context, company string, g *models.Geofence, updatedBy int64) error {
	pool, err := s.tenantPool(company)
	if err != nil {
		return err
	}
	boundary, err := json.Marshal(g.Boundary)
	if err != nil {
		return fmt.Errorf("store: encode boundary: %w", err)
	}
	_, err = pool.DB.ExecContext(ctx, `
UPDATE tm_geofences SET name = $2, description = $3, area_type = $4,
center_lat = $5, center_lon = $6, radius_meters = $7,
boundary_points = $8::jsonb, severity = $9, on_entry = $10, on_exit = $11,
active = $12, updated_by = $13, updated_at = CURRENT_TIMESTAMP
WHERE id = $1 AND deleted_at IS NULL`,
		g.ID, g.Name, g.Description, g.AreaType, g.CenterLat, g.CenterLon, g.RadiusM,
		string(boundary), nullableString(g.Severity), g.OnEntry, g.OnExit, g.Active, updatedBy)
	if err != nil {
		return fmt.Errorf("store: update geofence: %w", err)
	}
	return s.ReplaceGeofenceVehicles(ctx, company, g.ID, g.VehicleIDs, updatedBy)
}

// SoftDeleteGeofence flags a zone (the worker-alert cache drops it on refresh).
func (s *PostgresStore) SoftDeleteGeofence(ctx context.Context, company string, id, by int64, reason string) error {
	pool, err := s.tenantPool(company)
	if err != nil {
		return err
	}
	_, err = pool.DB.ExecContext(ctx, `
UPDATE tm_geofences SET deleted_at = CURRENT_TIMESTAMP, deleted_by = $2,
delete_reason = $3, updated_at = CURRENT_TIMESTAMP
WHERE id = $1 AND deleted_at IS NULL`, id, by, reason)
	return err
}

// RestoreGeofence reactivates a soft-deleted zone.
func (s *PostgresStore) RestoreGeofence(ctx context.Context, company string, id int64) error {
	pool, err := s.tenantPool(company)
	if err != nil {
		return err
	}
	_, err = pool.DB.ExecContext(ctx, `
UPDATE tm_geofences SET deleted_at = NULL, deleted_by = NULL, delete_reason = NULL,
updated_at = CURRENT_TIMESTAMP
WHERE id = $1 AND deleted_at IS NOT NULL`, id)
	return err
}

// ReplaceGeofenceVehicles rewrites the zone↔vehicle mapping (soft-deleting the
// rows that disappear, restoring/re-adding the ones present).
func (s *PostgresStore) ReplaceGeofenceVehicles(ctx context.Context, company string, geofenceID int64, vehicleIDs []int64, by int64) error {
	pool, err := s.tenantPool(company)
	if err != nil {
		return err
	}
	if _, err := pool.DB.ExecContext(ctx, `
UPDATE tm_geofence_vehicles SET enabled = FALSE, deleted_at = CURRENT_TIMESTAMP,
deleted_by = $2, updated_at = CURRENT_TIMESTAMP
WHERE geofence_id = $1 AND deleted_at IS NULL`, geofenceID, by); err != nil {
		return fmt.Errorf("store: clear geofence vehicles: %w", err)
	}
	for _, vid := range vehicleIDs {
		if _, err := pool.DB.ExecContext(ctx, `
INSERT INTO tm_geofence_vehicles (geofence_id, vehicle_id, enabled, created_by)
VALUES ($1, $2, TRUE, $3)
ON CONFLICT (geofence_id, vehicle_id) DO UPDATE
SET enabled = TRUE, deleted_at = NULL, deleted_by = NULL,
updated_at = CURRENT_TIMESTAMP`, geofenceID, vid, by); err != nil {
			return fmt.Errorf("store: map geofence vehicle: %w", err)
		}
	}
	return nil
}

// routeColumns is the projection shared by the route read paths.
const routeColumns = `r.id, r.name, r.description, r.waypoints,
r.estimated_duration_min, r.created_at, r.updated_at, r.deleted_at`

// scanRoute scans one route row.
func scanRoute(row interface{ Scan(...any) error }) (models.Route, error) {
	var (
		r         models.Route
		desc      sql.NullString
		waypoints []byte
		est       sql.NullInt64
		createdAt time.Time
		updatedAt time.Time
		deletedAt sql.NullTime
	)
	err := row.Scan(&r.ID, &r.Name, &desc, &waypoints, &est, &createdAt, &updatedAt, &deletedAt)
	if err != nil {
		return r, err
	}
	if desc.Valid {
		s := desc.String
		r.Description = &s
	}
	if len(waypoints) > 0 {
		_ = json.Unmarshal(waypoints, &r.Waypoints)
	}
	if est.Valid {
		v := int(est.Int64)
		r.EstMinutes = &v
	}
	r.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	r.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
	if deletedAt.Valid {
		s := deletedAt.Time.UTC().Format(time.RFC3339)
		r.DeletedAt = &s
	}
	return r, nil
}

// ListRoutes returns the route page.
func (s *PostgresStore) ListRoutes(ctx context.Context, q RouteQuery) ([]models.Route, int64, error) {
	pool, err := s.tenantPool(q.CompanyCode)
	if err != nil {
		return nil, 0, err
	}
	where := ""
	if !q.IncludeDel {
		where = " WHERE r.deleted_at IS NULL"
	}
	var total int64
	if err := pool.DB.QueryRowContext(ctx,
		`SELECT count(*) FROM tm_routes r`+where).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("store: count routes: %w", err)
	}
	rows, err := pool.DB.QueryContext(ctx,
		`SELECT `+routeColumns+` FROM tm_routes r`+where+
			" ORDER BY r.name, r.id LIMIT $1 OFFSET $2", q.Limit, (q.Page-1)*q.Limit)
	if err != nil {
		return nil, 0, fmt.Errorf("store: list routes: %w", err)
	}
	defer rows.Close()
	out := make([]models.Route, 0, q.Limit)
	for rows.Next() {
		r, err := scanRoute(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// RouteByID loads one route (nil when absent).
func (s *PostgresStore) RouteByID(ctx context.Context, company string, id int64, includeDeleted bool) (*models.Route, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return nil, err
	}
	query := "SELECT " + routeColumns + " FROM tm_routes r WHERE r.id = $1"
	if !includeDeleted {
		query += " AND r.deleted_at IS NULL"
	}
	r, err := scanRoute(pool.DB.QueryRowContext(ctx, query, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: route by id: %w", err)
	}
	return &r, nil
}

// RouteNameExists enforces the unique route name within the tenant.
func (s *PostgresStore) RouteNameExists(ctx context.Context, company, name string, excludeID int64) (bool, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return false, err
	}
	var n int
	err = pool.DB.QueryRowContext(ctx, `
SELECT count(*) FROM tm_routes
WHERE name = $1 AND id <> $2 AND deleted_at IS NULL`, name, excludeID).Scan(&n)
	return n > 0, err
}

// CreateRoute inserts one route.
func (s *PostgresStore) CreateRoute(ctx context.Context, company string, r *models.Route, createdBy int64) (int64, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return 0, err
	}
	waypoints, err := json.Marshal(r.Waypoints)
	if err != nil {
		return 0, fmt.Errorf("store: encode waypoints: %w", err)
	}
	var id int64
	err = pool.DB.QueryRowContext(ctx, `
INSERT INTO tm_routes (name, description, waypoints, estimated_duration_min, created_by)
VALUES ($1, $2, $3::jsonb, $4, $5) RETURNING id`,
		r.Name, r.Description, string(waypoints), r.EstMinutes, createdBy).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("store: create route: %w", err)
	}
	r.ID = id
	return id, nil
}

// UpdateRoute updates the mutable columns of one route.
func (s *PostgresStore) UpdateRoute(ctx context.Context, company string, r *models.Route, updatedBy int64) error {
	pool, err := s.tenantPool(company)
	if err != nil {
		return err
	}
	waypoints, err := json.Marshal(r.Waypoints)
	if err != nil {
		return fmt.Errorf("store: encode waypoints: %w", err)
	}
	_, err = pool.DB.ExecContext(ctx, `
UPDATE tm_routes SET name = $2, description = $3, waypoints = $4::jsonb,
estimated_duration_min = $5, updated_by = $6, updated_at = CURRENT_TIMESTAMP
WHERE id = $1 AND deleted_at IS NULL`,
		r.ID, r.Name, r.Description, string(waypoints), r.EstMinutes, updatedBy)
	return err
}

// SoftDeleteRoute flags a route (its assignments stay audit-visible).
func (s *PostgresStore) SoftDeleteRoute(ctx context.Context, company string, id, by int64, reason string) error {
	pool, err := s.tenantPool(company)
	if err != nil {
		return err
	}
	_, err = pool.DB.ExecContext(ctx, `
UPDATE tm_routes SET deleted_at = CURRENT_TIMESTAMP, deleted_by = $2,
delete_reason = $3, updated_at = CURRENT_TIMESTAMP
WHERE id = $1 AND deleted_at IS NULL`, id, by, reason)
	return err
}

// RestoreRoute reactivates a soft-deleted route.
func (s *PostgresStore) RestoreRoute(ctx context.Context, company string, id int64) error {
	pool, err := s.tenantPool(company)
	if err != nil {
		return err
	}
	_, err = pool.DB.ExecContext(ctx, `
UPDATE tm_routes SET deleted_at = NULL, deleted_by = NULL, delete_reason = NULL,
updated_at = CURRENT_TIMESTAMP
WHERE id = $1 AND deleted_at IS NOT NULL`, id)
	return err
}

// assignmentColumns is the projection of th_route_assignments.
const assignmentColumns = `a.id, a.route_id, a.vehicle_id, a.driver_user_id,
a.status, a.deviation_meters, a.started_at, a.completed_at,
a.created_at, a.updated_at, a.deleted_at`

// scanAssignment scans one assignment row.
func scanAssignment(row interface{ Scan(...any) error }) (models.RouteAssignment, error) {
	var (
		a         models.RouteAssignment
		driver    sql.NullInt64
		startedAt sql.NullTime
		completed sql.NullTime
		createdAt time.Time
		updatedAt time.Time
		deletedAt sql.NullTime
	)
	err := row.Scan(&a.ID, &a.RouteID, &a.VehicleID, &driver, &a.Status, &a.DeviationM,
		&startedAt, &completed, &createdAt, &updatedAt, &deletedAt)
	if err != nil {
		return a, err
	}
	if driver.Valid {
		v := driver.Int64
		a.DriverUserID = &v
	}
	if startedAt.Valid {
		s := startedAt.Time.UTC().Format(time.RFC3339)
		a.StartedAt = &s
	}
	if completed.Valid {
		s := completed.Time.UTC().Format(time.RFC3339)
		a.CompletedAt = &s
	}
	a.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	a.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
	if deletedAt.Valid {
		s := deletedAt.Time.UTC().Format(time.RFC3339)
		a.DeletedAt = &s
	}
	return a, nil
}

// ListAssignments returns the assignments of one route.
func (s *PostgresStore) ListAssignments(ctx context.Context, company string, routeID int64) ([]models.RouteAssignment, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return nil, err
	}
	rows, err := pool.DB.QueryContext(ctx,
		"SELECT "+assignmentColumns+" FROM th_route_assignments a"+
			" WHERE a.route_id = $1 AND a.deleted_at IS NULL ORDER BY a.id", routeID)
	if err != nil {
		return nil, fmt.Errorf("store: list assignments: %w", err)
	}
	defer rows.Close()
	out := []models.RouteAssignment{}
	for rows.Next() {
		a, err := scanAssignment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// AssignmentByID loads one assignment scoped to its route (nil when absent).
func (s *PostgresStore) AssignmentByID(ctx context.Context, company string, routeID, id int64) (*models.RouteAssignment, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return nil, err
	}
	a, err := scanAssignment(pool.DB.QueryRowContext(ctx,
		"SELECT "+assignmentColumns+" FROM th_route_assignments a"+
			" WHERE a.route_id = $1 AND a.id = $2 AND a.deleted_at IS NULL", routeID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: assignment by id: %w", err)
	}
	return &a, nil
}

// CreateAssignment inserts one route assignment (initial status not_started).
func (s *PostgresStore) CreateAssignment(ctx context.Context, company string, a *models.RouteAssignment, by int64) (int64, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return 0, err
	}
	var id int64
	err = pool.DB.QueryRowContext(ctx, `
INSERT INTO th_route_assignments (route_id, vehicle_id, driver_user_id, status, assigned_by)
VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		a.RouteID, a.VehicleID, a.DriverUserID, nullableString(a.Status), by).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("store: create assignment: %w", err)
	}
	a.ID = id
	return id, nil
}

// UpdateAssignmentStatus applies a manual status transition (timestamps are
// stamped on the first in_progress and on completion, PRD §5.9.2).
func (s *PostgresStore) UpdateAssignmentStatus(ctx context.Context, company string, a *models.RouteAssignment) error {
	pool, err := s.tenantPool(company)
	if err != nil {
		return err
	}
	parseTS := func(raw *string) any {
		if raw == nil || *raw == "" {
			return nil
		}
		if t, perr := time.Parse(time.RFC3339, *raw); perr == nil {
			return t
		}
		return nil
	}
	started, completed := parseTS(a.StartedAt), parseTS(a.CompletedAt)
	_, err = pool.DB.ExecContext(ctx, `
UPDATE th_route_assignments SET status = $3, started_at = $4, completed_at = $5,
updated_at = CURRENT_TIMESTAMP
WHERE id = $1 AND route_id = $2 AND deleted_at IS NULL`,
		a.ID, a.RouteID, a.Status, started, completed)
	return err
}

// SoftDeleteAssignment flags an assignment row.
func (s *PostgresStore) SoftDeleteAssignment(ctx context.Context, company string, routeID, id, by int64, reason string) error {
	pool, err := s.tenantPool(company)
	if err != nil {
		return err
	}
	_, err = pool.DB.ExecContext(ctx, `
UPDATE th_route_assignments SET deleted_at = CURRENT_TIMESTAMP, deleted_by = $3,
delete_reason = $4, updated_at = CURRENT_TIMESTAMP
WHERE id = $2 AND route_id = $1 AND deleted_at IS NULL`, routeID, id, by, reason)
	return err
}

// speedConfigColumns is the projection of tm_speed_configs.
const speedConfigColumns = `id, vehicle_id, max_speed_kmh, grace_margin_percent,
alert_severity, enabled, created_at, updated_at, deleted_at`

// scanSpeedConfig scans one config row.
func scanSpeedConfig(row interface{ Scan(...any) error }) (models.SpeedConfig, error) {
	var (
		sc        models.SpeedConfig
		vehicleID sql.NullInt64
		severity  sql.NullString
		createdAt time.Time
		updatedAt time.Time
		deletedAt sql.NullTime
	)
	err := row.Scan(&sc.ID, &vehicleID, &sc.MaxSpeedKMH, &sc.GracePct, &severity,
		&sc.Enabled, &createdAt, &updatedAt, &deletedAt)
	if err != nil {
		return sc, err
	}
	if vehicleID.Valid {
		v := vehicleID.Int64
		sc.VehicleID = &v
	}
	sc.Severity = severity.String
	sc.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	sc.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
	if deletedAt.Valid {
		s := deletedAt.Time.UTC().Format(time.RFC3339)
		sc.DeletedAt = &s
	}
	return sc, nil
}

// ListSpeedConfigs returns the speed configs (deleted rows only on request).
func (s *PostgresStore) ListSpeedConfigs(ctx context.Context, company string, includeDeleted bool) ([]models.SpeedConfig, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return nil, err
	}
	query := "SELECT " + speedConfigColumns + " FROM tm_speed_configs"
	if !includeDeleted {
		query += " WHERE deleted_at IS NULL"
	}
	query += " ORDER BY id"
	rows, err := pool.DB.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("store: list speed configs: %w", err)
	}
	defer rows.Close()
	out := []models.SpeedConfig{}
	for rows.Next() {
		sc, err := scanSpeedConfig(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

// SpeedConfigByID loads one config (nil when absent).
func (s *PostgresStore) SpeedConfigByID(ctx context.Context, company string, id int64, includeDeleted bool) (*models.SpeedConfig, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return nil, err
	}
	query := "SELECT " + speedConfigColumns + " FROM tm_speed_configs WHERE id = $1"
	if !includeDeleted {
		query += " AND deleted_at IS NULL"
	}
	sc, err := scanSpeedConfig(pool.DB.QueryRowContext(ctx, query, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: speed config by id: %w", err)
	}
	return &sc, nil
}

// CreateSpeedConfig inserts one config (global when vehicle_id is null).
func (s *PostgresStore) CreateSpeedConfig(ctx context.Context, company string, sc *models.SpeedConfig, createdBy int64) (int64, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return 0, err
	}
	var id int64
	err = pool.DB.QueryRowContext(ctx, `
INSERT INTO tm_speed_configs (vehicle_id, max_speed_kmh, grace_margin_percent,
alert_severity, enabled, created_by)
VALUES ($1, $2, $3, COALESCE($4, 'medium'), COALESCE($5, TRUE), $6)
RETURNING id`,
		sc.VehicleID, sc.MaxSpeedKMH, sc.GracePct, nullableString(sc.Severity),
		nil, createdBy).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("store: create speed config: %w", err)
	}
	sc.ID = id
	return id, nil
}

// UpdateSpeedConfig updates one config.
func (s *PostgresStore) UpdateSpeedConfig(ctx context.Context, company string, sc *models.SpeedConfig, updatedBy int64) error {
	pool, err := s.tenantPool(company)
	if err != nil {
		return err
	}
	_, err = pool.DB.ExecContext(ctx, `
UPDATE tm_speed_configs SET vehicle_id = $2, max_speed_kmh = $3,
grace_margin_percent = $4, alert_severity = $5, enabled = $6,
updated_by = $7, updated_at = CURRENT_TIMESTAMP
WHERE id = $1 AND deleted_at IS NULL`,
		sc.ID, sc.VehicleID, sc.MaxSpeedKMH, sc.GracePct, nullableString(sc.Severity),
		sc.Enabled, updatedBy)
	return err
}

// SoftDeleteSpeedConfig flags a config (worker-alert keeps the stale copy until
// the next refresh, then falls back to the remaining rows).
func (s *PostgresStore) SoftDeleteSpeedConfig(ctx context.Context, company string, id, by int64, reason string) error {
	pool, err := s.tenantPool(company)
	if err != nil {
		return err
	}
	_, err = pool.DB.ExecContext(ctx, `
UPDATE tm_speed_configs SET deleted_at = CURRENT_TIMESTAMP, deleted_by = $2,
delete_reason = $3, updated_at = CURRENT_TIMESTAMP
WHERE id = $1 AND deleted_at IS NULL`, id, by, reason)
	return err
}

// RestoreSpeedConfig reactivates a soft-deleted config.
func (s *PostgresStore) RestoreSpeedConfig(ctx context.Context, company string, id int64) error {
	pool, err := s.tenantPool(company)
	if err != nil {
		return err
	}
	_, err = pool.DB.ExecContext(ctx, `
UPDATE tm_speed_configs SET deleted_at = NULL, deleted_by = NULL, delete_reason = NULL,
updated_at = CURRENT_TIMESTAMP
WHERE id = $1 AND deleted_at IS NOT NULL`, id)
	return err
}

// alertColumns is the projection of th_alerts (read model).
const alertColumns = `id, type, severity, vehicle_id, imei, lat, lon, speed,
metadata, status, acknowledged_by, acknowledged_at, resolved_at,
sos_time_to_acknowledge_seconds, escalation_count, detected_at, created_at, updated_at`

// scanAlert scans one alert row.
func scanAlert(row interface{ Scan(...any) error }) (models.Alert, error) {
	var (
		a         models.Alert
		lat, lon  sql.NullFloat64
		speed     sql.NullFloat64
		meta      []byte
		ackedBy   sql.NullInt64
		ackedAt   sql.NullTime
		resolved  sql.NullTime
		tta       sql.NullInt64
		createdAt time.Time
		updatedAt time.Time
		detected  time.Time
	)
	err := row.Scan(&a.ID, &a.Type, &a.Severity, &a.VehicleID, &a.IMEI, &lat, &lon,
		&speed, &meta, &a.Status, &ackedBy, &ackedAt, &resolved, &tta,
		&a.Escalations, &detected, &createdAt, &updatedAt)
	if err != nil {
		return a, err
	}
	if lat.Valid {
		v := lat.Float64
		a.Lat = &v
	}
	if lon.Valid {
		v := lon.Float64
		a.Lon = &v
	}
	if speed.Valid {
		v := speed.Float64
		a.Speed = &v
	}
	if len(meta) > 0 {
		_ = json.Unmarshal(meta, &a.Metadata)
	}
	if ackedBy.Valid {
		v := ackedBy.Int64
		a.AckedBy = &v
	}
	if ackedAt.Valid {
		s := ackedAt.Time.UTC().Format(time.RFC3339)
		a.AckedAt = &s
	}
	if resolved.Valid {
		s := resolved.Time.UTC().Format(time.RFC3339)
		a.ResolvedAt = &s
	}
	if tta.Valid {
		v := int(tta.Int64)
		a.SOSTTA = &v
	}
	a.DetectedAt = detected.UTC().Format(time.RFC3339)
	a.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	a.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
	return a, nil
}

// ListAlerts returns the alert page with row-level filtering.
func (s *PostgresStore) ListAlerts(ctx context.Context, q AlertQuery) ([]models.Alert, int64, error) {
	// Read/write split (PRD §13): pure list read → replica first, primary on error.
	// Detail/`*ByID` lookups stay on the primary because the acknowledge/resolve
	// handlers reuse them.
	where := []string{}
	args := []any{}
	add := func(clause string, value any) {
		args = append(args, value)
		where = append(where, strings.Replace(clause, "?", "$"+itoa(len(args)), 1))
	}
	if !q.AllVehicles {
		if len(q.AssignedIDs) == 0 {
			return []models.Alert{}, 0, nil
		}
		parts := make([]string, 0, len(q.AssignedIDs))
		for _, id := range q.AssignedIDs {
			args = append(args, id)
			parts = append(parts, "$"+itoa(len(args)))
		}
		where = append(where, "vehicle_id IN ("+strings.Join(parts, ", ")+")")
	}
	if q.Type != "" {
		add("type = ?", q.Type)
	}
	if q.Severity != "" {
		add("severity = ?", q.Severity)
	}
	if q.Status != "" {
		add("status = ?", q.Status)
	}
	if !q.From.IsZero() {
		add("detected_at >= ?", q.From)
	}
	if !q.To.IsZero() {
		add("detected_at <= ?", q.To)
	}
	filter := ""
	if len(where) > 0 {
		filter = " WHERE " + strings.Join(where, " AND ")
	}
	var total int64
	if err := s.tenants.ReadQueryRow(ctx, q.CompanyCode, "SELECT count(*) FROM th_alerts"+filter, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("store: count alerts: %w", err)
	}
	argsPage := append(append([]any{}, args...), q.Limit, (q.Page-1)*q.Limit)
	rows, err := s.tenants.ReadQuery(ctx, q.CompanyCode,
		"SELECT "+alertColumns+" FROM th_alerts"+filter+
			" ORDER BY detected_at DESC, id DESC LIMIT $"+itoa(len(args)+1)+
			" OFFSET $"+itoa(len(args)+2), argsPage...)
	if err != nil {
		return nil, 0, fmt.Errorf("store: list alerts: %w", err)
	}
	defer rows.Close()
	out := make([]models.Alert, 0, q.Limit)
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, a)
	}
	return out, total, rows.Err()
}

// AcknowledgeAlert performs the open→acknowledged transition; it returns the
// number of rows changed (0 = already transitioned / not open), which makes the
// SOS TTA write-once guarantee (PRD §5.9.5) structurally impossible to violate.
func (s *PostgresStore) AcknowledgeAlert(ctx context.Context, company string, id, by int64) (int64, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return 0, err
	}
	tag, err := pool.DB.ExecContext(ctx, `
UPDATE th_alerts
SET status = 'acknowledged', acknowledged_by = $2, acknowledged_at = CURRENT_TIMESTAMP,
sos_time_to_acknowledge_seconds =
CASE WHEN type = 'sos' AND sos_time_to_acknowledge_seconds IS NULL
THEN GREATEST(0, EXTRACT(EPOCH FROM (CURRENT_TIMESTAMP - detected_at))::int)
END,
updated_at = CURRENT_TIMESTAMP
WHERE id = $1 AND status = 'open'`, id, by)
	if err != nil {
		return 0, fmt.Errorf("store: acknowledge alert: %w", err)
	}
	n, _ := tag.RowsAffected()
	return n, nil
}

// ResolveAlert performs the acknowledged|open→resolved transition.
func (s *PostgresStore) ResolveAlert(ctx context.Context, company string, id, by int64) (int64, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return 0, err
	}
	tag, err := pool.DB.ExecContext(ctx, `
UPDATE th_alerts SET status = 'resolved', resolved_at = CURRENT_TIMESTAMP,
resolved_by = $2, updated_at = CURRENT_TIMESTAMP
WHERE id = $1 AND status IN ('open', 'acknowledged')`, id, by)
	if err != nil {
		return 0, fmt.Errorf("store: resolve alert: %w", err)
	}
	n, _ := tag.RowsAffected()
	return n, nil
}

// AlertByID loads one alert (nil when absent).
func (s *PostgresStore) AlertByID(ctx context.Context, company string, id int64) (*models.Alert, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return nil, err
	}
	a, err := scanAlert(pool.DB.QueryRowContext(ctx,
		"SELECT "+alertColumns+" FROM th_alerts WHERE id = $1", id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: alert by id: %w", err)
	}
	return &a, nil
}
