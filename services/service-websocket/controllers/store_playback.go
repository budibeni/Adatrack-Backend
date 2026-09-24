package controllers

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"adatrack_gps/service-websocket/models"
)

// PlaybackQuery is the validated `GET /api/v1/vehicles/{id}/playback` request
// (B7.4). `Limit` is the service cap + 1, which lets the handler detect
// truncation without a second query.
type PlaybackQuery struct {
	CompanyCode string
	VehicleID   int64
	From        time.Time
	To          time.Time
	Limit       int
}

// PlaybackStore reads the raw playback route (B7.4). It is a separate interface
// so the core Store surface (and its fakes) stay untouched.
type PlaybackStore interface {
	VehiclePlayback(ctx context.Context, q PlaybackQuery) ([]models.Position, error)
}

// VehiclePlayback returns the unfiltered positions of a window in ASCENDING time
// order (playback replays the route forward, unlike the paged history endpoint).
// The read goes through the replica-first path (PRD §13) and relies on the
// (vehicle_id, timestamp DESC) index for the range scan.
//
// The projection matches `th_telemetry_logs` exactly (migration 007): only the
// columns the table really has, so playback and history can never drift apart.
func (s *PostgresStore) VehiclePlayback(ctx context.Context, q PlaybackQuery) ([]models.Position, error) {
	rows, err := s.tenants.ReadQuery(ctx, q.CompanyCode, `
		SELECT to_char("timestamp" AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		       latitude::float8, longitude::float8, speed, heading, altitude,
		       acc_status, battery_level
		FROM th_telemetry_logs
		WHERE vehicle_id = $1 AND "timestamp" >= $2 AND "timestamp" <= $3
		ORDER BY "timestamp" ASC
		LIMIT $4`,
		q.VehicleID, q.From.UTC(), q.To.UTC(), q.Limit)
	if err != nil {
		return nil, fmt.Errorf("store: playback: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]models.Position, 0, 512)
	for rows.Next() {
		var (
			p        models.Position
			heading  sql.NullFloat64
			altitude sql.NullFloat64
			acc      sql.NullInt64
			battery  sql.NullInt64
		)
		if err := rows.Scan(&p.Timestamp, &p.Lat, &p.Lon, &p.Speed, &heading, &altitude,
			&acc, &battery); err != nil {
			return nil, fmt.Errorf("store: scan playback: %w", err)
		}
		if heading.Valid {
			p.Heading = int16(heading.Float64)
		}
		if altitude.Valid {
			p.Altitude = int16(altitude.Float64)
		}
		if battery.Valid && battery.Int64 > 0 {
			p.Battery = uint8(battery.Int64)
		}
		if acc.Valid {
			// Tri-state (B6): NULL means the device never reported ACC, so the
			// playback point stays without an `acc` field.
			on := acc.Int64 != 0
			p.ACC = &on
			p.Fix = true
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: playback rows: %w", err)
	}
	return out, nil
}

// Compile-time guarantee that the production store satisfies PlaybackStore.
var _ PlaybackStore = (*PostgresStore)(nil)
