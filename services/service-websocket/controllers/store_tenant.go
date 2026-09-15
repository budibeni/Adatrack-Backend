package controllers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// TenantAccess resolves the effective role inside one tenant from
// `tm_user_company_access` (PRD §3.1: role_override wins over the global role).
// `found=false` means the user has no row in this tenant — which the RBAC layer
// treats as "not a member" (403) for tenant routes.
func (s *PostgresStore) TenantAccess(ctx context.Context, companyCode string, userID int64) (string, bool, bool, error) {
	pool, err := s.tenantPool(companyCode)
	if err != nil {
		return "", false, false, err
	}
	var (
		roleOverride sql.NullString
		isActive     bool
	)
	err = pool.DB.QueryRowContext(ctx, `
		SELECT role_override, is_active
		FROM tm_user_company_access
		WHERE user_id = $1 AND deleted_at IS NULL`, userID).Scan(&roleOverride, &isActive)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, false, nil
		}
		return "", false, false, fmt.Errorf("store: tenant access: %w", err)
	}
	return strings.TrimSpace(roleOverride.String), isActive, true, nil
}

// UpsertTenantAccess writes (or refreshes) the per-tenant access row, used by
// both FR-5.5 auto-provisioning and FR-5.6 user onboarding.
func (s *PostgresStore) UpsertTenantAccess(ctx context.Context, companyCode string, userID int64, role string) error {
	pool, err := s.tenantPool(companyCode)
	if err != nil {
		return err
	}
	_, err = pool.DB.ExecContext(ctx, `
		INSERT INTO tm_user_company_access (user_id, role_override, is_active)
		VALUES ($1, $2, TRUE)
		ON CONFLICT (user_id) DO UPDATE SET
			role_override = EXCLUDED.role_override,
			is_active = TRUE,
			deleted_at = NULL,
			deleted_by = NULL,
			delete_reason = NULL,
			updated_at = CURRENT_TIMESTAMP`, userID, role)
	if err != nil {
		return fmt.Errorf("store: upsert tenant access: %w", err)
	}
	return nil
}

// AssignedVehicleIDs returns the row-level vehicle grants for a user
// (`tm_user_vehicles`, PRD §3.1/§9.2).
func (s *PostgresStore) AssignedVehicleIDs(ctx context.Context, companyCode string, userID int64) ([]int64, error) {
	pool, err := s.tenantPool(companyCode)
	if err != nil {
		return nil, err
	}
	rows, err := pool.DB.QueryContext(ctx, `
		SELECT vehicle_id
		FROM tm_user_vehicles
		WHERE user_id = $1 AND deleted_at IS NULL
		ORDER BY vehicle_id`, userID)
	if err != nil {
		return nil, fmt.Errorf("store: assigned vehicles: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("store: assigned vehicles scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: assigned vehicles rows: %w", err)
	}
	return ids, nil
}

// AssignVehicles grants a user access to the given vehicles idempotently.
func (s *PostgresStore) AssignVehicles(ctx context.Context, companyCode string, userID int64, vehicleIDs []int64) error {
	if len(vehicleIDs) == 0 {
		return nil
	}
	pool, err := s.tenantPool(companyCode)
	if err != nil {
		return err
	}
	tx, err := pool.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: assign vehicles begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, id := range vehicleIDs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO tm_user_vehicles (user_id, vehicle_id)
			VALUES ($1, $2)
			ON CONFLICT (user_id, vehicle_id) DO UPDATE SET
				deleted_at = NULL, deleted_by = NULL, delete_reason = NULL,
				updated_at = CURRENT_TIMESTAMP`, userID, id); err != nil {
			return fmt.Errorf("store: assign vehicle %d: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: assign vehicles commit: %w", err)
	}
	return nil
}

// ExistingVehicleIDs filters the requested ids down to those that really exist in
// the tenant (prevents assigning a foreign/IDOR vehicle id — PRD §9.6). Values are
// always bound parameters; only the placeholder count is generated.
func (s *PostgresStore) ExistingVehicleIDs(ctx context.Context, companyCode string, ids []int64) ([]int64, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	pool, err := s.tenantPool(companyCode)
	if err != nil {
		return nil, err
	}
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := pool.DB.QueryContext(ctx, `
		SELECT id FROM tm_vehicles
		WHERE id IN (`+placeholders(len(ids))+`) AND deleted_at IS NULL
		ORDER BY id`, args...)
	if err != nil {
		return nil, fmt.Errorf("store: existing vehicles: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("store: existing vehicles scan: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: existing vehicles rows: %w", err)
	}
	return out, nil
}

// placeholders builds "$1, $2, ... $n" for an IN (...) / ANY list.
func placeholders(n int) string {
	if n <= 0 {
		return "NULL"
	}
	var sb strings.Builder
	for i := 1; i <= n; i++ {
		if i > 1 {
			sb.WriteString(", ")
		}
		sb.WriteByte('$')
		sb.WriteString(itoa(int64(i)))
	}
	return sb.String()
}

// TenantExists verifies the tenant schema is reachable (used before writing
// access rows for a new user — FR-5.6).
func (s *PostgresStore) TenantExists(ctx context.Context, companyCode string) bool {
	return s.PingTenant(ctx, companyCode) == nil
}
