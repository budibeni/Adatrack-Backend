package controllers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"adatrack_gps/internal"
	"adatrack_gps/internal/tenant"
	"adatrack_gps/worker-alert/models"
)

// PostgresStore implements Store on top of the shared tenant manager (master
// pool + one pre-warmed pool per company schema, PRD §6.2).
type PostgresStore struct {
	tenants *tenant.Manager
}

// NewPostgresStore wraps the tenant manager.
func NewPostgresStore(tm *tenant.Manager) *PostgresStore { return &PostgresStore{tenants: tm} }

// Readiness verifies the master pool answers (healthz).
func (s *PostgresStore) Readiness(ctx context.Context) error {
	return s.tenants.Master().Ping(ctx)
}

// tenantPool resolves a company code to its schema pool (code is normalised by
// tenant.Manager before any SQL runs — nothing user-controlled is interpolated).
func (s *PostgresStore) tenantPool(company string) (*internal.DBPool, error) {
	return s.tenants.DB(strings.ToUpper(strings.TrimSpace(company)))
}

// CompanyCodes lists active tenant codes (master tm_companies).
func (s *PostgresStore) CompanyCodes(ctx context.Context) ([]string, error) {
	rows, err := s.tenants.Master().DB.QueryContext(ctx,
		`SELECT code FROM tm_companies WHERE is_active = TRUE AND deleted_at IS NULL`)
	if err != nil {
		return nil, fmt.Errorf("store: company codes: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return nil, err
		}
		out = append(out, strings.ToUpper(strings.TrimSpace(code)))
	}
	return out, rows.Err()
}

