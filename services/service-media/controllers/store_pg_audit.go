package controllers

import (
	"context"
	"encoding/json"
	"fmt"

	"adatrack_gps/internal"
)

// WriteAudit appends rows to the master `tm_audit_logs` (append-only, §9.4).
// before/after states are marshalled to JSONB; sensitive keys are already
// redacted by the Auditor.
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
		return fmt.Errorf("media: write audit: %w", err)
	}
	return nil
}

// jsonOrNil marshals an audit state into JSONB (nil stays NULL).
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
