package controllers

import (
	"log/slog"
	"net/http"
	"strings"

	"ajb_gps/internal/tenant"

	"github.com/gin-gonic/gin"
)

// companyCreateRequest is the POST /api/v1/companies body.
// Enterprise-standard optional fields (migration 010) enrich the tenant
// registry: legal entity name, contacts, tax id.
type companyCreateRequest struct {
	Code        string `json:"code"`          // e.g. "ABLE01" — uppercase, trimmed
	Name        string `json:"name"`          // human-readable company name
	CountryCode string `json:"country_code"`  // ISO 3166-1 alpha-2, e.g. "ID"
	Timezone    string `json:"timezone"`      // IANA timezone, e.g. "Asia/Jakarta"
	// --- Enterprise-standard optional fields (migration 010) ---
	LegalName   string `json:"legal_name,omitempty"`    // legal entity name
	CompanyEmail string `json:"company_email,omitempty"` // official contact email
	Website     string `json:"website,omitempty"`       // website URL
	TaxID       string `json:"tax_id,omitempty"`        // NPWP / VAT number
	PostalCode  string `json:"postal_code,omitempty"`   // postal code
}

// companyResponse is the company creation result returned to the client.
type companyResponse struct {
	Code              string `json:"code"`
	Name              string `json:"name"`
	CountryCode       string `json:"country_code"`
	Timezone          string `json:"timezone"`
	DatabaseName      string `json:"database_name"`
	MigrationsApplied int    `json:"migrations_applied"`
}

// companyCreateHandler handles POST /api/v1/companies.
// PLATFORM-only: pendaftaran company adalah operasi level platform (konteks
// primer 'default') — admin sebuah tenant TIDAK boleh membuat company lain
// (PRD §6.1 auto-provision + governance Platform Tier, migration master 012).
func companyCreateHandler(c *gin.Context) {
	if !isPlatformAdmin(c) {
		recordRBACDenial(c, "company.create", "platform_only")
		writeError(c, http.StatusForbidden, "PLATFORM_ONLY", "company registration is a platform-level operation")
		return
	}

	var req companyCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "request body must include code and name")
		return
	}

	req.Code = strings.ToUpper(strings.TrimSpace(req.Code))
	req.Name = strings.TrimSpace(req.Name)
	if req.Code == "" || req.Name == "" {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "code and name are required")
		return
	}

	if appTenant == nil {
		writeError(c, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "tenant manager not initialized")
		return
	}

	result, err := appTenant.ProvisionCompany(c.Request.Context(), tenant.ProvisionCompanyInput{
		Code:         req.Code,
		Name:         req.Name,
		CountryCode:  req.CountryCode,
		Timezone:     req.Timezone,
		LegalName:    strings.TrimSpace(req.LegalName),
		CompanyEmail: strings.TrimSpace(req.CompanyEmail),
		Website:      strings.TrimSpace(req.Website),
		TaxID:        strings.TrimSpace(req.TaxID),
		PostalCode:   strings.TrimSpace(req.PostalCode),
	})
	if err != nil {
		slog.Error("company provision failed", "code", req.Code, "error", err)
		writeError(c, http.StatusInternalServerError, "PROVISION_FAILED", "failed to provision company database")
		return
	}

	countryCode := req.CountryCode
	if countryCode == "" {
		countryCode = "ID"
	}
	timezone := req.Timezone
	if timezone == "" {
		timezone = "Asia/Jakarta"
	}

	writeSuccess(c, http.StatusCreated, companyResponse{
		Code:              result.Code,
		Name:              req.Name,
		CountryCode:       countryCode,
		Timezone:          timezone,
		DatabaseName:      result.DBName,
		MigrationsApplied: result.MigrationsApplied,
	})
}
