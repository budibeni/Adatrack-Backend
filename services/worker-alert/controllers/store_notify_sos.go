package controllers

import (
	"database/sql"
	"time"

	"ajb_gps/internal/dialect"
	"ajb_gps/worker-alert/models"
)

// ---------------------------------------------------------------------------
// Notification preferences (migration 009) + audit trail (migration 010).
// ---------------------------------------------------------------------------

// EnabledPreferences lists enabled preferences matching any of the given alert
// types (lowercase, e.g. 'sos') or the 'all' wildcard.
func (s *companyStore) EnabledPreferences(alertTypes []string) ([]models.NotifPreference, error) {
	if len(alertTypes) == 0 {
		return nil, nil
	}
	// Dialect-aware default delivery_config (PG-parity fix): JSON_OBJECT()
	// menghasilkan tipe json di PG16 vs kolom jsonb → COALESCE error 42883;
	// MySQL tetap JSON_OBJECT().
	defExpr := deliveryDefaultExpr(dialect.Current())
	q := `SELECT user_id, alert_type, channel, COALESCE(min_severity,'warning'), COALESCE(delivery_config, ` + defExpr + `)
	      FROM notification_preferences WHERE is_enabled = TRUE AND alert_type IN (`
	q += "'" + alertTypes[0] + "'"
	for _, t := range alertTypes[1:] {
		// t berasal dari konstanta internal (bukan input user) — aman.
		q += ",'" + t + "'"
	}
	q += `,'all')`

	rows, err := s.ro.Query(q) // READ path (replica)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]models.NotifPreference, 0, 8)
	for rows.Next() {
		var p models.NotifPreference
		if err := rows.Scan(&p.UserID, &p.AlertType, &p.Channel, &p.MinSeverity, &p.DeliveryConf); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// VehicleUserIDs lists users having the vehicle assigned (user_vehicles, 003).
func (s *companyStore) VehicleUserIDs(vehicleID uint64) ([]uint64, error) {
	rows, err := s.ro.Query(`SELECT user_id FROM user_vehicles WHERE vehicle_id = ?`, vehicleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]uint64, 0, 4)
	for rows.Next() {
		var id uint64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// RecordNotification inserts a notifications audit row (migration 010).
func (s *companyStore) RecordNotification(userID, alertID uint64, channel, alertType, subject, body, status, errMsg string) error {
	var alertIDArg interface{}
	if alertID > 0 {
		alertIDArg = alertID
	}
	var errArg interface{}
	if errMsg != "" {
		errArg = errMsg
	}
	_, err := s.db.Exec(
		`INSERT INTO notifications (user_id, alert_id, company_code, channel, alert_type, subject, body, status, error_message)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		userID, alertIDArg, s.code, channel, alertType, nullIfEmpty(subject), nullIfEmpty(body), status, errArg,
	)
	return err
}

func nullIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// deliveryDefaultExpr returns the dialect-correct SQL expression for an empty
// delivery_config default inside COALESCE. MySQL: JSON_OBJECT(); PostgreSQL:
// '{}'-cast ke jsonb (JSON_OBJECT() PG16 menghasilkan json → COALESCE gagal
// "could not convert type json to jsonb", SQLSTATE 42883).
func deliveryDefaultExpr(d dialect.Dialect) string {
	if d == dialect.Postgres {
		return `'{}'::jsonb`
	}
	return `JSON_OBJECT()`
}

// ---------------------------------------------------------------------------
// SOS queries (alerts table, migration 008).
// ---------------------------------------------------------------------------

// OpenSOSOlderThan lists SOS alerts still open created at/before the cutoff.
func (s *companyStore) OpenSOSOlderThan(cutoff time.Time) ([]models.OpenSOSAlert, error) {
	rows, err := s.ro.Query( // READ path (replica); eskalasi periodik toleran lag ms
		`SELECT id, vehicle_id, created_at, acknowledged_at
		 FROM alerts
		 WHERE alert_type = 'SOS' AND status = 'open' AND created_at <= ?`,
		cutoff,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]models.OpenSOSAlert, 0, 4)
	for rows.Next() {
		var a models.OpenSOSAlert
		if err := rows.Scan(&a.ID, &a.VehicleID, &a.CreatedAt, &a.AcknowledgedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetAlert reads one alert row (used to observe TTA after acknowledgement).
func (s *companyStore) GetAlert(id uint64) (models.AlertRecord, error) {
	var (
		a    models.AlertRecord
		desc sql.NullString
		lat  sql.NullFloat64
		lon  sql.NullFloat64
	)
	err := s.ro.QueryRow( // READ path (replica)
		`SELECT id, vehicle_id, alert_type, severity, description, status, vehicle_lat, vehicle_lon, created_at
		 FROM alerts WHERE id = ?`, id,
	).Scan(&a.ID, &a.VehicleID, &a.AlertType, &a.Severity, &desc, &a.Status, &lat, &lon, &a.CreatedAt)
	if err != nil {
		return models.AlertRecord{}, err
	}
	if desc.Valid {
		a.Description = desc.String
	}
	if lat.Valid {
		v := lat.Float64
		a.VehicleLat = &v
	}
	if lon.Valid {
		v := lon.Float64
		a.VehicleLon = &v
	}
	return a, nil
}
