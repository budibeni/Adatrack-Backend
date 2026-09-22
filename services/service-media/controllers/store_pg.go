package controllers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

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

// Master exposes the master pool (health checks + audit writes).
func (s *PostgresStore) Master() *internal.DBPool { return s.tenants.Master() }

// TenantHealth aggregates every tenant pool's readiness.
func (s *PostgresStore) TenantHealth(ctx context.Context) error { return s.tenants.Health(ctx) }

// tenantPool resolves a company code to its pool (normalised here — nothing
// user-controlled is interpolated into SQL, PRD §9.6).
func (s *PostgresStore) tenantPool(companyCode string) (*internal.DBPool, error) {
	return s.tenants.DB(strings.ToUpper(strings.TrimSpace(companyCode)))
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
		return nil, fmt.Errorf("media: load user %d: %w", id, err)
	}
	return &u, nil
}

// TenantAccess reads `tm_user_company_access` in the tenant schema.
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
		WHERE user_id = $1`, userID).Scan(&role, &active)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, false, nil
	}
	if err != nil {
		return "", false, false, fmt.Errorf("media: tenant access: %w", err)
	}
	return role, active, true, nil
}

// AssignedVehicleIDs reads the row-level grants (`tm_user_vehicles`).
func (s *PostgresStore) AssignedVehicleIDs(ctx context.Context, companyCode string, userID int64) ([]int64, error) {
	pool, err := s.tenantPool(companyCode)
	if err != nil {
		return nil, err
	}
	rows, err := pool.DB.QueryContext(ctx, `
		SELECT vehicle_id FROM tm_user_vehicles
		WHERE user_id = $1 AND COALESCE(is_active, TRUE)
		ORDER BY vehicle_id`, userID)
	if err != nil {
		return nil, fmt.Errorf("media: assigned vehicles: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// MediaCompanies lists every active tenant with its media configuration (used by
// the per-request config lookup and by the retention sweep, FR-8.7).
func (s *PostgresStore) MediaCompanies(ctx context.Context) ([]MediaConfig, error) {
	rows, err := s.tenants.Master().DB.QueryContext(ctx, `
		SELECT c.code,
		       COALESCE(mc.bucket, ''),
		       COALESCE(mc.retention_days, 0),
		       COALESCE(mc.max_file_mb, 0),
		       COALESCE(mc.hmac_secret, '')
		FROM tm_companies c
		LEFT JOIN tm_company_media_config mc
		       ON mc.company_code = c.code AND mc.deleted_at IS NULL
		WHERE c.deleted_at IS NULL AND COALESCE(c.is_active, TRUE)
		ORDER BY c.code`)
	if err != nil {
		return nil, fmt.Errorf("media: list media companies: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []MediaConfig
	for rows.Next() {
		var cfg MediaConfig
		if err := rows.Scan(&cfg.CompanyCode, &cfg.Bucket, &cfg.RetentionDays,
			&cfg.MaxFileMB, &cfg.HMACSecret); err != nil {
			return nil, err
		}
		cfg.CompanyCode = strings.ToUpper(strings.TrimSpace(cfg.CompanyCode))
		out = append(out, cfg)
	}
	return out, rows.Err()
}

// ResolveVehicleByIMEI maps a device IMEI onto its tenant + vehicle through the
// master allowlist (anti-spoofing, FR-1.4/FR-8.1).
func (s *PostgresStore) ResolveVehicleByIMEI(ctx context.Context, imei string) (*VehicleRef, error) {
	var ref VehicleRef
	err := s.tenants.Master().DB.QueryRowContext(ctx, `
		SELECT company_code, COALESCE(vehicle_id, 0), is_active
		FROM tm_vehicle_imei_map
		WHERE imei = $1 AND deleted_at IS NULL`, imei).
		Scan(&ref.CompanyCode, &ref.VehicleID, &ref.IsActive)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("media: resolve IMEI %s: %w", imei, err)
	}
	ref.CompanyCode = strings.ToUpper(strings.TrimSpace(ref.CompanyCode))
	return &ref, nil
}
