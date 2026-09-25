// Package models: B8 downlink command contract (PRD §21.2 row 1).
package models

import (
	"time"

	"adatrack_gps/internal"
)

// CommandKind is the whitelisted downlink operation. The set is closed on
// purpose: the device command channel must never become a generic "send any
// string to the device" API (PRD §8.5/§9.6 input validation).
type CommandKind string

const (
	// CommandEngineCut cuts the oil/electricity circuit — GT06 `DYD#`.
	CommandEngineCut CommandKind = "engine_cut"
	// CommandEngineRestore reconnects the circuit — GT06 `HFYD#`.
	CommandEngineRestore CommandKind = "engine_restore"
	// CommandSetInterval changes the device reporting cadence — GT06 `TIMER,<s>#`.
	CommandSetInterval CommandKind = "set_interval"
	// CommandReboot restarts the terminal — GT06 `RESET#`.
	CommandReboot CommandKind = "reboot"
	// CommandLocate asks for an immediate position — GT06 `DWXX#`.
	CommandLocate CommandKind = "locate"
	// CommandDeviceVersion asks the terminal for its firmware/hardware version —
	// TK103 `AP07` (upstream TYPE_GET_VERSION). Useful for support without a
	// maintenance window.
	CommandDeviceVersion CommandKind = "device_version"
	// CommandPositionStop stops periodic position reporting — TK103
	// `AR0000000000` (upstream TYPE_POSITION_STOP). Reversible with set_interval.
	CommandPositionStop CommandKind = "position_stop"
)

// CommandKinds is the whitelist used by validation (API + dispatcher).
var CommandKinds = []CommandKind{
	CommandEngineCut, CommandEngineRestore, CommandSetInterval, CommandReboot, CommandLocate,
	CommandDeviceVersion, CommandPositionStop,
}

// ValidCommandKind reports whether kind is whitelisted.
func ValidCommandKind(kind string) bool {
	for _, k := range CommandKinds {
		if string(k) == kind {
			return true
		}
	}
	return false
}

// Command statuses (mirrors the `td_device_commands` CHECK constraint).
const (
	CommandStatusPending = "pending" // accepted by the API, not yet written to a device
	CommandStatusSent    = "sent"    // written to the live device socket
	CommandStatusAcked   = "acked"   // device replied (0x21/0x15 online-command reply)
	CommandStatusOffline = "offline" // device had no live connection when dispatched
	CommandStatusFailed  = "failed"  // encode/write failed
	CommandStatusTimeout = "timeout" // sent but no device reply inside ACK_TIMEOUT_SECONDS
)

// DeviceCommand is one downlink request. It is both the NATS payload on
// `command.request.<company>` and the `td_device_commands` row.
type DeviceCommand struct {
	ID          int64       `json:"id"`
	RequestID   string      `json:"request_id"`
	CompanyCode string      `json:"company_code"`
	VehicleID   int64       `json:"vehicle_id"`
	IMEI        string      `json:"imei"`
	Kind        CommandKind `json:"command"`
	// IntervalSeconds is mandatory for CommandSetInterval and ignored otherwise.
	IntervalSeconds int `json:"interval_seconds,omitempty"`
	// CreatedBy is the requesting user (audit trail, PRD §9.4).
	CreatedBy int64     `json:"created_by,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

// Validate rejects an incomplete or unsupported request before it reaches a
// device. The dispatcher re-validates: the NATS subject is internal, but a
// malformed frame must never be encoded onto a device socket.
func (c *DeviceCommand) Validate() error {
	if c.CompanyCode == "" {
		return &CommandError{Field: "company_code", Msg: "company_code is required"}
	}
	if c.IMEI == "" {
		return &CommandError{Field: "imei", Msg: "imei is required"}
	}
	if !ValidCommandKind(string(c.Kind)) {
		return &CommandError{Field: "command", Msg: "unsupported command"}
	}
	if c.Kind == CommandSetInterval {
		if c.IntervalSeconds < 5 || c.IntervalSeconds > 86400 {
			return &CommandError{Field: "interval_seconds", Msg: "interval_seconds must be between 5 and 86400"}
		}
	}
	return nil
}

// CommandError is a validation failure on a downlink command.
type CommandError struct {
	Field string
	Msg   string
}

func (e *CommandError) Error() string { return e.Field + ": " + e.Msg }

// CommandResult is the outcome published on `command.result.<company>` after the
// dispatcher wrote the command, or after the device replied / the ACK window
// elapsed. It is also the ACK audit trail persisted into `td_device_commands`.
type CommandResult struct {
	RequestID   string      `json:"request_id"`
	CommandID   int64       `json:"command_id"`
	CompanyCode string      `json:"company_code"`
	VehicleID   int64       `json:"vehicle_id"`
	IMEI        string      `json:"imei"`
	Kind        CommandKind `json:"command"`
	Status      string      `json:"status"`
	// Detail carries the device reply verbatim (e.g. `DYD=Success!`) or the error.
	Detail string `json:"detail,omitempty"`
	// ACK is the raw online-command reply content when the device answered.
	ACK       string    `json:"ack,omitempty"`
	At        time.Time `json:"at"`
	LatencyMS int64     `json:"latency_ms,omitempty"`
}

// CommandSubjectRequest builds the dispatcher subject for one company.
//
// It delegates to internal.CommandRequestSubject so the producer (api-vehicle)
// and this consumer share ONE definition — `handleRequest` rejects any payload
// whose company does not match the subject it arrived on.
func CommandSubjectRequest(company string) string { return internal.CommandRequestSubject(company) }

// CommandSubjectResult builds the result fan-out subject for one company.
func CommandSubjectResult(company string) string { return internal.CommandResultSubject(company) }
