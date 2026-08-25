package tenant

import "strings"

// ---------------------------------------------------------------------------
// Platform tier (konteks primer) — PRD §6.1 multi-tenant governance.
//
// Company registration is a PLATFORM-level operation, not a tenant-level one:
// it must never be performable by a user scoped to an existing company tenant
// (a DEF001 admin must not be able to mint sibling tenants like ABLE01).
// The platform context lives OUTSIDE every tenant, anchored on the reserved
// company_code "default" (database adatrack_gps_default) with the dedicated
// SuperAdmin role (master migration 012_create_platform_tenant.sql).
//
// Contract notes: both values are persisted (JWT claims + seeded rows), so
// they are wire/storage contracts — do NOT rename without a migration.
// ---------------------------------------------------------------------------

const (
	// PlatformCompanyCode is the reserved company_code for the platform tier.
	PlatformCompanyCode = "default"
	// PlatformRole is the role that identifies platform super admins.
	PlatformRole = "SuperAdmin"
)

// IsPlatformCompany reports whether companyCode refers to the platform
// context ("default", case-insensitive, trimmed).
func IsPlatformCompany(companyCode string) bool {
	return strings.EqualFold(strings.TrimSpace(companyCode), PlatformCompanyCode)
}

// IsPlatformRole reports whether role is the platform super-admin role
// (case-insensitive, trimmed).
func IsPlatformRole(role string) bool {
	return strings.EqualFold(strings.TrimSpace(role), PlatformRole)
}

// IsPlatformIdentity reports whether the (companyCode, role) pair belongs to
// the platform tier. Both conditions must hold so that a tenant-scoped user
// can never claim platform rights and vice versa.
func IsPlatformIdentity(companyCode, role string) bool {
	return IsPlatformCompany(companyCode) && IsPlatformRole(role)
}
