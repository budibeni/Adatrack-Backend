package controllers

// commandstore.go — persistence of the downlink audit trail (B8, PRD §9.4).
//
// ingestion-tcp already resolves the tenant for every device, so it writes the
// `td_device_commands` state transitions directly into the company schema. Every
// statement is parameterized and company-scoped (PRD §6.2, §9.6).

import (
	"context"
	"encoding/json"
	"log/slog"

	"adatrack_gps/ingestion-tcp/models"
)

// persist upserts one command state transition. A failure is logged (never
// silently dropped) but does not abort device control: the frame may already be
// on the wire, so blocking the ACK path on a database outage would be worse.
func (g *commandGateway) persist(ctx context.Context, cmd models.DeviceCommand, res models.CommandResult) {
	if !g.persistent || g.tenants == nil {
		return
	}
	company := cmd.CompanyCode
	if company == "" {
		company = res.CompanyCode
	}
	pool, err := g.tenants.DB(company)
	if err != nil || pool == nil {
		slog.Warn("downlink: tenant pool unavailable for command audit",
			"company", company, "request_id", res.RequestID, "error", err)
		return
	}

	params := map[string]any{}
	if cmd.Kind == models.CommandSetInterval {
		params["interval_seconds"] = cmd.IntervalSeconds
	}
	if cmd.VehicleID > 0 {
		params["vehicle_id"] = cmd.VehicleID
	}
	raw, err := json.Marshal(params)
	if err != nil {
		raw = []byte("{}")
	}

	const q = `
		INSERT INTO td_device_commands
			(request_id, company_code, vehicle_id, imei, command, parameters,
			 status, detail, ack_content, created_by, sent_at, acked_at)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9, NULLIF($10, 0),
		        CASE WHEN $7 IN ('sent', 'acked', 'failed', 'timeout') THEN CURRENT_TIMESTAMP END,
		        CASE WHEN $7 IN ('acked', 'failed') AND $9 <> '' THEN CURRENT_TIMESTAMP END)
		ON CONFLICT (request_id) DO UPDATE SET
			status      = EXCLUDED.status,
			detail      = EXCLUDED.detail,
			ack_content = COALESCE(NULLIF(EXCLUDED.ack_content, ''), td_device_commands.ack_content),
			sent_at     = COALESCE(td_device_commands.sent_at, EXCLUDED.sent_at),
			acked_at    = COALESCE(td_device_commands.acked_at, EXCLUDED.acked_at),
			updated_at  = CURRENT_TIMESTAMP`

	if _, err := pool.DB.ExecContext(ctx, q,
		res.RequestID, company, cmd.VehicleID, cmd.IMEI, string(cmd.Kind), string(raw),
		res.Status, res.Detail, res.ACK, cmd.CreatedBy); err != nil {
		slog.Warn("downlink: command audit write failed",
			"company", company, "request_id", res.RequestID, "status", res.Status, "error", err)
	}
}
