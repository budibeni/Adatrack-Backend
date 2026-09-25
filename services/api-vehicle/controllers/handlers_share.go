package controllers

import (
	"time"

	"github.com/gin-gonic/gin"

	"adatrack_gps/api-vehicle/models"
)

// registerShareRoutes wires §1.1 "Berbagi Lokasi" inside the authenticated tenant
// group; the PUBLIC read endpoint is registered separately (no auth) because a
// share link is meant to be opened by someone without an account (FR-9.3).
func (s *Service) registerShareRoutes(group *gin.RouterGroup) {
	shares := group.Group("/share-links")
	shares.GET("", s.requireAdmin(), s.handleListShareLinks)
	shares.POST("", s.requireAdmin(), s.handleCreateShareLink)
	shares.DELETE("/:id", s.requireAdmin(), s.handleRevokeShareLink)
}

// registerPublicShareRoute mounts the unauthenticated read endpoint
// `GET /api/v1/share/{token}` (FR-9.3). It cannot use the tenant middleware (there
// is no caller identity) — the token itself carries the company code.
func (s *Service) registerPublicShareRoute(api *gin.RouterGroup) {
	api.GET("/share/:token", s.handlePublicShare)
}

// handleCreateShareLink implements POST /api/v1/share-links (Admin).
func (s *Service) handleCreateShareLink(c *gin.Context) {
	if !s.enterpriseStoreOr503(c) {
		return
	}
	identity, _ := currentIdentity(c)
	var req models.CreateShareLinkRequest
	if verr := bindJSON(c, &req); verr != nil {
		respondError(c, verr)
		return
	}
	token, err := randomToken(24)
	if err != nil {
		respondError(c, errInternal("could not generate a share token"))
		return
	}
	link := &models.ShareLink{
		Token:      token,
		Label:      trimPtr(req.Label),
		Scope:      "vehicles",
		VehicleIDs: req.VehicleIDs,
	}
	expiresAt := time.Now().UTC().Add(time.Duration(req.TTLMinutes) * time.Minute)
	id, err := s.enterprise.CreateShareLink(c.Request.Context(), identity.companyCode, link, expiresAt, identity.userID)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	links, err := s.enterprise.ListShareLinks(c.Request.Context(), identity.companyCode)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	for i := range links {
		if links[i].ID == id {
			respondCreated(c, links[i])
			return
		}
	}
	respondCreated(c, gin.H{"id": id, "token": token})
}

// handleListShareLinks implements GET /api/v1/share-links (Admin).
func (s *Service) handleListShareLinks(c *gin.Context) {
	if !s.enterpriseStoreOr503(c) {
		return
	}
	identity, _ := currentIdentity(c)
	links, err := s.enterprise.ListShareLinks(c.Request.Context(), identity.companyCode)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondOK(c, links, nil)
}

// handleRevokeShareLink implements DELETE /api/v1/share-links/{id} (Admin).
func (s *Service) handleRevokeShareLink(c *gin.Context) {
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
		reason = "revoked via api"
	}
	affected, err := s.enterprise.RevokeShareLink(c.Request.Context(), identity.companyCode,
		id, identity.userID, reason)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	if affected == 0 {
		respondError(c, errNotFound("SHARE_LINK_NOT_FOUND", "share link not found"))
		return
	}
	respondOK(c, gin.H{"id": id, "revoked": true}, nil)
}

// handlePublicShare implements GET /api/v1/share/{token} (FR-9.3, no auth).
// A revoked or expired token is a 404 — the endpoint never reveals why, and it
// only ever returns the last known position of the link's own vehicles.
func (s *Service) handlePublicShare(c *gin.Context) {
	if !s.enterpriseStoreOr503(c) {
		return
	}
	token := c.Param("token")
	if len(token) < 16 || len(token) > 64 {
		respondError(c, errNotFound("SHARE_LINK_NOT_FOUND", "share link not found"))
		return
	}
	ctx := c.Request.Context()
	link, err := s.enterprise.ResolveShareLink(ctx, token)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	if link == nil {
		respondError(c, errNotFound("SHARE_LINK_NOT_FOUND", "share link not found"))
		return
	}
	vehicles, err := s.enterprise.SharedVehicles(ctx, link.CompanyCode, link.VehicleIDs)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondOK(c, gin.H{
		"label":      link.Label,
		"expires_at": link.ExpiresAt,
		"vehicles":   vehicles,
	}, nil)
}
