package controllers

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"ajb_gps/internal/tenant"
	"ajb_gps/service-websocket/models"
)

// handleCreateCompany implements `POST /api/v1/companies` (FR-5.5, PRD §4.2.1):
// platform-only (SuperAdmin) auto-provisioning of a tenant schema + its default
// admin account, in one call, fully audited.
func (s *Service) handleCreateCompany(c *gin.Context) {
	var req models.CreateCompanyRequest
	if apiErr := bindJSON(c, &req); apiErr != nil {
		s.countHTTPError(apiErr.Status, apiErr.Code)
		respondError(c, apiErr)
		return
	}

	code, err := tenant.NormalizeCode(req.Code)
	if err != nil {
		apiErr := errValidation("code must contain only A-Z, 0-9 or _ (max 20 characters)",
			map[string]string{"code": "invalid"})
		s.countHTTPError(apiErr.Status, apiErr.Code)
		respondError(c, apiErr)
		return
	}
	if code == models.PlatformCompanyCode {
		apiErr := errValidation("code DEFAULT is reserved for the platform tenant",
			map[string]string{"code": "reserved"})
		s.countHTTPError(apiErr.Status, apiErr.Code)
		respondError(c, apiErr)
		return
	}

	countryCode := strings.ToUpper(strings.TrimSpace(req.CountryCode))
	if countryCode == "" {
		countryCode = tenant.DefaultCountryCode
	}
	timezone := strings.TrimSpace(req.Timezone)
	if timezone == "" {
		timezone = tenant.DefaultTimezone
	}
	businessType := strings.ToLower(strings.TrimSpace(req.BusinessType))
	if businessType == "" {
		businessType = "b2b"
	}

	// Idempotency (FR-5.5): an existing tenant is reported with 409 and its
	// admin password is NEVER overwritten.
	exists, cerr := s.store.CompanyExists(c.Request.Context(), code)
	if cerr != nil {
		s.respondStoreError(c, cerr)
		return
	}
	if exists {
		s.countHTTPError(http.StatusConflict, CodeCompanyExists)
		respondError(c, errConflict(CodeCompanyExists,
			"company "+code+" already exists (admin_exists: existing admin was left untouched)"))
		return
	}

	res, perr := s.store.ProvisionTenant(c.Request.Context(), tenant.ProvisionOptions{
		Code:         code,
		Name:         strings.TrimSpace(req.Name),
		BusinessType: businessType,
		CountryCode:  countryCode,
		Timezone:     timezone,
	})
	if perr != nil {
		slogProvisionFailure(c, code, perr)
		s.countHTTPError(http.StatusServiceUnavailable, CodeServiceUnavailable)
		respondError(c, errUnavailable("tenant provisioning failed"))
		return
	}

	adminEmail := strings.TrimSpace(req.AdminEmail)
	if adminEmail == "" {
		adminEmail = defaultAdminEmail(code, s.settings.AdminEmailDomain)
	}
	admin, mustChange, aerr := s.ensureTenantAdmin(c, code, adminEmail)
	if aerr != nil {
		s.respondStoreError(c, aerr)
		return
	}

	applied := 0
	if res.Migrations != nil {
		applied = res.Migrations.Applied
	}
	if aerr := s.auditOnboarding(c, code, businessType, countryCode, timezone, applied, adminEmail); aerr != nil {
		s.countHTTPError(http.StatusServiceUnavailable, CodeServiceUnavailable)
		respondError(c, errUnavailable("audit trail unavailable"))
		return
	}

	respondCreated(c, models.CreateCompanyResponse{
		Code:              code,
		Name:              strings.TrimSpace(req.Name),
		CountryCode:       countryCode,
		Timezone:          timezone,
		BusinessType:      businessType,
		DatabaseName:      res.Schema,
		MigrationsApplied: applied,
		AdminUser:         models.AdminUser{Email: admin.Email, MustChangePassword: mustChange},
	})
}

