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
	"time"

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
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9, NULLIF($10, 0), $11, $12)
		ON CONFLICT (request_id) DO UPDATE SET
			status      = EXCLUDED.status,
			detail      = EXCLUDED.detail,
			ack_content = COALESCE(NULLIF(EXCLUDED.ack_content, ''), td_device_commands.ack_content),
			sent_at     = COALESCE(td_device_commands.sent_at, EXCLUDED.sent_at),
			acked_at    = COALESCE(td_device_commands.acked_at, EXCLUDED.acked_at),
			updated_at  = CURRENT_TIMESTAMP`

	// The timestamps are computed here (not with a SQL CASE on $7) because
	// PostgreSQL deduces ONE type per parameter: using $7 both as the varchar
	// column value and inside `CASE WHEN $7 IN (...)` fails with
	// "inconsistent types deduced for parameter" (42P08) — found by the live run.
	sentAt, ackedAt := commandTransitionTimes(res)

	if _, err := pool.DB.ExecContext(ctx, q,
		res.RequestID, company, cmd.VehicleID, cmd.IMEI, string(cmd.Kind), string(raw),
		res.Status, res.Detail, res.ACK, cmd.CreatedBy, sentAt, ackedAt); err != nil {
		slog.Warn("downlink: command audit write failed",
			"company", company, "request_id", res.RequestID, "status", res.Status, "error", err)
	}
}

// commandTransitionTimes maps a status onto the `sent_at`/`acked_at` columns:
//   - `offline`   → never written to a device ⇒ both NULL,
//   - `sent`      → sent_at = now,
//   - `acked`     → sent_at + acked_at = now,
//   - `failed`    → sent_at only when the frame reached the device (an ACK string is
//     present for a device-reported failure, a detail-only failure was not written),
//   - `timeout`   → sent_at (the frame went out, the reply never came).
func commandTransitionTimes(res models.CommandResult) (sentAt, ackedAt *time.Time) {
	now := time.Now().UTC()
	switch res.Status {
	case models.CommandStatusSent, models.CommandStatusTimeout:
		sentAt = &now
	case models.CommandStatusAcked:
		sentAt, ackedAt = &now, &now
	case models.CommandStatusFailed:
		if res.ACK != "" {
			sentAt, ackedAt = &now, &now
		}
	}
	return sentAt, ackedAt
}
