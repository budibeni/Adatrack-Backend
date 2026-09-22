package controllers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"adatrack_gps/service-media/models"
)

// ListMediaEvents returns the filtered, paginated catalog (FR-8.4 + §3.1).
func (s *PostgresStore) ListMediaEvents(ctx context.Context, q MediaQuery) ([]models.MediaEvent, int64, error) {
	pool, err := s.tenantPool(q.CompanyCode)
	if err != nil {
		return nil, 0, err
	}

	where := []string{"1=1"}
	args := []any{}
	if !q.IncludeDel {
		where = append(where, "deleted_at IS NULL")
	}
	if q.VehicleID > 0 {
		args = append(args, q.VehicleID)
		where = append(where, fmt.Sprintf("vehicle_id = $%d", len(args)))
	}
	if q.EventType != "" {
		args = append(args, q.EventType)
		where = append(where, fmt.Sprintf("event_type = $%d", len(args)))
	}
	if q.Status != "" {
		args = append(args, q.Status)
		where = append(where, fmt.Sprintf("status = $%d", len(args)))
	}
	if q.IMEI != "" {
		args = append(args, q.IMEI)
		where = append(where, fmt.Sprintf("imei = $%d", len(args)))
	}
	if !q.From.IsZero() {
		args = append(args, q.From.UTC())
		where = append(where, fmt.Sprintf("captured_at >= $%d", len(args)))
	}
	if !q.To.IsZero() {
		args = append(args, q.To.UTC())
		where = append(where, fmt.Sprintf("captured_at <= $%d", len(args)))
	}
	// Row-level RBAC: only explicitly assigned vehicles for non-Admin/Manager
	// roles. An empty grant list must return ZERO rows, never everything.
	if !q.AllVehicles {
		if len(q.AssignedIDs) == 0 {
			return []models.MediaEvent{}, 0, nil
		}
		placeholders := make([]string, 0, len(q.AssignedIDs))
		for _, id := range q.AssignedIDs {
			args = append(args, id)
			placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
		}
		where = append(where, "vehicle_id IN ("+strings.Join(placeholders, ", ")+")")
	}
	clause := strings.Join(where, " AND ")

	var total int64
	if err := pool.DB.QueryRowContext(ctx,
		`SELECT count(*) FROM th_media_events WHERE `+clause, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("media: count catalog: %w", err)
	}

	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}
	offset := (maxInt(q.Page, 1) - 1) * limit
	args = append(args, limit, offset)
	rows, err := pool.DB.QueryContext(ctx,
		`SELECT `+mediaColumns+` FROM th_media_events WHERE `+clause+
			fmt.Sprintf(` ORDER BY captured_at DESC, id DESC LIMIT $%d OFFSET $%d`, len(args)-1, len(args)),
		args...)
	if err != nil {
		return nil, 0, fmt.Errorf("media: list catalog: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []models.MediaEvent{}
	for rows.Next() {
		m, serr := scanMediaEvent(rows)
		if serr != nil {
			return nil, 0, serr
		}
		out = append(out, *m)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// CompleteMediaEvent finalises the JSON flow: object metadata + lifecycle
// `complete` + expiry. The WHERE clause keeps it idempotent-safe and rejects
// expired/deleted rows (INVALID_STATUS_TRANSITION).
func (s *PostgresStore) CompleteMediaEvent(ctx context.Context, company string, m *models.MediaEvent) error {
	pool, err := s.tenantPool(company)
	if err != nil {
		return err
	}
	res, err := pool.DB.ExecContext(ctx, `
		UPDATE th_media_events
		   SET status = $2, file_size = $3, mime_type = $4, object_etag = $5,
		       retention_days = $6, completed_at = $7, expires_at = $8,
		       notified_at = $9, updated_at = CURRENT_TIMESTAMP
		 WHERE id = $1 AND status = $10 AND deleted_at IS NULL`,
		m.ID, models.StatusComplete, m.FileSize, m.MimeType, nullableString(m.ObjectETag),
		nullableInt(m.RetentionDays), m.CompletedAt, m.ExpiresAt, m.NotifiedAt, models.StatusPending)
	if err != nil {
		return fmt.Errorf("media: complete media event: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errConflict(CodeInvalidTransition,
			"media event is not in a state that can be completed")
	}
	return nil
}

// MarkMediaExpired stamps the retention outcome (status `expired`) for rows whose
// objects have been deleted (FR-8.7).
func (s *PostgresStore) MarkMediaExpired(ctx context.Context, company string, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	pool, err := s.tenantPool(company)
	if err != nil {
		return err
	}
	_, err = pool.DB.ExecContext(ctx, `
		UPDATE th_media_events
		   SET status = $1, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ANY($2)`, models.StatusExpired, ids)
	if err != nil {
		return fmt.Errorf("media: mark expired: %w", err)
	}
	return nil
}

// SoftDeleteMediaEvent implements FR-8.9: status `deleted` + actor + reason. The
// physical object stays until the retention sweep.
func (s *PostgresStore) SoftDeleteMediaEvent(ctx context.Context, company string, id, by int64, reason string) error {
	pool, err := s.tenantPool(company)
	if err != nil {
		return err
	}
	res, err := pool.DB.ExecContext(ctx, `
		UPDATE th_media_events
		   SET status = $2, deleted_at = CURRENT_TIMESTAMP, deleted_by = $3,
		       delete_reason = $4, updated_at = CURRENT_TIMESTAMP
		 WHERE id = $1 AND deleted_at IS NULL`,
		id, models.StatusDeleted, nullableInt64(by), nullableString(reason))
	if err != nil {
		return fmt.Errorf("media: soft delete %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errConflict(CodeConflict, "media event is already deleted or does not exist")
	}
	return nil
}

// RestoreMediaEvent reverses a soft delete (FR-8.9).
func (s *PostgresStore) RestoreMediaEvent(ctx context.Context, company string, id int64) error {
	pool, err := s.tenantPool(company)
	if err != nil {
		return err
	}
	res, err := pool.DB.ExecContext(ctx, `
		UPDATE th_media_events
		   SET status = CASE
		           WHEN expires_at IS NOT NULL AND expires_at <= CURRENT_TIMESTAMP THEN $2
		           ELSE $3 END,
		       deleted_at = NULL, deleted_by = NULL, delete_reason = NULL,
		       updated_at = CURRENT_TIMESTAMP
		 WHERE id = $1 AND deleted_at IS NOT NULL`,
		id, models.StatusExpired, models.StatusComplete)
	if err != nil {
		return fmt.Errorf("media: restore %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errConflict(CodeConflict, "media event is not deleted")
	}
	return nil
}

// ExpiredMediaEvents lists retention candidates (FR-8.7):
//   - `complete`/`deleted` rows whose expires_at has passed,
//   - legacy rows without expires_at whose captured_at is older than the horizon,
//   - `pending` uploads that never completed within the pending TTL.
func (s *PostgresStore) ExpiredMediaEvents(ctx context.Context, company string, now, pendingBefore time.Time, limit int) ([]models.MediaEvent, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 500
	}
	rows, err := pool.DB.QueryContext(ctx, `
		SELECT `+mediaColumns+`
		FROM th_media_events
		WHERE status <> $1
		  AND (
		    (expires_at IS NOT NULL AND expires_at <= $2)
		    OR (expires_at IS NULL AND status = $3 AND captured_at <= $2)
		    OR (status = $4 AND created_at <= $5)
		  )
		ORDER BY id
		LIMIT $6`,
		models.StatusExpired, now.UTC(), models.StatusComplete, models.StatusPending,
		pendingBefore.UTC(), limit)
	if err != nil {
		return nil, fmt.Errorf("media: list retention candidates: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []models.MediaEvent{}
	for rows.Next() {
		m, serr := scanMediaEvent(rows)
		if serr != nil {
			return nil, serr
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// CountStoredObjects is the FR-8.8 `storage_objects{bucket}` sample: catalog rows
// that still reference an object (complete + soft-deleted, not expired). It is
// DB-derived on purpose — a per-scrape object-storage listing would be far more
// expensive and no more accurate for the retention decision.
func (s *PostgresStore) CountStoredObjects(ctx context.Context, company string) (int64, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return 0, err
	}
	var n int64
	err = pool.DB.QueryRowContext(ctx, `
		SELECT count(*) FROM th_media_events
		WHERE status IN ($1, $2)`, models.StatusComplete, models.StatusDeleted).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("media: count stored objects: %w", err)
	}
	return n, nil
}

// nullableInt64 maps 0 to SQL NULL.
func nullableInt64(v int64) any {
	if v <= 0 {
		return nil
	}
	return v
}

// maxInt is a tiny helper for pagination bounds.
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
