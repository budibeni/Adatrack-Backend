package controllers

// store_pg_commands.go — B8 downlink command persistence (migration 021).
//
// The API owns the `pending` insert (the audit trail of the *request*); every
// later transition is written by ingestion-tcp from the device connection. Reads
// use the read/write split (PRD §13); writes always use the primary pool.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"adatrack_gps/api-vehicle/models"
)

// CreateDeviceCommand inserts the pending row and returns its id.
func (s *PostgresStore) CreateDeviceCommand(ctx context.Context, company string, cmd *models.DeviceCommand) (int64, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return 0, err
	}
	params := cmd.Parameters
	if params == nil {
		params = map[string]any{}
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return 0, fmt.Errorf("store: encode command parameters: %w", err)
	}
	var id int64
	err = pool.DB.QueryRowContext(ctx, `
INSERT INTO td_device_commands
	(request_id, company_code, vehicle_id, imei, command, parameters, status, created_by)
VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, NULLIF($8, 0))
ON CONFLICT (request_id) DO UPDATE SET updated_at = CURRENT_TIMESTAMP
RETURNING id`,
		cmd.RequestID, company, cmd.VehicleID, cmd.IMEI, cmd.Command, string(raw),
		models.CommandPending, cmd.CreatedBy).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("store: insert device command: %w", err)
	}
	return id, nil
}

// DeviceCommandByRequestID reads one command row back.
func (s *PostgresStore) DeviceCommandByRequestID(ctx context.Context, company, requestID string) (*models.DeviceCommand, error) {
	pool, err := s.tenantPool(company)
	if err != nil {
		return nil, err
	}
	row := pool.DB.QueryRowContext(ctx, commandSelect+`
 WHERE request_id = $1 AND deleted_at IS NULL`, requestID)
	cmd, err := scanDeviceCommand(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: device command by request id: %w", err)
	}
	return cmd, nil
}

// commandSelect is shared by the single-row and list queries.
const commandSelect = `
SELECT id, request_id, company_code, vehicle_id, imei, command, parameters,
       status, COALESCE(detail, ''), COALESCE(ack_content, ''),
       COALESCE(created_by, 0), created_at, sent_at, acked_at
FROM td_device_commands`

// scanDeviceCommand scans one row (shared by the single-row and list paths).
func scanDeviceCommand(row interface{ Scan(...any) error }) (*models.DeviceCommand, error) {
	var (
		cmd    models.DeviceCommand
		raw    []byte
		sentAt sql.NullTime
		ackAt  sql.NullTime
	)
	if err := row.Scan(&cmd.ID, &cmd.RequestID, &cmd.CompanyCode, &cmd.VehicleID, &cmd.IMEI,
		&cmd.Command, &raw, &cmd.Status, &cmd.Detail, &cmd.ACKContent, &cmd.CreatedBy,
		&cmd.CreatedAt, &sentAt, &ackAt); err != nil {
		return nil, err
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &cmd.Parameters)
	}
	if sentAt.Valid {
		cmd.SentAt = &sentAt.Time
	}
	if ackAt.Valid {
		cmd.AckedAt = &ackAt.Time
	}
	return &cmd, nil
}

// ListDeviceCommands returns the command history with row-level filtering.
func (s *PostgresStore) ListDeviceCommands(ctx context.Context, q CommandQuery) ([]models.DeviceCommand, int64, error) {
	where := []string{}
	args := []any{}
	if !q.AllVehicles {
		if len(q.AssignedIDs) == 0 {
			return []models.DeviceCommand{}, 0, nil
		}
		parts := make([]string, 0, len(q.AssignedIDs))
		for _, id := range q.AssignedIDs {
			args = append(args, id)
			parts = append(parts, "$"+itoa(len(args)))
		}
		where = append(where, "vehicle_id IN ("+strings.Join(parts, ", ")+")")
	}
	if q.VehicleID > 0 {
		args = append(args, q.VehicleID)
		where = append(where, "vehicle_id = $"+itoa(len(args)))
	}
	if q.Status != "" {
		args = append(args, q.Status)
		where = append(where, "status = $"+itoa(len(args)))
	}
	where = append(where, "deleted_at IS NULL")
	filter := " WHERE " + strings.Join(where, " AND ")

	var total int64
	if err := s.tenants.ReadQueryRow(ctx, q.CompanyCode,
		"SELECT count(*) FROM td_device_commands"+filter, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("store: count device commands: %w", err)
	}

	argsPage := append(append([]any{}, args...), q.Limit, (q.Page-1)*q.Limit)
	rows, err := s.tenants.ReadQuery(ctx, q.CompanyCode,
		commandSelect+filter+" ORDER BY created_at DESC, id DESC LIMIT $"+itoa(len(args)+1)+
			" OFFSET $"+itoa(len(args)+2), argsPage...)
	if err != nil {
		return nil, 0, fmt.Errorf("store: list device commands: %w", err)
	}
	defer rows.Close()

	out := []models.DeviceCommand{}
	for rows.Next() {
		cmd, serr := scanDeviceCommand(rows)
		if serr != nil {
			return nil, 0, serr
		}
		out = append(out, *cmd)
	}
	return out, total, rows.Err()
}
