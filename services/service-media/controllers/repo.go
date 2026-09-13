package controllers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"ajb_gps/internal/dialect"
	"ajb_gps/internal/tenant"
	"ajb_gps/service-media/models"
)

// companyStore implements media_events access against ONE pre-warmed company
// pool (adatrack_gps_{company_code}), migration 015. `db` = PRIMARY (writes),
// `ro` = ReadRouter (replica-preferred reads) — B4 HA read/write split.
type companyStore struct {
	code string
	db   *sql.DB
	ro   *tenant.ReadRouter
}

func newCompanyStore(code string, db *sql.DB, tm *tenant.Manager) *companyStore {
	ro, rerr := tm.ReadRouter(code)
	if rerr != nil || ro == nil {
		ro = tenant.NewSingleRouter(db)
	}
	return &companyStore{code: code, db: db, ro: ro}
}

// VehicleByID returns (imei, status) for a live vehicle row.
func (s *companyStore) VehicleByID(vehicleID uint64) (string, string, error) {
	var imei, status string
	err := s.ro.QueryRow(
		`SELECT imei, COALESCE(status,'active') FROM vehicles WHERE id = ? AND deleted_at IS NULL LIMIT 1`,
		vehicleID,
	).Scan(&imei, &status)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", fmt.Errorf("vehicle %d not found", vehicleID)
		}
		return "", "", err
	}
	return imei, status, nil
}

// VehicleByIMEI returns (id, status) for a live vehicle row.
func (s *companyStore) VehicleByIMEI(imei string) (uint64, string, error) {
	var id uint64
	var status string
	err := s.ro.QueryRow(
		`SELECT id, COALESCE(status,'active') FROM vehicles WHERE imei = ? AND deleted_at IS NULL LIMIT 1`,
		imei,
	).Scan(&id, &status)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, "", fmt.Errorf("imei %s not found", imei)
		}
		return 0, "", err
	}
	return id, status, nil
}

