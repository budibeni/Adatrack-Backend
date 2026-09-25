package controllers

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"adatrack_gps/api-vehicle/models"
)

// EnterpriseQuery is the validated filter of a generic enterprise list.
type EnterpriseQuery struct {
	CompanyCode string
	Search      string
	IncludeDel  bool
	Page        int
	Limit       int
}

// EnterpriseStore is the table-driven CRUD surface of the B12 enterprise modules.
// Every statement is parameterized and every identifier comes from the
// compile-time resourceSpec whitelist (PRD §9.6).
type EnterpriseStore interface {
	ListEnterprise(ctx context.Context, spec resourceSpec, q EnterpriseQuery) ([]map[string]any, int64, error)
	GetEnterprise(ctx context.Context, company string, spec resourceSpec, id int64, includeDeleted bool) (map[string]any, error)
	CreateEnterprise(ctx context.Context, company string, spec resourceSpec, data map[string]any, by int64) (int64, error)
	UpdateEnterprise(ctx context.Context, company string, spec resourceSpec, id int64, data map[string]any, by int64) (int64, error)
	SoftDeleteEnterprise(ctx context.Context, company string, spec resourceSpec, id, by int64, reason string) (int64, error)
	RestoreEnterprise(ctx context.Context, company string, spec resourceSpec, id int64) (int64, error)

	// --- §1.x access & menu registry (§5.10, B12 tasks 1–2) ---
	MenuItemsForRole(ctx context.Context, company, role string) ([]models.MenuItem, error)
	RoleMenuMatrix(ctx context.Context, company, role string) ([]models.RoleMenuAccess, error)
	ReplaceRoleMenuAccess(ctx context.Context, company, role string, entries []models.MenuAccessEntry, by int64) (int64, error)

	// --- industry module licensing (B12 task 11) ---
	ModuleLicenses(ctx context.Context, company string) ([]models.ModuleLicense, error)
	SetModuleLicense(ctx context.Context, company, code string, enabled bool, expiresAt *time.Time, by int64) error

	// --- §1.8 integrations (API key + webhook) ---
	ListIntegrations(ctx context.Context, company string) ([]models.Integration, error)
	IntegrationByID(ctx context.Context, company string, id int64) (*models.Integration, error)
	IntegrationNameExists(ctx context.Context, company, name string, excludeID int64) (bool, error)
	CreateIntegration(ctx context.Context, company string, in *models.Integration, secretHash string, by int64) (int64, error)
	UpdateIntegration(ctx context.Context, company string, in *models.Integration, by int64) (int64, error)
	SoftDeleteIntegration(ctx context.Context, company string, id, by int64, reason string) (int64, error)

	// --- §1.1 public location share (FR-9.3) ---
	CreateShareLink(ctx context.Context, company string, link *models.ShareLink, expiresAt time.Time, by int64) (int64, error)
	ListShareLinks(ctx context.Context, company string) ([]models.ShareLink, error)
	RevokeShareLink(ctx context.Context, company string, id, by int64, reason string) (int64, error)
	ResolveShareLink(ctx context.Context, token string) (*models.ShareLink, error)
	SharedVehicles(ctx context.Context, company string, ids []int64) ([]models.SharedVehicle, error)

	// --- §1.1/§1.5/§1.6 heatmap, reports, safety ---
	HeatmapCells(ctx context.Context, company string, limit int) ([]models.HeatmapCell, error)
	RebuildHeatmap(ctx context.Context, company string, from, to time.Time, cellSize float64) (int64, error)
	TripReport(ctx context.Context, company string, from, to time.Time) (*models.TripReport, error)
	ViolationReport(ctx context.Context, company string, from, to time.Time) ([]models.ViolationReportRow, error)
	SafetyScores(ctx context.Context, company string, from, to time.Time, limit int) ([]models.SafetyScore, error)

	// --- §1.2 group membership ---
	ListGroupMembers(ctx context.Context, company string, groupID int64) ([]models.GroupMember, error)
	AddGroupMember(ctx context.Context, company string, groupID int64, memberType string, memberID, by int64) (int64, error)
	RemoveGroupMember(ctx context.Context, company string, groupID, id, by int64) (int64, error)
}