// Users loads master tm_users rows for notification targets.
func (s *PostgresStore) Users(ctx context.Context, ids []int64) ([]models.Recipient, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := s.tenants.Master().DB.QueryContext(ctx, `
SELECT id, COALESCE(email, ''), COALESCE(full_name, '')
FROM tm_users
WHERE id = ANY($1) AND is_active = TRUE AND deleted_at IS NULL`, ids)
	if err != nil {
		return nil, fmt.Errorf("store: users: %w", err)
	}
	defer rows.Close()
	var out []models.Recipient
	for rows.Next() {
		var r models.Recipient
		if err := rows.Scan(&r.UserID, &r.Email, &r.FullName); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// TenantAdminUserIDs returns company Admin/Manager user ids (company schema
// tm_user_company_access — role_override is the effective tenant role).
func (s *PostgresStore) TenantAdminUserIDs(ctx context.Context, company string) ([]int64, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return nil, err
	}
	rows, err := pool.DB.QueryContext(ctx, `
SELECT user_id FROM tm_user_company_access
WHERE lower(role_override) IN ('admin', 'manager') AND is_active = TRUE
  AND deleted_at IS NULL`)
	if err != nil {
		return nil, fmt.Errorf("store: tenant admins: %w", err)
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

// VehicleGrants returns users with a row-level grant on the vehicle.
func (s *PostgresStore) VehicleGrants(ctx context.Context, company string, vehicleID int64) ([]int64, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return nil, err
	}
	rows, err := pool.DB.QueryContext(ctx, `
SELECT user_id FROM tm_user_vehicles
WHERE vehicle_id = $1 AND deleted_at IS NULL`, vehicleID)
	if err != nil {
		return nil, fmt.Errorf("store: vehicle grants: %w", err)
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

// Preferences loads tm_notification_preferences rows for the given users.
func (s *PostgresStore) Preferences(ctx context.Context, company string, userIDs []int64) ([]models.PrefRow, error) {
	if len(userIDs) == 0 {
		return nil, nil
	}
	pool, err := s.tenantPool(company)
	if err != nil {
		return nil, err
	}
	rows, err := pool.DB.QueryContext(ctx, `
SELECT user_id, alert_type, channel, enabled, min_severity
FROM tm_notification_preferences
WHERE user_id = ANY($1) AND deleted_at IS NULL`, userIDs)
	if err != nil {
		return nil, fmt.Errorf("store: preferences: %w", err)
	}
	defer rows.Close()
	var out []models.PrefRow
	for rows.Next() {
		var p models.PrefRow
		if err := rows.Scan(&p.UserID, &p.AlertType, &p.Channel, &p.Enabled, &p.MinSeverity); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// InsertAlert persists one alert; ON CONFLICT on the partial unique open-dedup
// index leaves the existing open alert untouched. Returns inserted=false when
// the dedup guard suppressed the row.
func (s *PostgresStore) InsertAlert(ctx context.Context, company string, a *models.Alert) (bool, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return false, err
	}
	meta, err := json.Marshal(a.Metadata)
	if err != nil {
		meta = []byte(`{}`)
	}
	var id int64
	err = pool.DB.QueryRowContext(ctx, `
INSERT INTO th_alerts
(type, severity, vehicle_id, imei, company_code, lat, lon, speed, metadata,
 status, dedup_key, detected_at)
VALUES ($1, $2, $3, $4, $5, NULLIF($6, 0), NULLIF($7, 0), NULLIF($8, 0),
        $9::jsonb, $10, $11, $12)
ON CONFLICT (dedup_key) WHERE status = 'open' DO NOTHING
RETURNING id`,
		a.Type, a.Severity, a.VehicleID, a.IMEI, a.CompanyCode, a.Lat, a.Lon, a.Speed,
		string(meta), a.Status, a.DedupKey, a.DetectedAt).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("store: insert alert: %w", err)
	}
	a.ID = id
	return true, nil
}

// UpdateRouteDeviation keeps the MAX deviation fresh on the OPEN alert
// (PRD §5.9.2: deviation_meters ter-update).
func (s *PostgresStore) UpdateRouteDeviation(ctx context.Context, company, dedupKey string, meters float64) error {
	pool, err := s.tenantPool(company)
	if err != nil {
		return err
	}
	_, err = pool.DB.ExecContext(ctx, `
UPDATE th_alerts
SET metadata = jsonb_set(metadata, '{deviation_meters}', to_jsonb($2::double precision), true),
    updated_at = CURRENT_TIMESTAMP
WHERE dedup_key = $1 AND status = 'open'
  AND COALESCE((metadata->>'deviation_meters')::double precision, 0) < $2`,
		dedupKey, meters)
	return err
}

// ResolveOpenAlerts resolves open alerts for one dedup identity (offline OK).
func (s *PostgresStore) ResolveOpenAlerts(ctx context.Context, company, dedupKey string) (int64, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return 0, err
	}
	tag, err := pool.DB.ExecContext(ctx, `
UPDATE th_alerts
SET status = 'resolved', resolved_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
WHERE dedup_key = $1 AND status = 'open'`, dedupKey)
	if err != nil {
		return 0, fmt.Errorf("store: resolve alerts: %w", err)
	}
	n, _ := tag.RowsAffected()
	return n, nil
}

// OpenSOAlerts lists open SOS alerts older than `olderThan` with an escalation
// counter below the cap.
func (s *PostgresStore) OpenSOAlerts(ctx context.Context, company string, olderThan time.Time, maxCount int) ([]models.Alert, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return nil, err
	}
	rows, err := pool.DB.QueryContext(ctx, `
SELECT id, type, severity, vehicle_id, imei, company_code,
       COALESCE(lat, 0), COALESCE(lon, 0), COALESCE(speed, 0), metadata,
       status, dedup_key, detected_at, escalation_count
FROM th_alerts
WHERE type = 'sos' AND status = 'open' AND detected_at < $1 AND escalation_count < $2
ORDER BY detected_at`, olderThan, maxCount)
	if err != nil {
		return nil, fmt.Errorf("store: open sos alerts: %w", err)
	}
	defer rows.Close()
	var out []models.Alert
	for rows.Next() {
		var a models.Alert
		var meta []byte
		if err := rows.Scan(&a.ID, &a.Type, &a.Severity, &a.VehicleID, &a.IMEI, &a.CompanyCode,
			&a.Lat, &a.Lon, &a.Speed, &meta, &a.Status, &a.DedupKey, &a.DetectedAt, &a.EscalationCount); err != nil {
			return nil, err
		}
		if len(meta) > 0 {
			_ = json.Unmarshal(meta, &a.Metadata)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// EscalateAlert bumps the escalation counter of one open alert.
func (s *PostgresStore) EscalateAlert(ctx context.Context, company string, id int64, count int) error {
	pool, err := s.tenantPool(company)
	if err != nil {
		return err
	}
	_, err = pool.DB.ExecContext(ctx, `
UPDATE th_alerts SET escalation_count = $2, updated_at = CURRENT_TIMESTAMP
WHERE id = $1 AND status = 'open'`, id, count)
	return err
}

// InsertNotifications appends td_notifications delivery audit rows.
func (s *PostgresStore) InsertNotifications(ctx context.Context, company string, rows []models.NotificationRow) error {
	if len(rows) == 0 {
		return nil
	}
	pool, err := s.tenantPool(company)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var resp any
		if r.ResponseJSON != nil {
			if body, jerr := json.Marshal(r.ResponseJSON); jerr == nil {
				resp = string(body)
			}
		}
		var reason any
		if r.Reason != "" {
			reason = r.Reason
		}
		if _, err := pool.DB.ExecContext(ctx, `
INSERT INTO td_notifications (alert_id, user_id, channel, status, provider_response, error_reason, sent_at)
VALUES ($1, $2, $3, $4::varchar, $5::jsonb, $6,
        CASE WHEN $4::varchar IN ('sent', 'delivered') THEN CURRENT_TIMESTAMP END)`,
			r.AlertID, r.UserID, r.Channel, r.Status, resp, reason); err != nil {
			return fmt.Errorf("store: insert notification: %w", err)
		}
	}
	return nil
}

// LoadGeofences loads active zones with their enabled vehicle mapping.
func (s *PostgresStore) LoadGeofences(ctx context.Context, company string) ([]models.Geofence, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return nil, err
	}
	rows, err := pool.DB.QueryContext(ctx, `
SELECT g.id, g.name, g.area_type, COALESCE(g.center_lat, 0), COALESCE(g.center_lon, 0),
       COALESCE(g.radius_meters, 0), COALESCE(g.boundary_points, '[]'::jsonb),
       g.severity, g.on_entry, g.on_exit,
       COALESCE(gv.vehicle_id, 0)
FROM tm_geofences g
LEFT JOIN tm_geofence_vehicles gv
       ON gv.geofence_id = g.id AND gv.enabled = TRUE AND gv.deleted_at IS NULL
WHERE g.active = TRUE AND g.deleted_at IS NULL`)
	if err != nil {
		return nil, fmt.Errorf("store: geofences: %w", err)
	}
	defer rows.Close()
	index := map[int64]*models.Geofence{}
	var order []int64
	for rows.Next() {
		var (
			id       int64
			vehicle  int64
			boundary []byte
			g        models.Geofence
		)
		if err := rows.Scan(&id, &g.Name, &g.AreaType, &g.CenterLat, &g.CenterLon,
			&g.RadiusM, &boundary, &g.Severity, &g.OnEntry, &g.OnExit, &vehicle); err != nil {
			return nil, err
		}
		existing, ok := index[id]
		if !ok {
			g.ID = id
			g.VehicleIDs = map[int64]bool{}
			if err := json.Unmarshal(boundary, &g.Boundary); err != nil {
				return nil, fmt.Errorf("store: geofence %d boundary: %w", id, err)
			}
			index[id] = &g
			order = append(order, id)
			existing = index[id]
		}
		if vehicle > 0 {
			existing.VehicleIDs[vehicle] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]models.Geofence, 0, len(order))
	for _, id := range order {
		out = append(out, *index[id])
	}
	return out, nil
}

// LoadSpeedConfigs loads enabled speed configs (vehicle-specific + global).
func (s *PostgresStore) LoadSpeedConfigs(ctx context.Context, company string) ([]models.SpeedConfig, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return nil, err
	}
	rows, err := pool.DB.QueryContext(ctx, `
SELECT id, COALESCE(vehicle_id, 0), max_speed_kmh, grace_margin_percent, alert_severity
FROM tm_speed_configs
WHERE enabled = TRUE AND deleted_at IS NULL`)
	if err != nil {
		return nil, fmt.Errorf("store: speed configs: %w", err)
	}
	defer rows.Close()
	var out []models.SpeedConfig
	for rows.Next() {
		var c models.SpeedConfig
		if err := rows.Scan(&c.ID, &c.VehicleID, &c.MaxSpeed, &c.GracePct, &c.Severity); err != nil {
			return nil, err
		}
		c.Enabled = true
		out = append(out, c)
	}
	return out, rows.Err()
}

// LoadAssignments loads in-progress route assignments with their waypoints.
func (s *PostgresStore) LoadAssignments(ctx context.Context, company string) ([]models.Assignment, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return nil, err
	}
	rows, err := pool.DB.QueryContext(ctx, `
SELECT a.id, a.vehicle_id, r.waypoints
FROM th_route_assignments a
JOIN tm_routes r ON r.id = a.route_id AND r.deleted_at IS NULL
WHERE a.status = 'in_progress' AND a.deleted_at IS NULL`)
	if err != nil {
		return nil, fmt.Errorf("store: assignments: %w", err)
	}
	defer rows.Close()
	var out []models.Assignment
	for rows.Next() {
		var a models.Assignment
		var waypoints []byte
		if err := rows.Scan(&a.ID, &a.VehicleID, &waypoints); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(waypoints, &a.Waypoints); err != nil {
			return nil, fmt.Errorf("store: assignment %d waypoints: %w", a.ID, err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// FuelConfigs loads per-vehicle + tenant-wide fuel configs (vehicle_id 0 =
// global) from tm_fuel_configs (migration 013).
func (s *PostgresStore) FuelConfigs(ctx context.Context, company string) ([]models.FuelConfig, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return nil, err
	}
	rows, err := pool.DB.QueryContext(ctx, `
SELECT id, COALESCE(vehicle_id, 0), drop_threshold_percent, refuel_threshold_percent,
       window_seconds, alert_severity, require_acc, acc_stale_seconds, enabled,
       created_at, updated_at, deleted_at
FROM tm_fuel_configs
WHERE deleted_at IS NULL ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("store: fuel configs: %w", err)
	}
	defer rows.Close()
	var out []models.FuelConfig
	for rows.Next() {
		var c models.FuelConfig
		var vehicleID sql.NullInt64
		var deletedAt sql.NullTime
		if err := rows.Scan(&c.ID, &vehicleID, &c.DropThresholdPct, &c.RefuelThresholdPct,
			&c.WindowSeconds, &c.Severity, &c.RequireACC, &c.ACCStaleSeconds, &c.Enabled,
			&c.CreatedAt, &c.UpdatedAt, &deletedAt); err != nil {
			return nil, err
		}
		if vehicleID.Valid {
			c.VehicleID = vehicleID.Int64
		}
		if deletedAt.Valid {
			t := deletedAt.Time
			c.DeletedAt = &t
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpsertFuelConfig inserts or updates the config row of one scope: the
// tenant-wide default when cfg.VehicleID is 0, otherwise the vehicle override.
// Migration 013 keeps TWO partial unique indexes (vehicle_id IS NULL / IS NOT
// NULL + deleted_at IS NULL), so the conflict target must quote the exact
// predicate of the matching index — a single generic clause never infers.
func (s *PostgresStore) UpsertFuelConfig(ctx context.Context, company string, cfg *models.FuelConfig, by int64) error {
	pool, err := s.tenantPool(company)
	if err != nil {
		return err
	}
	conflict := `WHERE vehicle_id IS NULL AND deleted_at IS NULL`
	if cfg.VehicleID != 0 {
		conflict = `WHERE vehicle_id IS NOT NULL AND deleted_at IS NULL`
	}
	_, err = pool.DB.ExecContext(ctx, `
INSERT INTO tm_fuel_configs (vehicle_id, drop_threshold_percent, refuel_threshold_percent,
                             window_seconds, alert_severity, require_acc, acc_stale_seconds,
                             enabled, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (vehicle_id) `+conflict+`
DO UPDATE SET drop_threshold_percent = EXCLUDED.drop_threshold_percent,
              refuel_threshold_percent = EXCLUDED.refuel_threshold_percent,
              window_seconds = EXCLUDED.window_seconds,
              alert_severity = EXCLUDED.alert_severity,
              require_acc = EXCLUDED.require_acc,
              acc_stale_seconds = EXCLUDED.acc_stale_seconds,
              enabled = EXCLUDED.enabled,
              updated_by = EXCLUDED.created_by,
              updated_at = CURRENT_TIMESTAMP`,
		vehicleIDArg(cfg.VehicleID), cfg.DropThresholdPct, cfg.RefuelThresholdPct, cfg.WindowSeconds,
		cfg.Severity, cfg.RequireACC, cfg.ACCStaleSeconds, cfg.Enabled, by)
	if err != nil {
		return fmt.Errorf("store: upsert fuel config: %w", err)
	}
	return nil
}

// vehicleIDArg maps 0 to SQL NULL (tenant-wide default row).
func vehicleIDArg(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

// ActiveVehicles lists active vehicles (id + IMEI) of one company.
func (s *PostgresStore) ActiveVehicles(ctx context.Context, company string) ([]VehicleRef, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return nil, err
	}
	rows, err := pool.DB.QueryContext(ctx, `
SELECT id, imei FROM tm_vehicles
WHERE status <> 'inactive' AND deleted_at IS NULL`)
	if err != nil {
		return nil, fmt.Errorf("store: active vehicles: %w", err)
	}
	defer rows.Close()
	var out []VehicleRef
	for rows.Next() {
		var v VehicleRef
		if err := rows.Scan(&v.ID, &v.IMEI); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
