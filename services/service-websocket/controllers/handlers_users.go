package controllers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"adatrack_gps/internal/tenant"
	"adatrack_gps/service-websocket/models"
)

// handleCreateUser implements `POST /api/v1/users` (FR-5.6): platform-only
// onboarding of one primary tenant account (master insert + per-tenant access +
// optional row-level vehicle grants) in a single audited call.
func (s *Service) handleCreateUser(c *gin.Context) {
	var req models.CreateUserRequest
	if apiErr := bindJSON(c, &req); apiErr != nil {
		s.countHTTPError(apiErr.Status, apiErr.Code)
		respondError(c, apiErr)
		return
	}

	// Guard: the SuperAdmin role is reserved for the platform tier (FR-5.6).
	if req.Role == models.RoleSuperAdmin {
		s.denyRequest(c, nil, errForbidden(CodePlatformRoleReserved,
			"the SuperAdmin role is reserved for the platform tier"), "platform_role_reserved")
		return
	}

	code, err := tenant.NormalizeCode(req.CompanyCode)
	if err != nil {
		apiErr := errValidation("company_code must contain only A-Z, 0-9 or _",
			map[string]string{"company_code": "invalid"})
		s.countHTTPError(apiErr.Status, apiErr.Code)
		respondError(c, apiErr)
		return
	}
	// Guard: the platform context cannot hold tenant users (FR-5.6).
	if code == models.PlatformCompanyCode {
		apiErr := errValidation("company_code DEFAULT is the platform context, not a tenant",
			map[string]string{"company_code": "reserved"})
		s.countHTTPError(apiErr.Status, apiErr.Code)
		respondError(c, apiErr)
		return
	}

	exists, cerr := s.store.CompanyExists(c.Request.Context(), code)
	if cerr != nil {
		s.respondStoreError(c, cerr)
		return
	}
	if !exists {
		respondError(c, errNotFound(CodeCompanyNotFound, "company "+code+" is not registered"))
		return
	}

	email := strings.ToLower(strings.TrimSpace(req.Email))
	emailTaken, eerr := s.store.UserEmailExists(c.Request.Context(), email)
	if eerr != nil {
		s.respondStoreError(c, eerr)
		return
	}
	if emailTaken {
		s.countHTTPError(http.StatusConflict, CodeUserExists)
		respondError(c, errConflict(CodeUserExists, "email "+email+" is already registered"))
		return
	}

	// Vehicle grants must reference vehicles of THIS tenant (anti-IDOR, PRD §9.6).
	var granted []int64
	if len(req.VehicleIDs) > 0 {
		existing, verr := s.store.ExistingVehicleIDs(c.Request.Context(), code, req.VehicleIDs)
		if verr != nil {
			s.respondStoreError(c, verr)
			return
		}
		if len(existing) != len(req.VehicleIDs) {
			apiErr := errValidation("one or more vehicle_ids do not belong to this tenant",
				map[string]string{"vehicle_ids": "unknown vehicle"})
			s.countHTTPError(apiErr.Status, apiErr.Code)
			respondError(c, apiErr)
			return
		}
		granted = existing
	}

	password := req.Password
	if strings.TrimSpace(password) == "" {
		password = s.settings.DefaultAdminPassword
	}
	hash, herr := bcrypt.GenerateFromPassword([]byte(password), s.settings.BcryptCost)
	if herr != nil {
		respondError(c, errInternal("could not hash the password"))
		return
	}

	userID, cerr := s.store.CreateUser(c.Request.Context(), UserRecord{
		CompanyCode:        code,
		Email:              email,
		FullName:           strings.TrimSpace(req.FullName),
		PasswordHash:       string(hash),
		GlobalRole:         req.Role,
		MustChangePassword: true,
	})
	if cerr != nil {
		s.respondStoreError(c, cerr)
		return
	}
	if aerr := s.store.UpsertTenantAccess(c.Request.Context(), code, userID, req.Role); aerr != nil {
		s.respondStoreError(c, aerr)
		return
	}
	if len(granted) > 0 {
		if aerr := s.store.AssignVehicles(c.Request.Context(), code, userID, granted); aerr != nil {
			s.respondStoreError(c, aerr)
			return
		}
	}

	// Audit the onboarding (fail-closed, PRD §9.4).
	claims, _ := currentClaims(c)
	row := AuditRow{
		Action: ActionUserCreated, Outcome: OutcomeSuccess,
		CompanyCode: code, EntityType: "user", EntityID: itoa(userID),
		AfterState: map[string]any{
			"email": email, "full_name": strings.TrimSpace(req.FullName),
			"role": req.Role, "vehicle_ids": granted,
		},
		RequestID: requestID(c),
	}
	if claims != nil {
		row.ActorUserID, row.ActorEmail, row.ActorRole = claims.UserID, claims.Email, claims.Role
	}
	if aerr := s.auditor.RecordSync(c.Request.Context(), row); aerr != nil {
		respondError(c, errUnavailable("audit trail unavailable"))
		return
	}

	respondCreated(c, models.CreateUserResponse{
		ID:                 userID,
		Email:              email,
		FullName:           strings.TrimSpace(req.FullName),
		Role:               req.Role,
		CompanyCode:        code,
		MustChangePassword: true,
		VehicleIDs:         granted,
	})
}
