// Package models: B8 downlink command API contract (PRD §21.2 row 1).
//
// The REST shape mirrors the ingestion-side payload published on
// `command.request.<company>` (services/ingestion-tcp/models/command.go) so the
// API can hand the stored row to the dispatcher unchanged.
package models

import "time"

// Command kinds (closed whitelist — th/td_device_commands CHECK constraint).
const (
	CommandEngineCut     = "engine_cut"
	CommandEngineRestore = "engine_restore"
	CommandSetInterval   = "set_interval"
	CommandReboot        = "reboot"
	CommandLocate        = "locate"
)

// CommandStatuses tracks the delivery life-cycle of one command.
const (
	CommandPending = "pending"
	CommandSent    = "sent"
	CommandAcked   = "acked"
	CommandOffline = "offline"
	CommandFailed  = "failed"
	CommandTimeout = "timeout"
)

// DeviceCommand is one td_device_commands row (the downlink audit trail).
type DeviceCommand struct {
	ID          int64  `json:"id"`
	RequestID   string `json:"request_id"`
	CompanyCode string `json:"company_code"`
	VehicleID   int64  `json:"vehicle_id"`
	IMEI        string `json:"imei"`
	Command     string `json:"command"`
	// Parameters carries command-specific arguments (e.g. interval_seconds).
	Parameters map[string]any `json:"parameters,omitempty"`
	Status     string         `json:"status"`
	Detail     string         `json:"detail,omitempty"`
	ACKContent string         `json:"ack_content,omitempty"`
	CreatedBy  int64          `json:"created_by,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
	SentAt     *time.Time     `json:"sent_at,omitempty"`
	AckedAt    *time.Time     `json:"acked_at,omitempty"`
}

// IntervalSeconds returns the requested device reporting interval (0 when unset).
func (c DeviceCommand) IntervalSeconds() int {
	if v, ok := c.Parameters["interval_seconds"]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		}
	}
	return 0
}

// CommandRequest is the POST /api/v1/vehicles/{id}/commands body.
type CommandRequest struct {
	Command string `json:"command" binding:"required,max=32"`
	// IntervalSeconds is required for the set_interval command (5..86400).
	IntervalSeconds int `json:"interval_seconds" binding:"omitempty,min=0,max=86400"`
}