// InsertMediaEvent creates a media_events row (dialect-aware: PG RETURNING id).
func (s *companyStore) InsertMediaEvent(ev models.MediaEvent) (uint64, error) {
	meta, _ := normalizeMeta(ev.Meta)
	id, err := dialect.InsertReturningID(dialect.Current(), context.Background(), s.db,
		`INSERT INTO media_events
		 (vehicle_id, imei, company_code, media_type, trigger_type, object_key, bucket,
		  size_bytes, duration_seconds, mime_type, sha256, status, taken_at, meta)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ev.VehicleID, ev.IMEI, s.code, ev.MediaType, ev.TriggerType, ev.ObjectKey, ev.Bucket,
		ev.SizeBytes, nullableInt(ev.DurationSec), ev.MimeType, nullIfEmpty(ev.SHA256),
		ev.Status, ev.TakenAt, meta,
	)
	if err != nil {
		return 0, err
	}
	return uint64(id), nil
}

// SetMediaStatus transitions a media_events row status.
func (s *companyStore) SetMediaStatus(id uint64, status string) error {
	_, err := s.db.Exec(`UPDATE media_events SET status = ? WHERE id = ?`, status, id)
	return err
}

// CompleteMediaEvent marks a previously 'uploaded' media row as 'available'
// with the object's real size after the device uploaded via presigned PUT
// (FR-8.3 lifecycle uploaded → available).
func (s *companyStore) CompleteMediaEvent(id uint64, size int64) error {
	res, err := s.db.Exec(
		`UPDATE media_events SET status = 'available', size_bytes = ?
		 WHERE id = ? AND status = 'uploaded'`,
		size, id,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		// Already available, or not in 'uploaded' state.
		return ErrMediaNotPending
	}
	return nil
}

// ErrMediaNotPending is returned by CompleteMediaEvent when the media row is
// not in the 'uploaded' state (e.g. already available / removed).
var ErrMediaNotPending = errors.New("media not in uploaded state")

// GetMediaEvent reads one media_events row (READ path).
func (s *companyStore) GetMediaEvent(id uint64) (*models.MediaEvent, error) {
	var (
		ev   models.MediaEvent
		dur  sql.NullInt64
		sha  sql.NullString
		del  sql.NullTime
		meta []byte
	)
	err := s.ro.QueryRow(
		`SELECT id, vehicle_id, imei, company_code, media_type, trigger_type, object_key, bucket,
		        size_bytes, duration_seconds, mime_type, sha256, status, taken_at, meta, created_at, deleted_at
		 FROM media_events WHERE id = ?`, id,
	).Scan(&ev.ID, &ev.VehicleID, &ev.IMEI, &ev.CompanyCode, &ev.MediaType, &ev.TriggerType,
		&ev.ObjectKey, &ev.Bucket, &ev.SizeBytes, &dur, &ev.MimeType, &sha, &ev.Status,
		&ev.TakenAt, &meta, &ev.CreatedAt, &del)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("media not found")
		}
		return nil, err
	}
	if dur.Valid {
		v := int(dur.Int64)
		ev.DurationSec = &v
	}
	if sha.Valid {
		ev.SHA256 = sha.String
	}
	ev.Meta = meta
	if del.Valid {
		ev.DeletedAt = &del.Time
	}
	return &ev, nil
}

// ListMediaEvents lists media_events for a company, filtered by an optional
// vehicle-scope (empty = Admin sees all) and optional trigger/status filters.
func (s *companyStore) ListMediaEvents(scope map[uint64]struct{}, triggerType, status string, page, limit int) ([]models.MediaEvent, int64, error) {
	var (
		conds []string
		args  []interface{}
	)
	conds = append(conds, "deleted_at IS NULL")
	if len(scope) > 0 {
		ids := sortedKeys(scope)
		ph := make([]string, 0, len(ids))
		for _, id := range ids {
			ph = append(ph, "?")
			args = append(args, id)
		}
		conds = append(conds, "vehicle_id IN ("+joinPlaceholders(ph)+")")
	}
	if triggerType != "" {
		conds = append(conds, "trigger_type = ?")
		args = append(args, triggerType)
	}
	if status != "" {
		conds = append(conds, "status = ?")
		args = append(args, status)
	}
	where := "WHERE " + joinConditions(conds)

	var total int64
	if err := s.ro.QueryRow(`SELECT COUNT(*) FROM media_events `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * limit
	rows, err := s.ro.Query(
		`SELECT id, vehicle_id, imei, media_type, trigger_type, object_key, bucket,
		        size_bytes, duration_seconds, mime_type, sha256, status, taken_at, meta
		 FROM media_events `+where+` ORDER BY taken_at DESC LIMIT ? OFFSET ?`,
		append(args, limit, offset)...,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := make([]models.MediaEvent, 0, 16)
	for rows.Next() {
		var (
			ev   models.MediaEvent
			dur  sql.NullInt64
			sha  sql.NullString
			meta []byte
		)
		if err := rows.Scan(&ev.ID, &ev.VehicleID, &ev.IMEI, &ev.MediaType, &ev.TriggerType,
			&ev.ObjectKey, &ev.Bucket, &ev.SizeBytes, &dur, &ev.MimeType, &sha, &ev.Status,
			&ev.TakenAt, &meta); err != nil {
			return nil, 0, err
		}
		if dur.Valid {
			v := int(dur.Int64)
			ev.DurationSec = &v
		}
		if sha.Valid {
			ev.SHA256 = sha.String
		}
		ev.Meta = meta
		ev.CompanyCode = s.code
		out = append(out, ev)
	}
	return out, total, rows.Err()
}

// SoftDeleteMediaEvent marks a media_events row deleted (FR-8.4 soft-delete).
func (s *companyStore) SoftDeleteMediaEvent(id uint64) error {
	_, err := s.db.Exec(`UPDATE media_events SET deleted_at = CURRENT_TIMESTAMP WHERE id = ? AND deleted_at IS NULL`, id)
	return err
}

// ExpiredMediaEntries lists available media older than the cutoff (used by the
// daily retention job, FR-8.7) in batches; objects are then deleted + status
// marked 'expired'.
func (s *companyStore) ExpiredMediaEntries(cutoff time.Time, limit int) ([]models.MediaEvent, error) {
	rows, err := s.db.Query(
		`SELECT id, vehicle_id, bucket, object_key, taken_at
		 FROM media_events
		 WHERE status = 'available' AND deleted_at IS NULL AND taken_at < ?
		 ORDER BY taken_at ASC LIMIT ?`, cutoff, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]models.MediaEvent, 0, 16)
	for rows.Next() {
		var ev models.MediaEvent
		if err := rows.Scan(&ev.ID, &ev.VehicleID, &ev.Bucket, &ev.ObjectKey, &ev.TakenAt); err != nil {
			return nil, err
		}
		ev.Status = "available"
		out = append(out, ev)
	}
	return out, rows.Err()
}
