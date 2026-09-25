package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"adatrack_gps/api-vehicle/models"
	"adatrack_gps/internal"
)

// WriteAudit appends rows to the master `tm_audit_logs` (append-only, PRD §9.4).
// Sensitive keys are already redacted by the Auditor; before/after states are
// marshalled into JSONB.
func (s *PostgresStore) WriteAudit(ctx context.Context, rows []AuditRow) error {
	if len(rows) == 0 {
		return nil
	}
	columns := []string{
		"action", "outcome", "actor_user_id", "actor_email", "actor_role",
		"actor_ip", "actor_user_agent", "company_code", "entity_type", "entity_id",
		"before_state", "after_state", "reason", "request_id",
	}
	values := make([][]any, 0, len(rows))
	for _, row := range rows {
		outcome := row.Outcome
		if outcome == "" {
			outcome = OutcomeSuccess
		}
		values = append(values, []any{
			row.Action, outcome, nullableInt64(row.ActorUserID), nullableString(row.ActorEmail),
			nullableString(row.ActorRole), nullableString(row.ActorIP),
			nullableString(row.ActorUserAgent), nullableString(row.CompanyCode),
			nullableString(row.EntityType), nullableString(row.EntityID),
			jsonOrNil(row.BeforeState), jsonOrNil(row.AfterState),
			nullableString(row.Reason), nullableString(row.RequestID),
		})
	}
	if _, err := internal.BatchInsert(ctx, s.tenants.Master(), "tm_audit_logs", columns, values); err != nil {
		return fmt.Errorf("store: write audit: %w", err)
	}
	return nil
}

// ListAuditLogs reads the tenant slice of the append-only audit trail (PRD §9.4;
// read access is Admin-only at the router). Filters are parameterized and the
// tenant scope is mandatory — a tenant can never observe another tenant's trail.
func (s *PostgresStore) ListAuditLogs(ctx context.Context, q AuditLogQuery) ([]models.AuditLog, int64, error) {
	var (
		where = []string{"company_code = $1"}
		args  = []any{q.CompanyCode}
	)
	add := func(clause string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}
	if q.Action != "" {
		add("action = $%d", q.Action)
	}
	if q.Outcome != "" {
		add("outcome = $%d", q.Outcome)
	}
	if q.EntityType != "" {
		add("entity_type = $%d", q.EntityType)
	}
	if q.ActorUserID > 0 {
		add("actor_user_id = $%d", q.ActorUserID)
	}
	if q.From != nil {
		add("created_at >= $%d", *q.From)
	}
	if q.To != nil {
		add("created_at <= $%d", *q.To)
	}
	clause := " WHERE " + strings.Join(where, " AND ")

	var total int64
	if err := s.tenants.Master().DB.QueryRowContext(ctx,
		"SELECT count(*) FROM tm_audit_logs"+clause, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("store: count audit logs: %w", err)
	}

	args = append(args, q.Limit, (q.Page-1)*q.Limit)
	listSQL := fmt.Sprintf(`
SELECT audit_id, action, outcome, actor_user_id, actor_email, actor_role, actor_ip,
       company_code, entity_type, entity_id, before_state::text, after_state::text,
       reason, request_id,
       to_char(created_at, 'YYYY-MM-DD"T"HH24:MI:SSOF')
FROM tm_audit_logs%s
ORDER BY created_at DESC, audit_id DESC
LIMIT $%d OFFSET $%d`, clause, len(args)-1, len(args))

	rows, err := s.tenants.Master().DB.QueryContext(ctx, listSQL, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("store: list audit logs: %w", err)
	}
	defer rows.Close()

	var out []models.AuditLog
	for rows.Next() {
		var (
			item       models.AuditLog
			actorID    *int64
			beforeJSON *string
			afterJSON  *string
		)
		if err := rows.Scan(&item.AuditID, &item.Action, &item.Outcome, &actorID,
			&item.ActorEmail, &item.ActorRole, &item.ActorIP, &item.CompanyCode,
			&item.EntityType, &item.EntityID, &beforeJSON, &afterJSON,
			&item.Reason, &item.RequestID, &item.CreatedAt); err != nil {
			return nil, 0, fmt.Errorf("store: scan audit log: %w", err)
		}
		item.ActorID = actorID
		item.BeforeState = decodeAuditState(beforeJSON)
		item.AfterState = decodeAuditState(afterJSON)
		out = append(out, item)
	}
	return out, total, rows.Err()
}

// decodeAuditState unmarshals a JSONB audit snapshot (nil/empty stays nil).
func decodeAuditState(raw *string) any {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return nil
	}
	var decoded any
	if err := json.Unmarshal([]byte(*raw), &decoded); err != nil {
		return nil
	}
	return decoded
}

// jsonOrNil marshals an audit snapshot into JSONB (nil stays NULL).
func jsonOrNil(v any) any {
	if v == nil {
		return nil
	}
	body, err := json.Marshal(v)
	if err != nil || len(body) == 0 || string(body) == "null" {
		return nil
	}
	return string(body)
}

// nullableInt64 maps 0 to SQL NULL (an unauthenticated actor id).
func nullableInt64(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}
