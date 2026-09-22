package controllers

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"adatrack_gps/service-websocket/models"
)

// VehicleHistory returns one page of `th_telemetry_logs` positions for a vehicle
// (FR-5.1 history playback, PRD §8.2). The partitions are pruned by the
// timestamp range, and the range itself is validated upstream (PRD §8.5).
func (s *PostgresStore) VehicleHistory(ctx context.Context, q HistoryQuery) ([]models.Position, int64, error) {
	// Read/write split (PRD §13): pure read -> replica first (when configured),
	// primary on error. service-websocket has no vehicle write endpoint, so every
	// read here may be served by the standby.

	var total int64
	if err := s.tenants.ReadQueryRow(ctx, q.CompanyCode, `
		SELECT count(*) FROM th_telemetry_logs
		WHERE vehicle_id = $1 AND "timestamp" >= $2 AND "timestamp" <= $3`,
		q.VehicleID, q.From.UTC(), q.To.UTC()).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("store: count history: %w", err)
	}

	rows, err := s.tenants.ReadQuery(ctx, q.CompanyCode, `
		SELECT to_char("timestamp" AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		       latitude::float8, longitude::float8, speed, heading, altitude,
		       acc_status, battery_level
		FROM th_telemetry_logs
		WHERE vehicle_id = $1 AND "timestamp" >= $2 AND "timestamp" <= $3
		ORDER BY "timestamp" DESC
		LIMIT $4 OFFSET $5`,
		q.VehicleID, q.From.UTC(), q.To.UTC(), q.Limit, (q.Page-1)*q.Limit)
	if err != nil {
		return nil, 0, fmt.Errorf("store: history: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]models.Position, 0, q.Limit)
	for rows.Next() {
		var (
			p        models.Position
			acc      sql.NullInt64
			altitude sql.NullFloat64
			heading  sql.NullFloat64
		)
		if err := rows.Scan(&p.Timestamp, &p.Lat, &p.Lon, &p.Speed, &heading, &altitude,
			&acc, &p.Battery); err != nil {
			return nil, 0, fmt.Errorf("store: scan history: %w", err)
		}
		if heading.Valid {
			p.Heading = int16(heading.Float64)
		}
		if altitude.Valid {
			p.Altitude = int16(altitude.Float64)
		}
		if acc.Valid {
			on := acc.Int64 != 0
			p.ACC = &on
			p.Fix = true
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("store: history rows: %w", err)
	}
	return out, total, nil
}

// positionTimestampLayout documents the wire format of Position.Timestamp.
const positionTimestampLayout = time.RFC3339
