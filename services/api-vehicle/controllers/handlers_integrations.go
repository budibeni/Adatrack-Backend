package controllers

import (
	"strings"

	"github.com/gin-gonic/gin"

	"adatrack_gps/api-vehicle/models"
)

// registerIntegrationRoutes wires §1.8 Integrations:
//
//	GET    /api/v1/integrations
//	POST   /api/v1/integrations           (Admin; secret returned ONCE)
//	PATCH  /api/v1/integrations/{id}      (Admin)
//	DELETE /api/v1/integrations/{id}      (Admin, soft delete + immediate disable)
func (s *Service) registerIntegrationRoutes(group *gin.RouterGroup) {
	integrations := group.Group("/integrations")
	integrations.GET("", s.handleListIntegrations)
	integrations.POST("", s.requireAdmin(), s.handleCreateIntegration)
	integrations.PATCH("/:id", s.requireAdmin(), s.handleUpdateIntegration)
	integrations.DELETE("/:id", s.requireAdmin(), s.handleDeleteIntegration)
}

// handleListIntegrations implements GET /api/v1/integrations. The secret is never
// returned again — only the display prefix is kept.
func (s *Service) handleListIntegrations(c *gin.Context) {
	if !s.enterpriseStoreOr503(c) {
		return
	}
	identity, _ := currentIdentity(c)
	items, err := s.enterprise.ListIntegrations(c.Request.Context(), identity.companyCode)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondOK(c, items, nil)
}

// handleCreateIntegration implements POST /api/v1/integrations: the plaintext
// secret is generated server-side and returned exactly once (only its SHA-256
// hash is persisted — the same rule as the media HMAC secret).
func (s *Service) handleCreateIntegration(c *gin.Context) {
	if !s.enterpriseStoreOr503(c) {
		return
	}
	identity, _ := currentIdentity(c)
	var req models.UpsertIntegrationRequest
	if verr := bindJSON(c, &req); verr != nil {
		respondError(c, verr)
		return
	}
	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	if kind == "" {
		respondError(c, errValidation("kind is required",
			map[string]string{"kind": "must be one of: api_key webhook"}))
		return
	}
	endpoint := trimPtr(req.EndpointURL)
	if kind == "webhook" && (endpoint == nil || *endpoint == "") {
		respondError(c, errValidation("endpoint_url is required for a webhook",
			map[string]string{"endpoint_url": "required"}))
		return
	}
	if kind == "api_key" && endpoint != nil {
		endpoint = nil // an API key has no endpoint; ignore rather than mislead
	}
	ctx := c.Request.Context()
	exists, err := s.enterprise.IntegrationNameExists(ctx, identity.companyCode, req.Name, 0)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	if exists {
		respondError(c, errConflict("an integration with this name already exists"))
		return
	}

	prefix := "adt_"
	if kind == "webhook" {
		prefix = "whsec_"
	}
	random, gerr := randomToken(24)
	if gerr != nil {
		respondError(c, errInternal("could not generate the integration secret"))
		return
	}
	secret := prefix + random
	keyPrefix := secret[:min(len(secret), 12)]
	status := "active"
	if req.Status != nil {
		status = *req.Status
	}
	integration := &models.Integration{
		Name:        strings.TrimSpace(req.Name),
		Kind:        kind,
		EndpointURL: endpoint,
		KeyPrefix:   &keyPrefix,
		Events:      req.Events,
		Status:      status,
		Notes:       req.Notes,
	}
	id, err := s.enterprise.CreateIntegration(ctx, identity.companyCode, integration, hashSecret(secret), identity.userID)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	created, err := s.enterprise.IntegrationByID(ctx, identity.companyCode, id)
	if err != nil || created == nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	created.Secret = secret // one-time display
	respondCreated(c, created)
}

// handleUpdateIntegration implements PATCH /api/v1/integrations/{id}: name,
// endpoint, events and status are mutable; the secret is immutable (rotate by
// creating a new integration so the rotation itself is auditable).
func (s *Service) handleUpdateIntegration(c *gin.Context) {
	if !s.enterpriseStoreOr503(c) {
		return
	}
	identity, _ := currentIdentity(c)
	id, perr := pathID(c)
	if perr != nil {
		respondError(c, perr)
		return
	}
	var req models.UpsertIntegrationRequest
	if verr := bindJSON(c, &req); verr != nil {
		respondError(c, verr)
		return
	}
	ctx := c.Request.Context()
	existing, err := s.enterprise.IntegrationByID(ctx, identity.companyCode, id)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	if existing == nil {
		respondError(c, errNotFound("INTEGRATION_NOT_FOUND", "integration not found"))
		return
	}
	if name := strings.TrimSpace(req.Name); name != "" && name != existing.Name {
		taken, terr := s.enterprise.IntegrationNameExists(ctx, identity.companyCode, name, id)
		if terr != nil {
			respondError(c, vehicleStoreErr(terr))
			return
		}
		if taken {
			respondError(c, errConflict("an integration with this name already exists"))
			return
		}
		existing.Name = name
	}
	if req.EndpointURL != nil {
		existing.EndpointURL = trimPtr(req.EndpointURL)
	}
	if req.Events != nil {
		existing.Events = req.Events
	}
	if req.Status != nil {
		existing.Status = *req.Status
	}
	if req.Notes != nil {
		existing.Notes = req.Notes
	}
	affected, err := s.enterprise.UpdateIntegration(ctx, identity.companyCode, existing, identity.userID)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	if affected == 0 {
		respondError(c, errNotFound("INTEGRATION_NOT_FOUND", "integration not found"))
		return
	}
	updated, err := s.enterprise.IntegrationByID(ctx, identity.companyCode, id)
	if err != nil || updated == nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondOK(c, updated, nil)
}

// handleDeleteIntegration implements DELETE /api/v1/integrations/{id}.
func (s *Service) handleDeleteIntegration(c *gin.Context) {
	if !s.enterpriseStoreOr503(c) {
		return
	}
	identity, _ := currentIdentity(c)
	id, perr := pathID(c)
	if perr != nil {
		respondError(c, perr)
		return
	}
	var body deleteReasonRequest
	_ = c.ShouldBindJSON(&body)
	reason := body.Reason
	if reason == "" {
		reason = "deleted via api"
	}
	affected, err := s.enterprise.SoftDeleteIntegration(c.Request.Context(), identity.companyCode,
		id, identity.userID, reason)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	if affected == 0 {
		respondError(c, errNotFound("INTEGRATION_NOT_FOUND", "integration not found"))
		return
	}
	respondOK(c, gin.H{"id": id, "deleted": true}, nil)
}

// trimPtr normalises an optional string field (nil/blank → nil).
func trimPtr(in *string) *string {
	if in == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*in)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
