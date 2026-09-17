package controllers

import (
	"github.com/gin-gonic/gin"

	"adatrack_gps/service-websocket/models"
)

// handleLogin implements `POST /api/v1/auth/login` (PRD §8.2, §9.1).
func (s *Service) handleLogin(c *gin.Context) {
	var req models.LoginRequest
	if apiErr := bindJSON(c, &req); apiErr != nil {
		s.countHTTPError(apiErr.Status, apiErr.Code)
		respondError(c, apiErr)
		return
	}

	resp, err := s.auth.Login(c.Request.Context(), req, c.ClientIP(), c.Request.UserAgent(), requestID(c))
	if err != nil {
		apiErr := asAPIError(err)
		s.countHTTPError(apiErr.Status, apiErr.Code)
		respondError(c, apiErr)
		return
	}
	respondOK(c, resp, nil)
}

// handleRefresh implements `POST /api/v1/auth/refresh` (FR-5.7): the refresh
// token is rotated on every call.
func (s *Service) handleRefresh(c *gin.Context) {
	var req models.RefreshRequest
	if apiErr := bindJSON(c, &req); apiErr != nil {
		s.countHTTPError(apiErr.Status, apiErr.Code)
		respondError(c, apiErr)
		return
	}

	resp, err := s.auth.Refresh(c.Request.Context(), req.RefreshToken, c.ClientIP(),
		c.Request.UserAgent(), requestID(c))
	if err != nil {
		apiErr := asAPIError(err)
		s.countHTTPError(apiErr.Status, apiErr.Code)
		respondError(c, apiErr)
		return
	}
	respondOK(c, resp, nil)
}

// handleLogout implements `POST /api/v1/auth/logout` (FR-5.7): the access token
// jti is denylisted and the presented refresh token is revoked.
func (s *Service) handleLogout(c *gin.Context) {
	var req models.LogoutRequest
	// An empty body is valid (revoking just the access token), but a present body
	// must still validate.
	if c.Request.ContentLength > 0 {
		if apiErr := bindJSON(c, &req); apiErr != nil {
			s.countHTTPError(apiErr.Status, apiErr.Code)
			respondError(c, apiErr)
			return
		}
	}

	claims, ok := currentClaims(c)
	if !ok {
		respondError(c, errUnauthorized("unauthenticated"))
		return
	}
	if err := s.auth.Logout(c.Request.Context(), claims, req.RefreshToken, c.ClientIP(),
		c.Request.UserAgent(), requestID(c)); err != nil {
		apiErr := asAPIError(err)
		s.countHTTPError(apiErr.Status, apiErr.Code)
		respondError(c, apiErr)
		return
	}
	respondOK(c, gin.H{"revoked": true}, nil)
}