// enterpriseProjection is the SELECT list of a resource (soft-deletable tables
// also expose `deleted_at`; an immutable log has no such column).
func enterpriseProjection(spec resourceSpec) string {
	cols := spec.selectColumns()
	if !spec.Immutable {
		cols = append(cols, "deleted_at")
	}
	return strings.Join(cols, ", ")
}

// ListEnterprise returns one page of a resource plus the total count.
func (s *PostgresStore) ListEnterprise(ctx context.Context, spec resourceSpec, q EnterpriseQuery) ([]map[string]any, int64, error) {
	pool, err := s.tenantPool(q.CompanyCode)
	if err != nil {
		return nil, 0, err
	}
	where := []string{"company_code = $1"}
	args := []any{q.CompanyCode}
	if !spec.Immutable && !q.IncludeDel {
		where = append(where, "deleted_at IS NULL")
	}
	if term := strings.TrimSpace(q.Search); term != "" && len(spec.Search) > 0 {
		args = append(args, "%"+escapeLike(term)+"%")
		idx := itoa(len(args))
		parts := make([]string, 0, len(spec.Search))
		for _, col := range spec.Search {
			parts = append(parts, col+" ILIKE $"+idx)
		}
		where = append(where, "("+strings.Join(parts, " OR ")+")")
	}
	clause := " WHERE " + strings.Join(where, " AND ")

	var total int64
	if err := pool.DB.QueryRowContext(ctx, "SELECT count(*) FROM "+spec.Table+clause, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("store: count %s: %w", spec.Name, err)
	}

	order := spec.OrderBy
	if order == "" {
		order = "id DESC"
	}
	args = append(args, q.Limit, (q.Page-1)*q.Limit)
	listSQL := fmt.Sprintf("SELECT %s FROM %s%s ORDER BY %s LIMIT $%d OFFSET $%d",
		enterpriseProjection(spec), spec.Table, clause, order, len(args)-1, len(args))

	rows, err := pool.DB.QueryContext(ctx, listSQL, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("store: list %s: %w", spec.Name, err)
	}
	defer rows.Close()

	var out []map[string]any
	for rows.Next() {
		item, err := scanEnterpriseRow(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("store: scan %s: %w", spec.Name, err)
		}
		out = append(out, item)
	}
	return out, total, rows.Err()
}

// GetEnterprise loads one resource row (nil when absent).
func (s *PostgresStore) GetEnterprise(ctx context.Context, company string, spec resourceSpec, id int64, includeDeleted bool) (map[string]any, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return nil, err
	}
	where := []string{"company_code = $1", "id = $2"}
	args := []any{company, id}
	if !spec.Immutable && !includeDeleted {
		where = append(where, "deleted_at IS NULL")
	}
	query := fmt.Sprintf("SELECT %s FROM %s WHERE %s",
		enterpriseProjection(spec), spec.Table, strings.Join(where, " AND "))

	rows, err := pool.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: get %s: %w", spec.Name, err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	return scanEnterpriseRow(rows)
}

// CreateEnterprise inserts one resource row and returns its id.
func (s *PostgresStore) CreateEnterprise(ctx context.Context, company string, spec resourceSpec, data map[string]any, by int64) (int64, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return 0, err
	}
	columns := []string{"company_code", "created_by"}
	placeholders := []string{"$1", "$2"}
	values := []any{company, by}
	for _, field := range spec.Fields {
		value, ok := data[field.Column]
		if !ok {
			continue
		}
		values = append(values, value)
		columns = append(columns, field.Column)
		placeholders = append(placeholders, "$"+itoa(len(values)))
	}
	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) RETURNING id",
		spec.Table, strings.Join(columns, ", "), strings.Join(placeholders, ", "))

	var id int64
	if err := pool.DB.QueryRowContext(ctx, query, values...).Scan(&id); err != nil {
		return 0, fmt.Errorf("store: create %s: %w", spec.Name, err)
	}
	return id, nil
}

