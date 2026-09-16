package controllers

import (
	"time"

	"github.com/gin-gonic/gin"

	"ajb_gps/api-vehicle/models"
)

// assignmentID parses the `:assignmentId` path parameter.
func assignmentID(c *gin.Context) (int64, *APIError) {
	raw := c.Param("assignmentId")
	id, err := parsePositiveInt(raw)
	if err != nil {
		return 0, errValidation("assignment id must be a positive integer",
			map[string]string{"assignmentId": "invalid"})
	}
	return id, nil
}

// handleListAssignments implements GET /api/v1/routes/:id/assignments.
func (s *Service) handleListAssignments(c *gin.Context) {
	identity, _ := currentIdentity(c)
	routeID, perr := pathID(c)
	if perr != nil {
		respondError(c, perr)
		return
	}
	items, err := s.store.ListAssignments(c.Request.Context(), identity.companyCode, routeID)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondOK(c, items, nil)
}

// handleCreateAssignment implements POST /api/v1/routes/:id/assignments
// (PRD §5.9.2: a fresh assignment always starts as not_started).
func (s *Service) handleCreateAssignment(c *gin.Context) {
	identity, _ := currentIdentity(c)
	routeID, perr := pathID(c)
	if perr != nil {
		respondError(c, perr)
		return
	}
	var req models.CreateAssignmentRequest
	if verr := bindJSON(c, &req); verr != nil {
		respondError(c, verr)
		return
	}
	ctx := c.Request.Context()
	route, err := s.store.RouteByID(ctx, identity.companyCode, routeID, false)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	if route == nil {
		respondError(c, errNotFound(CodeRouteNotFound, "route not found"))
		return
	}
	vehicle, err := s.store.VehicleByID(ctx, identity.companyCode, req.VehicleID, false)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	if vehicle == nil {
		respondError(c, errNotFound(CodeVehicleNotFound, "vehicle not found"))
		return
	}
	a := &models.RouteAssignment{
		RouteID:      routeID,
		VehicleID:    req.VehicleID,
		DriverUserID: req.DriverUserID,
		Status:       models.AssignNotStarted,
	}
	if _, err := s.store.CreateAssignment(ctx, identity.companyCode, a, identity.userID); err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	created, err := s.store.AssignmentByID(ctx, identity.companyCode, routeID, a.ID)
	if err != nil || created == nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondCreated(c, created)
}

// handlePatchAssignment implements PATCH /api/v1/routes/:id/assignments/:id —
// the MANUAL status transition (PRD §5.9.2 state machine), stamping started_at
// on the first in_progress and completed_at on completion.
func (s *Service) handlePatchAssignment(c *gin.Context) {
	identity, _ := currentIdentity(c)
	routeID, perr := pathID(c)
	if perr != nil {
		respondError(c, perr)
		return
	}
	id, aerr := assignmentID(c)
	if aerr != nil {
		respondError(c, aerr)
		return
	}
	var req models.PatchAssignmentRequest
	if verr := bindJSON(c, &req); verr != nil {
		respondError(c, verr)
		return
	}
	ctx := c.Request.Context()
	existing, err := s.store.AssignmentByID(ctx, identity.companyCode, routeID, id)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	if existing == nil {
		respondError(c, errNotFound(CodeAssignmentNotFound, "assignment not found"))
		return
	}
	if !transitionAllowed(existing.Status, req.Status) {
		respondError(c, errBadRequest(CodeInvalidTransition,
			"cannot transition from "+existing.Status+" to "+req.Status))
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	updated := *existing
	updated.Status = req.Status
	if req.Status == models.AssignInProgress && existing.StartedAt == nil {
		updated.StartedAt = &now
	}
	if req.Status == models.AssignCompleted {
		updated.CompletedAt = &now
		if updated.StartedAt == nil {
			updated.StartedAt = &now
		}
	}
	if err := s.store.UpdateAssignmentStatus(ctx, identity.companyCode, &updated); err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	after, err := s.store.AssignmentByID(ctx, identity.companyCode, routeID, id)
	if err != nil || after == nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondOK(c, after, nil)
}

// handleDeleteAssignment implements DELETE /api/v1/routes/:id/assignments/:id.
func (s *Service) handleDeleteAssignment(c *gin.Context) {
	identity, _ := currentIdentity(c)
	routeID, perr := pathID(c)
	if perr != nil {
		respondError(c, perr)
		return
	}
	id, aerr := assignmentID(c)
	if aerr != nil {
		respondError(c, aerr)
		return
	}
	var body deleteReasonRequest
	_ = c.ShouldBindJSON(&body)
	reason := body.Reason
	if reason == "" {
		reason = "deleted via api"
	}
	if err := s.store.SoftDeleteAssignment(c.Request.Context(), identity.companyCode, routeID, id, identity.userID, reason); err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondOK(c, gin.H{"id": id, "deleted": true}, nil)
}

// transitionAllowed checks the manual status machine (PRD §5.9.2).
func transitionAllowed(from, to string) bool {
	for _, next := range models.AllowedAssignmentTransitions[from] {
		if next == to {
			return true
		}
	}
	return from == to
}
