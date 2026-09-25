package controllers

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"adatrack_gps/api-vehicle/models"
)

// industryModuleCodes are the docs/FRONTEND.md §1.7 industry modules: opt-in per
// tenant, gated by tm_company_modules (a licence), unlike the core modules.
var industryModuleCodes = map[string]bool{
	"rental": true, "transport": true, "logistics": true, "sales": true,
	"field-service": true, "patrol": true, "project-site": true,
}

// moduleDefaultEnabled is the licence default of a module code when the tenant has
// no explicit row: core modules are ON, industry modules OFF (opt-in licensing).
func moduleDefaultEnabled(code string) bool {
	return !industryModuleCodes[strings.ToLower(strings.TrimSpace(code))]
}

// moduleLicences loads the tenant's licence map (module_code → enabled).
func (s *PostgresStore) moduleLicences(ctx context.Context, company string) (map[string]bool, error) {
	out := map[string]bool{}
	rows, err := s.tenants.Master().DB.QueryContext(ctx, `
SELECT module_code, enabled FROM tm_company_modules WHERE company_code = $1`, company)
	if err != nil {
		return nil, fmt.Errorf("store: module licences: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var code string
		var enabled bool
		if err := rows.Scan(&code, &enabled); err != nil {
			return nil, err
		}
		out[strings.ToLower(strings.TrimSpace(code))] = enabled
	}
	return out, rows.Err()
}

// moduleLicensed resolves a module's effective licence state.
func moduleLicensed(licences map[string]bool, code string) bool {
	key := strings.ToLower(strings.TrimSpace(code))
	if enabled, ok := licences[key]; ok {
		return enabled
	}
	return moduleDefaultEnabled(key)
}

// MenuItemsForRole returns the navigation of one role: master menus filtered by
// the tenant licence registry AND the role→menu matrix (PRD §5.10 B12). A menu
// without an explicit access row is NOT visible (deny by default).
func (s *PostgresStore) MenuItemsForRole(ctx context.Context, company, role string) ([]models.MenuItem, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return nil, err
	}
	type accessRow struct{ view, create, edit, remove bool }
	access := map[int64]accessRow{}
	rows, err := pool.DB.QueryContext(ctx, `
SELECT menu_id, can_view, can_create, can_edit, can_delete
FROM tm_role_menu_access
WHERE role = $1 AND enabled AND deleted_at IS NULL`, role)
	if err != nil {
		return nil, fmt.Errorf("store: role menu access: %w", err)
	}
	for rows.Next() {
		var id int64
		var a accessRow
		if err := rows.Scan(&id, &a.view, &a.create, &a.edit, &a.remove); err != nil {
			rows.Close()
			return nil, err
		}
		access[id] = a
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	licences, err := s.moduleLicences(ctx, company)
	if err != nil {
		return nil, err
	}

	menuRows, err := s.tenants.Master().DB.QueryContext(ctx, `
SELECT m.id, mod.code, mod.name, mod.app, m.code, m.name, COALESCE(m.path, ''),
       m.parent_id, COALESCE(m.icon, ''), m.sort_order
FROM tm_menus m
JOIN tm_modules mod ON mod.id = m.module_id
WHERE m.enabled AND m.deleted_at IS NULL
ORDER BY mod.sort_order, m.sort_order, m.id`)
	if err != nil {
		return nil, fmt.Errorf("store: list menus: %w", err)
	}
	defer menuRows.Close()

	out := []models.MenuItem{}
	for menuRows.Next() {
		var item models.MenuItem
		var parentID *int64
		if err := menuRows.Scan(&item.MenuID, &item.ModuleCode, &item.ModuleName, &item.App,
			&item.Code, &item.Name, &item.Path, &parentID, &item.Icon, &item.SortOrder); err != nil {
			return nil, err
		}
		if !moduleLicensed(licences, item.ModuleCode) {
			continue
		}
		a, ok := access[item.MenuID]
		if !ok || !a.view {
			continue
		}
		item.ParentID = parentID
		item.CanView, item.CanCreate, item.CanEdit, item.CanDelete = a.view, a.create, a.edit, a.remove
		out = append(out, item)
	}
	return out, menuRows.Err()
}

// RoleMenuMatrix returns the full role→menu matrix (admin view), including menus
// the role cannot see yet, so the editor can toggle them on.
func (s *PostgresStore) RoleMenuMatrix(ctx context.Context, company, role string) ([]models.RoleMenuAccess, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return nil, err
	}
	rows, err := pool.DB.QueryContext(ctx, `
SELECT menu_id, can_view, can_create, can_edit, can_delete, enabled
FROM tm_role_menu_access WHERE role = $1 AND deleted_at IS NULL`, role)
	if err != nil {
		return nil, fmt.Errorf("store: role menu matrix: %w", err)
	}
	defer rows.Close()

	byMenu := map[int64]models.RoleMenuAccess{}
	for rows.Next() {
		var item models.RoleMenuAccess
		if err := rows.Scan(&item.MenuID, &item.CanView, &item.CanCreate, &item.CanEdit,
			&item.CanDelete, &item.Enabled); err != nil {
			return nil, err
		}
		byMenu[item.MenuID] = item
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	menuRows, err := s.tenants.Master().DB.QueryContext(ctx,
		`SELECT id, code, name FROM tm_menus WHERE deleted_at IS NULL ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("store: list menus for matrix: %w", err)
	}
	defer menuRows.Close()

	out := []models.RoleMenuAccess{}
	for menuRows.Next() {
		var item models.RoleMenuAccess
		if err := menuRows.Scan(&item.MenuID, &item.MenuCode, &item.MenuName); err != nil {
			return nil, err
		}
		if existing, ok := byMenu[item.MenuID]; ok {
			item.CanView, item.CanCreate = existing.CanView, existing.CanCreate
			item.CanEdit, item.CanDelete, item.Enabled = existing.CanEdit, existing.CanDelete, existing.Enabled
		}
		item.Enabled = item.Enabled || item.CanView
		out = append(out, item)
	}
	return out, menuRows.Err()
}

// ReplaceRoleMenuAccess rewrites the whole matrix of one role in a single
// transaction (an admin edit is atomic — no half-applied navigation).
func (s *PostgresStore) ReplaceRoleMenuAccess(ctx context.Context, company, role string, entries []models.MenuAccessEntry, by int64) (int64, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return 0, err
	}
	tx, err := pool.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("store: begin role menu tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM tm_role_menu_access WHERE role = $1`, role); err != nil {
		return 0, fmt.Errorf("store: clear role menu access: %w", err)
	}
	var written int64
	for _, entry := range entries {
		enabled := entry.CanView
		if entry.Enabled != nil {
			enabled = *entry.Enabled
		}
		tag, err := tx.ExecContext(ctx, `
INSERT INTO tm_role_menu_access (role, menu_id, can_view, can_create, can_edit, can_delete, enabled, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (role, menu_id) DO UPDATE SET
    can_view = EXCLUDED.can_view, can_create = EXCLUDED.can_create,
    can_edit = EXCLUDED.can_edit, can_delete = EXCLUDED.can_delete,
    enabled = EXCLUDED.enabled, updated_by = EXCLUDED.created_by,
    updated_at = CURRENT_TIMESTAMP, deleted_at = NULL`,
			role, entry.MenuID, entry.CanView, entry.CanCreate, entry.CanEdit, entry.CanDelete, enabled, by)
		if err != nil {
			return 0, fmt.Errorf("store: upsert role menu access: %w", err)
		}
		if n, _ := tag.RowsAffected(); n > 0 {
			written++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: commit role menu access: %w", err)
	}
	return written, nil
}

// ModuleLicenses lists every registered module with the tenant's licence state.
func (s *PostgresStore) ModuleLicenses(ctx context.Context, company string) ([]models.ModuleLicense, error) {
	licences, err := s.moduleLicences(ctx, company)
	if err != nil {
		return nil, err
	}
	rows, err := s.tenants.Master().DB.QueryContext(ctx, `
SELECT mod.code, mod.name, mod.app, cm.licensed_at, cm.expires_at
FROM tm_modules mod
LEFT JOIN tm_company_modules cm ON cm.module_code = mod.code AND cm.company_code = $1
ORDER BY mod.sort_order, mod.code`, company)
	if err != nil {
		return nil, fmt.Errorf("store: list modules: %w", err)
	}
	defer rows.Close()

	out := []models.ModuleLicense{}
	for rows.Next() {
		var (
			item      models.ModuleLicense
			licensed  *time.Time
			expiresAt *time.Time
		)
		if err := rows.Scan(&item.ModuleCode, &item.Name, &item.App, &licensed, &expiresAt); err != nil {
			return nil, err
		}
		item.Industry = industryModuleCodes[strings.ToLower(item.ModuleCode)]
		item.Enabled = moduleLicensed(licences, item.ModuleCode)
		if licensed != nil {
			formatted := licensed.UTC().Format(time.RFC3339)
			item.LicensedAt = &formatted
		}
		if expiresAt != nil {
			formatted := expiresAt.UTC().Format(time.RFC3339)
			item.ExpiresAt = &formatted
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// SetModuleLicense upserts one module licence of a tenant (master table).
func (s *PostgresStore) SetModuleLicense(ctx context.Context, company, code string, enabled bool, expiresAt *time.Time, by int64) error {
	code = strings.ToLower(strings.TrimSpace(code))
	_, err := s.tenants.Master().DB.ExecContext(ctx, `
INSERT INTO tm_company_modules (company_code, module_code, enabled, licensed_at, expires_at, created_by)
VALUES ($1, $2, $3, CASE WHEN $3 THEN CURRENT_TIMESTAMP ELSE NULL END, $4, $5)
ON CONFLICT (company_code, module_code) DO UPDATE SET
    enabled = EXCLUDED.enabled,
    licensed_at = COALESCE(tm_company_modules.licensed_at, EXCLUDED.licensed_at),
    expires_at = EXCLUDED.expires_at,
    updated_by = EXCLUDED.created_by,
    updated_at = CURRENT_TIMESTAMP`,
		company, code, enabled, expiresAt, by)
	if err != nil {
		return fmt.Errorf("store: set module license: %w", err)
	}
	return nil
}

// --- §1.8 Integrations (API key + webhook) ---------------------------------

// integrationColumns is the shared projection of tm_integrations.
const integrationColumns = `id, name, kind, endpoint_url, key_prefix, events, status,
last_used_at, notes, to_char(created_at, 'YYYY-MM-DD"T"HH24:MI:SSOF')`

// scanIntegration scans one tm_integrations row.
func scanIntegration(row interface{ Scan(...any) error }) (models.Integration, error) {
	var (
		item       models.Integration
		lastUsedAt *time.Time
	)
	err := row.Scan(&item.ID, &item.Name, &item.Kind, &item.EndpointURL, &item.KeyPrefix,
		&item.Events, &item.Status, &lastUsedAt, &item.Notes, &item.CreatedAt)
	if err != nil {
		return item, err
	}
	if lastUsedAt != nil {
		formatted := lastUsedAt.UTC().Format(time.RFC3339)
		item.LastUsedAt = &formatted
	}
	return item, nil
}

// ListIntegrations returns the tenant's integrations (newest first).
func (s *PostgresStore) ListIntegrations(ctx context.Context, company string) ([]models.Integration, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return nil, err
	}
	rows, err := pool.DB.QueryContext(ctx, "SELECT "+integrationColumns+
		" FROM tm_integrations WHERE company_code = $1 AND deleted_at IS NULL ORDER BY id DESC", company)
	if err != nil {
		return nil, fmt.Errorf("store: list integrations: %w", err)
	}
	defer rows.Close()

	out := []models.Integration{}
	for rows.Next() {
		item, err := scanIntegration(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// IntegrationByID loads one integration (nil when absent).
func (s *PostgresStore) IntegrationByID(ctx context.Context, company string, id int64) (*models.Integration, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return nil, err
	}
	rows, err := pool.DB.QueryContext(ctx, "SELECT "+integrationColumns+
		" FROM tm_integrations WHERE company_code = $1 AND id = $2 AND deleted_at IS NULL", company, id)
	if err != nil {
		return nil, fmt.Errorf("store: get integration: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	item, err := scanIntegration(rows)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// IntegrationNameExists enforces the per-tenant unique name.
func (s *PostgresStore) IntegrationNameExists(ctx context.Context, company, name string, excludeID int64) (bool, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return false, err
	}
	var n int
	err = pool.DB.QueryRowContext(ctx, `
SELECT count(*) FROM tm_integrations
WHERE company_code = $1 AND name = $2 AND id <> $3 AND deleted_at IS NULL`,
		company, name, excludeID).Scan(&n)
	return n > 0, err
}

// CreateIntegration inserts one integration (the secret hash is passed in the DTO).
func (s *PostgresStore) CreateIntegration(ctx context.Context, company string, in *models.Integration, secretHash string, by int64) (int64, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return 0, err
	}
	var id int64
	err = pool.DB.QueryRowContext(ctx, `
INSERT INTO tm_integrations (company_code, name, kind, endpoint_url, key_prefix, secret_hash, events, status, notes, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, COALESCE($8, 'active'), $9, $10)
RETURNING id`,
		company, in.Name, in.Kind, in.EndpointURL, in.KeyPrefix, secretHash,
		in.Events, in.Status, in.Notes, by).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("store: create integration: %w", err)
	}
	return id, nil
}

// UpdateIntegration patches the mutable columns of one integration.
func (s *PostgresStore) UpdateIntegration(ctx context.Context, company string, in *models.Integration, by int64) (int64, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return 0, err
	}
	tag, err := pool.DB.ExecContext(ctx, `
UPDATE tm_integrations SET name = $3, endpoint_url = $4, events = $5,
status = $6, notes = $7, updated_by = $8, updated_at = CURRENT_TIMESTAMP
WHERE company_code = $1 AND id = $2 AND deleted_at IS NULL`,
		company, in.ID, in.Name, in.EndpointURL, in.Events, in.Status, in.Notes, by)
	if err != nil {
		return 0, fmt.Errorf("store: update integration: %w", err)
	}
	n, _ := tag.RowsAffected()
	return n, nil
}

// SoftDeleteIntegration flags an integration as deleted (§6.0.1) and disables it
// immediately so a revoked webhook/API key stops being used.
func (s *PostgresStore) SoftDeleteIntegration(ctx context.Context, company string, id, by int64, reason string) (int64, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return 0, err
	}
	tag, err := pool.DB.ExecContext(ctx, `
UPDATE tm_integrations SET deleted_at = CURRENT_TIMESTAMP, deleted_by = $3,
delete_reason = $4, status = 'disabled', updated_by = $3, updated_at = CURRENT_TIMESTAMP
WHERE company_code = $1 AND id = $2 AND deleted_at IS NULL`, company, id, by, reason)
	if err != nil {
		return 0, fmt.Errorf("store: soft delete integration: %w", err)
	}
	n, _ := tag.RowsAffected()
	return n, nil
}

// hashSecret hashes an integration secret for storage (SHA-256 hex).
func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// --- §1.1 Public share links (FR-9.3) — MASTER table ------------------------

// shareColumns is the shared projection of tm_share_links (company_code included
// for the internal owner lookup; it is never serialised — see models.ShareLink).
const shareColumns = `company_code, id, token, label, scope, vehicle_ids, expires_at,
revoked_at, view_count, last_viewed_at, to_char(created_at, 'YYYY-MM-DD"T"HH24:MI:SSOF')`

// scanShareLink scans one tm_share_links row.
func scanShareLink(row interface{ Scan(...any) error }) (models.ShareLink, error) {
	var (
		link         models.ShareLink
		expiresAt    time.Time
		revokedAt    *time.Time
		lastViewedAt *time.Time
	)
	err := row.Scan(&link.CompanyCode, &link.ID, &link.Token, &link.Label, &link.Scope,
		&link.VehicleIDs, &expiresAt, &revokedAt, &link.ViewCount, &lastViewedAt, &link.CreatedAt)
	if err != nil {
		return link, err
	}
	link.ExpiresAt = expiresAt.UTC().Format(time.RFC3339)
	if revokedAt != nil {
		formatted := revokedAt.UTC().Format(time.RFC3339)
		link.RevokedAt = &formatted
	}
	if lastViewedAt != nil {
		formatted := lastViewedAt.UTC().Format(time.RFC3339)
		link.LastViewedAt = &formatted
	}
	return link, nil
}

// CreateShareLink inserts a TTL-bound public share link (master schema).
func (s *PostgresStore) CreateShareLink(ctx context.Context, company string, link *models.ShareLink, expiresAt time.Time, by int64) (int64, error) {
	var id int64
	err := s.tenants.Master().DB.QueryRowContext(ctx, `
INSERT INTO tm_share_links (company_code, token, label, scope, vehicle_ids, expires_at, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		company, link.Token, link.Label, link.Scope, link.VehicleIDs, expiresAt, by).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("store: create share link: %w", err)
	}
	return id, nil
}

// ListShareLinks returns the tenant's share links (newest first).
func (s *PostgresStore) ListShareLinks(ctx context.Context, company string) ([]models.ShareLink, error) {
	rows, err := s.tenants.Master().DB.QueryContext(ctx, "SELECT "+shareColumns+
		" FROM tm_share_links WHERE company_code = $1 AND deleted_at IS NULL ORDER BY id DESC", company)
	if err != nil {
		return nil, fmt.Errorf("store: list share links: %w", err)
	}
	defer rows.Close()

	out := []models.ShareLink{}
	for rows.Next() {
		link, err := scanShareLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, link)
	}
	return out, rows.Err()
}

// RevokeShareLink invalidates a public link immediately (revocation beats TTL).
func (s *PostgresStore) RevokeShareLink(ctx context.Context, company string, id, by int64, reason string) (int64, error) {
	tag, err := s.tenants.Master().DB.ExecContext(ctx, `
UPDATE tm_share_links SET revoked_at = CURRENT_TIMESTAMP, deleted_at = CURRENT_TIMESTAMP,
deleted_by = $3, delete_reason = $4, updated_by = $3, updated_at = CURRENT_TIMESTAMP
WHERE company_code = $1 AND id = $2 AND deleted_at IS NULL`, company, id, by, reason)
	if err != nil {
		return 0, fmt.Errorf("store: revoke share link: %w", err)
	}
	n, _ := tag.RowsAffected()
	return n, nil
}

// ResolveShareLink resolves a public token, atomically counting the view. A
// revoked or expired token resolves to nil (404 to the caller).
func (s *PostgresStore) ResolveShareLink(ctx context.Context, token string) (*models.ShareLink, error) {
	rows, err := s.tenants.Master().DB.QueryContext(ctx, `
UPDATE tm_share_links
SET view_count = view_count + 1, last_viewed_at = CURRENT_TIMESTAMP
WHERE token = $1 AND deleted_at IS NULL AND revoked_at IS NULL AND expires_at > CURRENT_TIMESTAMP
RETURNING `+shareColumns, token)
	if err != nil {
		return nil, fmt.Errorf("store: resolve share link: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	link, err := scanShareLink(rows)
	if err != nil {
		return nil, err
	}
	return &link, nil
}

// SharedVehicles returns the position payload of a share's vehicles.
func (s *PostgresStore) SharedVehicles(ctx context.Context, company string, ids []int64) ([]models.SharedVehicle, error) {
	if len(ids) == 0 {
		return []models.SharedVehicle{}, nil
	}
	pool, err := s.tenantPool(company)
	if err != nil {
		return nil, err
	}
	placeholders := make([]string, 0, len(ids)+1)
	args := []any{company}
	for _, id := range ids {
		args = append(args, id)
		placeholders = append(placeholders, "$"+itoa(len(args)))
	}
	query := fmt.Sprintf(`
SELECT id, plate_number, current_lat, current_lon, current_speed,
       to_char(last_seen_at, 'YYYY-MM-DD"T"HH24:MI:SSOF')
FROM tm_vehicles
WHERE company_code = $1 AND deleted_at IS NULL AND id IN (%s)`,
		strings.Join(placeholders, ", "))

	rows, err := pool.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: shared vehicles: %w", err)
	}
	defer rows.Close()

	out := []models.SharedVehicle{}
	for rows.Next() {
		var item models.SharedVehicle
		if err := rows.Scan(&item.VehicleID, &item.PlateNumber, &item.Lat, &item.Lon,
			&item.Speed, &item.LastSeenAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// --- §1.1 Heatmap (Pemantauan) ---------------------------------------------

// HeatmapCells returns the densest cached cells of a tenant (read path).
func (s *PostgresStore) HeatmapCells(ctx context.Context, company string, limit int) ([]models.HeatmapCell, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return nil, err
	}
	rows, err := pool.DB.QueryContext(ctx, `
SELECT cell_lat, cell_lon, sum(sample_count) AS samples
FROM tm_heatmap_cells
WHERE company_code = $1
GROUP BY cell_lat, cell_lon
ORDER BY samples DESC, cell_lat ASC
LIMIT $2`, company, limit)
	if err != nil {
		return nil, fmt.Errorf("store: heatmap cells: %w", err)
	}
	defer rows.Close()

	out := []models.HeatmapCell{}
	for rows.Next() {
		var cell models.HeatmapCell
		if err := rows.Scan(&cell.CellLat, &cell.CellLon, &cell.SampleCount); err != nil {
			return nil, err
		}
		out = append(out, cell)
	}
	return out, rows.Err()
}

// RebuildHeatmap recomputes the cached density cells from telemetry history
// (Admin-triggered; the dashboard only reads the cache).
func (s *PostgresStore) RebuildHeatmap(ctx context.Context, company string, from, to time.Time, cellSize float64) (int64, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return 0, err
	}
	if cellSize <= 0 {
		cellSize = 0.01
	}
	tx, err := pool.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("store: begin heatmap tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM tm_heatmap_cells WHERE company_code = $1`, company); err != nil {
		return 0, fmt.Errorf("store: clear heatmap: %w", err)
	}
	tag, err := tx.ExecContext(ctx, `
INSERT INTO tm_heatmap_cells (company_code, cell_lat, cell_lon, vehicle_id, sample_count, first_seen_at, last_seen_at)
SELECT company_code,
       (floor(latitude / $4::numeric) * $4::numeric)::numeric(10, 6),
       (floor(longitude / $4::numeric) * $4::numeric)::numeric(11, 6),
       vehicle_id, count(*), min("timestamp"), max("timestamp")
FROM th_telemetry_logs
WHERE company_code = $1 AND "timestamp" >= $2 AND "timestamp" <= $3
GROUP BY company_code, 2, 3, vehicle_id`,
		company, from, to, cellSize)
	if err != nil {
		return 0, fmt.Errorf("store: rebuild heatmap: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: commit heatmap: %w", err)
	}
	n, _ := tag.RowsAffected()
	return n, nil
}

// --- §1.6 Reports / Analytics ----------------------------------------------

// TripReport aggregates th_vehicle_trips over a window.
func (s *PostgresStore) TripReport(ctx context.Context, company string, from, to time.Time) (*models.TripReport, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return nil, err
	}
	report := &models.TripReport{From: from.UTC().Format(time.RFC3339), To: to.UTC().Format(time.RFC3339)}
	err = pool.DB.QueryRowContext(ctx, `
SELECT count(*), COALESCE(sum(distance_km), 0)::float8, COALESCE(avg(avg_speed_kmh), 0)::float8,
       COALESCE(max(max_speed_kmh), 0)::float8, COALESCE(sum(stop_count), 0)
FROM th_vehicle_trips
WHERE company_code = $1 AND start_time >= $2 AND start_time <= $3 AND deleted_at IS NULL`,
		company, from, to).Scan(&report.TripCount, &report.DistanceKm, &report.AvgSpeedKmh,
		&report.MaxSpeedKmh, &report.StopCount)
	if err != nil {
		return nil, fmt.Errorf("store: trip report: %w", err)
	}
	return report, nil
}

// ViolationReport aggregates B8 driver events per type/severity.
func (s *PostgresStore) ViolationReport(ctx context.Context, company string, from, to time.Time) ([]models.ViolationReportRow, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return nil, err
	}
	rows, err := pool.DB.QueryContext(ctx, `
SELECT event_type, severity, count(*)
FROM td_driver_events
WHERE company_code = $1 AND "timestamp" >= $2 AND "timestamp" <= $3
GROUP BY event_type, severity
ORDER BY count(*) DESC, event_type ASC`, company, from, to)
	if err != nil {
		return nil, fmt.Errorf("store: violation report: %w", err)
	}
	defer rows.Close()

	out := []models.ViolationReportRow{}
	for rows.Next() {
		var row models.ViolationReportRow
		if err := rows.Scan(&row.EventType, &row.Severity, &row.Count); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// SafetyScores reads the B8 driver scores of a window (Safety menu).
func (s *PostgresStore) SafetyScores(ctx context.Context, company string, from, to time.Time, limit int) ([]models.SafetyScore, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return nil, err
	}
	rows, err := pool.DB.QueryContext(ctx, `
SELECT vehicle_id, driver_id, to_char(period_start, 'YYYY-MM-DD'), to_char(period_end, 'YYYY-MM-DD'),
       score::float8, grade, harsh_acceleration_count, harsh_braking_count, harsh_cornering_count,
       speeding_count, speeding_seconds, to_char(computed_at, 'YYYY-MM-DD"T"HH24:MI:SSOF')
FROM th_driver_scores
WHERE company_code = $1 AND period_start >= $2::date AND period_start <= $3::date
ORDER BY period_start DESC, vehicle_id ASC
LIMIT $4`, company, from, to, limit)
	if err != nil {
		return nil, fmt.Errorf("store: safety scores: %w", err)
	}
	defer rows.Close()

	out := []models.SafetyScore{}
	for rows.Next() {
		var score models.SafetyScore
		if err := rows.Scan(&score.VehicleID, &score.DriverID, &score.PeriodStart, &score.PeriodEnd,
			&score.Score, &score.Grade, &score.HarshAcceleration, &score.HarshBraking,
			&score.HarshCornering, &score.SpeedingCount, &score.SpeedingSeconds,
			&score.ComputedAt); err != nil {
			return nil, err
		}
		out = append(out, score)
	}
	return out, rows.Err()
}

// --- §1.2 Group membership (vehicle/driver mapping) ------------------------

// ListGroupMembers returns the memberships of one group.
func (s *PostgresStore) ListGroupMembers(ctx context.Context, company string, groupID int64) ([]models.GroupMember, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return nil, err
	}
	rows, err := pool.DB.QueryContext(ctx, `
SELECT id, group_id, member_type, member_id, to_char(created_at, 'YYYY-MM-DD"T"HH24:MI:SSOF')
FROM tm_group_members
WHERE company_code = $1 AND group_id = $2 AND deleted_at IS NULL
ORDER BY member_type, member_id`, company, groupID)
	if err != nil {
		return nil, fmt.Errorf("store: list group members: %w", err)
	}
	defer rows.Close()

	out := []models.GroupMember{}
	for rows.Next() {
		var member models.GroupMember
		if err := rows.Scan(&member.ID, &member.GroupID, &member.MemberType,
			&member.MemberID, &member.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, member)
	}
	return out, rows.Err()
}

// AddGroupMember adds (or revives) one membership — idempotent by design.
func (s *PostgresStore) AddGroupMember(ctx context.Context, company string, groupID int64, memberType string, memberID, by int64) (int64, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return 0, err
	}
	var id int64
	err = pool.DB.QueryRowContext(ctx, `
INSERT INTO tm_group_members (company_code, group_id, member_type, member_id, created_by)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (group_id, member_type, member_id) WHERE deleted_at IS NULL
DO UPDATE SET deleted_at = NULL, updated_by = $5, updated_at = CURRENT_TIMESTAMP
RETURNING id`, company, groupID, memberType, memberID, by).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("store: add group member: %w", err)
	}
	return id, nil
}

// RemoveGroupMember soft-deletes one membership.
func (s *PostgresStore) RemoveGroupMember(ctx context.Context, company string, groupID, id, by int64) (int64, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return 0, err
	}
	tag, err := pool.DB.ExecContext(ctx, `
UPDATE tm_group_members SET deleted_at = CURRENT_TIMESTAMP, deleted_by = $4,
updated_by = $4, updated_at = CURRENT_TIMESTAMP
WHERE company_code = $1 AND group_id = $2 AND id = $3 AND deleted_at IS NULL`,
		company, groupID, id, by)
	if err != nil {
		return 0, fmt.Errorf("store: remove group member: %w", err)
	}
	n, _ := tag.RowsAffected()
	return n, nil
}

// ensure the sql/errors imports stay used if a future refactor drops a query.
var _ = sql.ErrNoRows
var _ = errors.Is
