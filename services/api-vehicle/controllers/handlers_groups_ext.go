package controllers

import (
	"github.com/gin-gonic/gin"

	"adatrack_gps/api-vehicle/models"
)

// registerGroupMemberRoutes wires the §1.2 group↔vehicle/driver mapping:
//
//	GET    /api/v1/groups/{id}/members
//	POST   /api/v1/groups/{id}/members           (write)
//	DELETE /api/v1/groups/{id}/members/{memberId} (write)
//
// The membership is soft-deleted (never physically removed), so the mapping
// history survives a driver/vehicle being moved between groups.
func (s *Service) registerGroupMemberRoutes(group *gin.RouterGroup) {
	groups := group.Group("/groups")
	groups.GET("/:id/members", s.handleListGroupMembers)
	groups.POST("/:id/members", s.requireWrite(), s.handleAddGroupMember)
	groups.DELETE("/:id/members/:memberId", s.requireWrite(), s.handleRemoveGroupMember)
}

// handleListGroupMembers implements GET /api/v1/groups/{id}/members.
func (s *Service) handleListGroupMembers(c *gin.Context) {
	if !s.enterpriseStoreOr503(c) {
		return
	}
	identity, _ := currentIdentity(c)
	groupID, perr := pathID(c)
	if perr != nil {
		respondError(c, perr)
		return
	}
	members, err := s.enterprise.ListGroupMembers(c.Request.Context(), identity.companyCode, groupID)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	respondOK(c, members, nil)
}

// handleAddGroupMember implements POST /api/v1/groups/{id}/members.
func (s *Service) handleAddGroupMember(c *gin.Context) {
	if !s.enterpriseStoreOr503(c) {
		return
	}
	identity, _ := currentIdentity(c)
	groupID, perr := pathID(c)
	if perr != nil {
		respondError(c, perr)
		return
	}
	var req models.AddGroupMemberRequest
	if verr := bindJSON(c, &req); verr != nil {
		respondError(c, verr)
		return
	}
	id, err := s.enterprise.AddGroupMember(c.Request.Context(), identity.companyCode, groupID,
		req.MemberType, req.MemberID, identity.userID)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	members, err := s.enterprise.ListGroupMembers(c.Request.Context(), identity.companyCode, groupID)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	for _, member := range members {
		if member.ID == id {
			respondCreated(c, member)
			return
		}
	}
	respondCreated(c, gin.H{"id": id, "group_id": groupID})
}

// handleRemoveGroupMember implements DELETE /api/v1/groups/{id}/members/{memberId}.
func (s *Service) handleRemoveGroupMember(c *gin.Context) {
	if !s.enterpriseStoreOr503(c) {
		return
	}
	identity, _ := currentIdentity(c)
	groupID, perr := pathID(c)
	if perr != nil {
		respondError(c, perr)
		return
	}
	memberID, merr := parsePositiveInt(c.Param("memberId"))
	if merr != nil {
		respondError(c, errValidation("member id must be a positive integer",
			map[string]string{"memberId": "invalid"}))
		return
	}
	affected, err := s.enterprise.RemoveGroupMember(c.Request.Context(), identity.companyCode,
		groupID, memberID, identity.userID)
	if err != nil {
		respondError(c, vehicleStoreErr(err))
		return
	}
	if affected == 0 {
		respondError(c, errNotFound("GROUP_MEMBER_NOT_FOUND", "group member not found"))
		return
	}
	respondOK(c, gin.H{"id": memberID, "removed": true}, nil)
}
