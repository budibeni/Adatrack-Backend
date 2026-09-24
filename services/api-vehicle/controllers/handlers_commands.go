package controllers

// handlers_commands.go — B8 downlink command endpoints (PRD §21.2 row 1):
//
//	POST /api/v1/vehicles/{id}/commands  → validate + persist `pending` + publish
//	GET  /api/v1/vehicles/{id}/commands  → delivery history + ACK content
//
// The endpoint never talks to a device directly: it writes the audit row and
// publishes `command.request.<company>`, which ingestion-tcp (the only process
// holding the device sockets) consumes. The device's reply then lands back in the
// same row as `acked`/`failed` — that is the "ACK device tercatat" acceptance.

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"

	"adatrack_gps/api-vehicle/models"
	"adatrack_gps/internal/validate"
)

// CommandPublisher publishes a downlink request (implemented by the NATS
// adapter in main.go; tests inject a fake).
type CommandPublisher interface {
	PublishDeviceCommand(company string, cmd *models.DeviceCommand) error
}

// allowedCommandStatuses mirrors the td_device_commands CHECK constraint.
var allowedCommandStatuses = map[string]bool{
	models.CommandPending: true, models.CommandSent: true, models.CommandAcked: true,
	models.CommandOffline: true, models.CommandFailed: true, models.CommandTimeout: true,
}

// handleCreateVehicleCommand implements POST /api/v1/vehicles/:id/commands.
func (s *Service) handleCreateVehicleCommand(c *gin.Context) {
	identity, _ := currentIdentity(c)
	id, perr := pathID(c)
	if perr != nil {
		respondError(c, perr)
		return
	}
	var req models.CommandRequest
	if verr := bindJSON(c, &req); verr != nil {
		respondError(c, verr)
		return
	}
	// Validation (PRD §8.5): the command set is closed and set_interval carries a
	// mandatory, bounded argument. Nothing else ever reaches a device.
	params, verr := validateCommandRequest(&req)
	if verr != nil {
		respondError(c, verr)
		return
	}

	ctx := c.Request.Context()
	v, err := s.store.VehicleByID(ctx, identity.companyCode, id, false)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	if v == nil {
		respondError(c, errNotFound(CodeVehicleNotFound, "vehicle not found"))
		return
	}
	if v.Status != "" && v.Status != "active" {
		respondError(c, errConflict("vehicle is not active"))
		return
	}

	cmd := &models.DeviceCommand{
		RequestID:   newCommandRequestID(),
		CompanyCode: identity.companyCode,
		VehicleID:   v.ID,
		IMEI:        v.IMEI,
		Command:     req.Command,
		Parameters:  params,
		Status:      models.CommandPending,
		CreatedBy:   identity.userID,
		CreatedAt:   time.Now().UTC(),
	}
	if _, err := s.store.CreateDeviceCommand(ctx, identity.companyCode, cmd); err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}

	// Dispatch is best-effort: a NATS outage must not lose the audited request —
	// the row stays `pending` and the operator can re-issue it.
	if s.commands == nil {
		respondError(c, errUnavailable("command dispatch unavailable"))
		return
	}
	if err := s.commands.PublishDeviceCommand(identity.companyCode, cmd); err != nil {
		slog.Error("downlink: publish failed", "company", identity.companyCode,
			"vehicle_id", v.ID, "command", cmd.Command, "request_id", cmd.RequestID, "error", err)
		respondError(c, errUnavailable("command dispatch unavailable"))
		return
	}
	commandsRequested.WithLabelValues(cmd.Command).Inc()

	stored, err := s.store.DeviceCommandByRequestID(ctx, identity.companyCode, cmd.RequestID)
	if err != nil || stored == nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondCreated(c, stored)
}

// handleListVehicleCommands implements GET /api/v1/vehicles/:id/commands.
func (s *Service) handleListVehicleCommands(c *gin.Context) {
	identity, _ := currentIdentity(c)
	id, perr := pathID(c)
	if perr != nil {
		respondError(c, perr)
		return
	}
	page, limit, perr := s.parsePagination(c)
	if perr != nil {
		respondError(c, perr)
		return
	}
	q := CommandQuery{
		CompanyCode: identity.companyCode,
		VehicleID:   id,
		Status:      c.Query("status"),
		AssignedIDs: identity.assigned,
		AllVehicles: identity.allVehicles,
		Page:        page,
		Limit:       limit,
	}
	if q.Status != "" && !allowedCommandStatuses[q.Status] {
		respondError(c, errValidation("invalid status filter",
			map[string]string{"status": "must be one of: pending sent acked offline failed timeout"}))
		return
	}
	items, total, err := s.store.ListDeviceCommands(c.Request.Context(), q)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondOK(c, items, pagination(page, limit, total))
}

// newCommandRequestID returns a random 128-bit hex request id. It is generated
// in-process (crypto/rand) so api-vehicle does not need a UUID dependency, and it
// is the idempotency key of the `td_device_commands` audit row.
func newCommandRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// validateCommandRequest applies the closed whitelist + argument bounds through
// the shared B10 validation primitives (PRD §8.5), so the command channel uses the
// same policy object as every other input.
func validateCommandRequest(req *models.CommandRequest) (map[string]any, *APIError) {
	if err := validate.OneOf("command", req.Command,
		models.CommandEngineCut, models.CommandEngineRestore, models.CommandSetInterval,
		models.CommandReboot, models.CommandLocate); err != nil {
		return nil, errValidation("unsupported command",
			map[string]string{"command": "must be one of: engine_cut engine_restore set_interval reboot locate"})
	}
	if req.Command == models.CommandSetInterval {
		if err := validate.IntRange("interval_seconds", req.IntervalSeconds, 5, 86400); err != nil {
			return nil, errValidation("interval_seconds must be between 5 and 86400",
				map[string]string{"interval_seconds": "must be between 5 and 86400"})
		}
		return map[string]any{"interval_seconds": req.IntervalSeconds}, nil
	}
	return map[string]any{}, nil
}