// UpdateEnterprise patches one resource row; the affected-row count is returned so
// the handler can answer 404 for an unknown/soft-deleted id.
func (s *PostgresStore) UpdateEnterprise(ctx context.Context, company string, spec resourceSpec, id int64, data map[string]any, by int64) (int64, error) {
	if spec.Immutable {
		return 0, nil
	}
	pool, err := s.tenantPool(company)
	if err != nil {
		return 0, err
	}
	sets := []string{}
	args := []any{company, id}
	for _, field := range spec.Fields {
		value, ok := data[field.Column]
		if !ok {
			continue
		}
		args = append(args, value)
		sets = append(sets, field.Column+" = $"+itoa(len(args)))
	}
	if len(sets) == 0 {
		return 0, nil
	}
	args = append(args, by)
	sets = append(sets, "updated_by = $"+itoa(len(args)), "updated_at = CURRENT_TIMESTAMP")

	query := fmt.Sprintf("UPDATE %s SET %s WHERE id = $2 AND company_code = $1 AND deleted_at IS NULL",
		spec.Table, strings.Join(sets, ", "))
	tag, err := pool.DB.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("store: update %s: %w", spec.Name, err)
	}
	n, _ := tag.RowsAffected()
	return n, nil
}

// SoftDeleteEnterprise flags a row as deleted (§6.0.1) — never a physical DELETE.
func (s *PostgresStore) SoftDeleteEnterprise(ctx context.Context, company string, spec resourceSpec, id, by int64, reason string) (int64, error) {
	if spec.Immutable {
		return 0, nil
	}
	pool, err := s.tenantPool(company)
	if err != nil {
		return 0, err
	}
	query := fmt.Sprintf(`UPDATE %s SET deleted_at = CURRENT_TIMESTAMP, deleted_by = $3,
delete_reason = $4, updated_by = $3, updated_at = CURRENT_TIMESTAMP
WHERE id = $2 AND company_code = $1 AND deleted_at IS NULL`, spec.Table)
	tag, err := pool.DB.ExecContext(ctx, query, company, id, by, reason)
	if err != nil {
		return 0, fmt.Errorf("store: soft delete %s: %w", spec.Name, err)
	}
	n, _ := tag.RowsAffected()
	return n, nil
}

// RestoreEnterprise clears the soft-delete veil (§6.0.1 restore endpoint).
func (s *PostgresStore) RestoreEnterprise(ctx context.Context, company string, spec resourceSpec, id int64) (int64, error) {
	if spec.Immutable {
		return 0, nil
	}
	pool, err := s.tenantPool(company)
	if err != nil {
		return 0, err
	}
	query := fmt.Sprintf(`UPDATE %s SET deleted_at = NULL, deleted_by = NULL, delete_reason = NULL,
updated_at = CURRENT_TIMESTAMP
WHERE id = $2 AND company_code = $1 AND deleted_at IS NOT NULL`, spec.Table)
	tag, err := pool.DB.ExecContext(ctx, query, company, id)
	if err != nil {
		return 0, fmt.Errorf("store: restore %s: %w", spec.Name, err)
	}
	n, _ := tag.RowsAffected()
	return n, nil
}

// scanEnterpriseRow scans a dynamic projection into a JSON-ready map.
func scanEnterpriseRow(rows *sql.Rows) (map[string]any, error) {
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	raw := make([]any, len(columns))
	ptrs := make([]any, len(columns))
	for i := range raw {
		ptrs[i] = &raw[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return nil, err
	}
	out := make(map[string]any, len(columns))
	for i, name := range columns {
		out[name] = normaliseSQLValue(raw[i])
	}
	return out, nil
}

// normaliseSQLValue makes a raw driver value JSON-marshalable.
func normaliseSQLValue(value any) any {
	switch typed := value.(type) {
	case nil:
		return nil
	case []byte:
		return string(typed)
	case time.Time:
		return typed.UTC().Format(time.RFC3339)
	default:
		return value
	}
}

// EnterpriseStore is implemented by PostgresStore.
var _ EnterpriseStore = (*PostgresStore)(nil)
