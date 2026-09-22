package controllers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"adatrack_gps/service-media/models"
)

// mediaColumns is the canonical projection of `th_media_events` (migration
// 015 + additive 016). Kept in one place so every SELECT scans identically.
const mediaColumns = `id, vehicle_id, imei, event_type, object_key, file_size, mime_type,
	content_sha256, status, hmac_verified, COALESCE(upload_source, 'multipart'),
	COALESCE(object_etag, ''), COALESCE(retention_days, 0), created_by_user_id,
	captured_at, completed_at, expires_at, notified_at, deleted_at,
	COALESCE(delete_reason, ''), created_at, updated_at`

// scanMediaEvent reads one catalog row.
func scanMediaEvent(sc interface {
	Scan(dest ...any) error
}) (*models.MediaEvent, error) {
	var m models.MediaEvent
	err := sc.Scan(&m.ID, &m.VehicleID, &m.IMEI, &m.EventType, &m.ObjectKey, &m.FileSize,
		&m.MimeType, &m.ContentSHA256, &m.Status, &m.HMACVerified, &m.UploadSource,
		&m.ObjectETag, &m.RetentionDays, &m.CreatedByUserID, &m.CapturedAt,
		&m.CompletedAt, &m.ExpiresAt, &m.NotifiedAt, &m.DeletedAt, &m.DeleteReason,
		&m.CreatedAt, &m.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// VehicleByID loads one tenant vehicle (ingest validation).
func (s *PostgresStore) VehicleByID(ctx context.Context, company string, id int64, includeDeleted bool) (*Vehicle, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return nil, err
	}
	query := `SELECT id, COALESCE(plate_number, ''), COALESCE(imei, ''), deleted_at IS NOT NULL
	          FROM tm_vehicles WHERE id = $1`
	if !includeDeleted {
		query += ` AND deleted_at IS NULL`
	}
	var v Vehicle
	err = pool.DB.QueryRowContext(ctx, query, id).Scan(&v.ID, &v.PlateNumber, &v.IMEI, &v.Deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("media: load vehicle %d: %w", id, err)
	}
	return &v, nil
}

// CreateMediaEvent inserts one catalog row and returns its id (FR-8.3). The row
// is written as `pending` (JSON flow) or `complete` (multipart flow) depending
// on the fields the caller filled in.
func (s *PostgresStore) CreateMediaEvent(ctx context.Context, company string, m *models.MediaEvent) (int64, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return 0, err
	}
	var id int64
	err = pool.DB.QueryRowContext(ctx, `
		INSERT INTO th_media_events (vehicle_id, imei, event_type, object_key, file_size,
			mime_type, content_sha256, status, hmac_verified, captured_at,
			created_by_user_id, upload_source, object_etag, retention_days,
			completed_at, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		RETURNING id`,
		m.VehicleID, m.IMEI, m.EventType, m.ObjectKey, m.FileSize, m.MimeType,
		m.ContentSHA256, m.Status, m.HMACVerified, m.CapturedAt.UTC(),
		m.CreatedByUserID, m.UploadSource, nullableString(m.ObjectETag),
		nullableInt(m.RetentionDays), m.CompletedAt, m.ExpiresAt).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("media: create media event: %w", err)
	}
	return id, nil
}

// MediaEventByID loads one catalog row (soft-deleted visibility opt-in).
func (s *PostgresStore) MediaEventByID(ctx context.Context, company string, id int64, includeDeleted bool) (*models.MediaEvent, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return nil, err
	}
	query := `SELECT ` + mediaColumns + ` FROM th_media_events WHERE id = $1`
	if !includeDeleted {
		query += ` AND deleted_at IS NULL`
	}
	m, err := scanMediaEvent(pool.DB.QueryRowContext(ctx, query, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("media: load media event %d: %w", id, err)
	}
	return m, nil
}

// MediaEventByObjectKey loads one row by object key (JSON-flow completion).
func (s *PostgresStore) MediaEventByObjectKey(ctx context.Context, company, key string) (*models.MediaEvent, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return nil, err
	}
	m, err := scanMediaEvent(pool.DB.QueryRowContext(ctx,
		`SELECT `+mediaColumns+` FROM th_media_events WHERE object_key = $1`, key))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("media: load media event by key: %w", err)
	}
	return m, nil
}

// nullableInt maps 0 to SQL NULL (0 means "not snapshotted").
func nullableInt(v int) any {
	if v <= 0 {
		return nil
	}
	return v
}

// nullableString maps "" to SQL NULL.
func nullableString(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}
