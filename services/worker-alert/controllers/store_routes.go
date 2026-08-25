package controllers

import (
	"database/sql"
	"encoding/json"

	"ajb_gps/worker-alert/models"
)

// ---------------------------------------------------------------------------
// Routes + route_assignments (migrations 011/012).
// ---------------------------------------------------------------------------

// LoadActiveAssignments joins route_assignments+routes+vehicles for rows still
// being tracked (not_started / in_progress / delayed).
func (s *companyStore) LoadActiveAssignments() ([]models.RouteAssignment, error) {
	// READ path (replica): snapshot assignment di-refresh berkala.
	rows, err := s.ro.Query(
		`SELECT ra.id, ra.route_id, r.name, ra.vehicle_id, v.imei,
		        ra.driver_user_id, ra.status, ra.started_at, r.waypoints, r.estimated_duration_sec
		 FROM route_assignments ra
		 JOIN routes r  ON r.id = ra.route_id AND r.is_active = TRUE
		 JOIN vehicles v ON v.id = ra.vehicle_id AND v.deleted_at IS NULL
		 WHERE ra.status IN ('not_started','in_progress','delayed')`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]models.RouteAssignment, 0, 8)
	for rows.Next() {
		var (
			ra      models.RouteAssignment
			started sql.NullTime
			wps     []byte
			est     sql.NullInt64
		)
		if err := rows.Scan(&ra.AssignmentID, &ra.RouteID, &ra.RouteName, &ra.VehicleID,
			&ra.IMEI, &ra.DriverUserID, &ra.Status, &started, &wps, &est); err != nil {
			return nil, err
		}
		if started.Valid {
			t := started.Time
			ra.StartedAt = &t
		}
		if est.Valid {
			e := int(est.Int64)
			ra.EstimatedSec = &e
		}
		if len(wps) > 0 {
			_ = json.Unmarshal(wps, &ra.Waypoints)
		}
		out = append(out, ra)
	}
	return out, rows.Err()
}

// UpdateAssignmentStatus applies a lifecycle transition (migration 012).
func (s *companyStore) UpdateAssignmentStatus(id uint64, status string) error {
	switch status {
	case "in_progress": // started_at diisi sekali (COALESCE)
		_, err := s.db.Exec(
			`UPDATE route_assignments SET status='in_progress', started_at=COALESCE(started_at, NOW()) WHERE id=?`, id)
		return err
	case "completed": // completed_at diisi sekali
		_, err := s.db.Exec(
			`UPDATE route_assignments SET status='completed', completed_at=COALESCE(completed_at, NOW()) WHERE id=?`, id)
		return err
	default:
		_, err := s.db.Exec(`UPDATE route_assignments SET status=? WHERE id=?`, status, id)
		return err
	}
}

// UpdateAssignmentDeviation records the observed deviation distance (max).
func (s *companyStore) UpdateAssignmentDeviation(id uint64, meters float64) error {
	_, err := s.db.Exec(
		`UPDATE route_assignments SET deviation_meters = GREATEST(COALESCE(deviation_meters,0), ?) WHERE id=?`,
		meters, id,
	)
	return err
}