// auditOnboarding writes the FR-5.5 audit triplet (COMPANY_CREATED +
// TENANT_PROVISIONED + ADMIN_USER_AUTOCREATED) fail-closed.
func (s *Service) auditOnboarding(c *gin.Context, code, businessType, countryCode, timezone string,
	applied int, adminEmail string) error {
	claims, _ := currentClaims(c)
	row := AuditRow{
		ActorIP: c.ClientIP(), ActorUserAgent: c.Request.UserAgent(),
		CompanyCode: code, EntityType: "company", EntityID: code,
		AfterState: map[string]any{
			"code": code, "business_type": businessType, "country_code": countryCode,
			"timezone": timezone, "migrations_applied": applied, "admin_email": adminEmail,
		},
		RequestID: requestID(c),
	}
	if claims != nil {
		row.ActorUserID, row.ActorEmail, row.ActorRole = claims.UserID, claims.Email, claims.Role
	}
	for _, action := range []string{ActionCompanyCreated, ActionTenantProvisioned, ActionAdminUserAutocreated} {
		entry := row
		entry.Action = action
		entry.Outcome = OutcomeSuccess
		if err := s.auditor.RecordSync(c.Request.Context(), entry); err != nil {
			return err
		}
	}
	return nil
}

// ensureTenantAdmin creates the FR-5.5 default tenant admin (`Admin@123`,
// must_change_password) and its per-tenant access row. An existing account is
// reused as-is: the password is never overwritten (idempotent onboarding).
func (s *Service) ensureTenantAdmin(c *gin.Context, code, email string) (models.AdminUser, bool, error) {
	ctx := c.Request.Context()
	email = strings.ToLower(strings.TrimSpace(email))

	exists, err := s.store.UserEmailExists(ctx, email)
	if err != nil {
		return models.AdminUser{}, false, err
	}
	if exists {
		existing, uerr := s.store.UserByEmail(ctx, email)
		if uerr != nil {
			return models.AdminUser{}, false, uerr
		}
		if existing != nil {
			if aerr := s.store.UpsertTenantAccess(ctx, code, existing.ID, models.RoleAdmin); aerr != nil {
				return models.AdminUser{}, false, aerr
			}
			return models.AdminUser{Email: existing.Email, MustChangePassword: existing.MustChangePassword},
				existing.MustChangePassword, nil
		}
	}

	hash, herr := bcrypt.GenerateFromPassword([]byte(s.settings.DefaultAdminPassword), s.settings.BcryptCost)
	if herr != nil {
		return models.AdminUser{}, false, errInternal("could not hash the default admin password")
	}
	id, cerr := s.store.CreateUser(ctx, UserRecord{
		CompanyCode:        code,
		Email:              email,
		FullName:           "Admin " + code,
		PasswordHash:       string(hash),
		GlobalRole:         models.RoleAdmin,
		MustChangePassword: true,
	})
	if cerr != nil {
		return models.AdminUser{}, false, cerr
	}
	if aerr := s.store.UpsertTenantAccess(ctx, code, id, models.RoleAdmin); aerr != nil {
		return models.AdminUser{}, false, aerr
	}
	return models.AdminUser{Email: email, MustChangePassword: true}, true, nil
}

// defaultAdminEmail builds `admin@{code}.local` (FR-5.5).
func defaultAdminEmail(code, domain string) string {
	if strings.TrimSpace(domain) == "" {
		domain = "local"
	}
	return fmt.Sprintf("admin@%s.%s", strings.ToLower(code), strings.ToLower(strings.TrimSpace(domain)))
}

// slogProvisionFailure logs a provisioning failure loudly (never silent) with the
// tenant context needed for incident triage (docs/INCIDENT_RUNBOOK.md).
func slogProvisionFailure(c *gin.Context, code string, err error) {
	slog.Error("service-websocket: tenant provisioning failed",
		"company", code, "request_id", requestID(c), "error", err)
}
