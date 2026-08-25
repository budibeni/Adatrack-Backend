package tenant

import "testing"

func TestIsPlatformCompany(t *testing.T) {
	cases := map[string]bool{
		"default":   true,
		"DEFAULT":   true,
		" Default ": true,
		"DefaUlt":   true,
		"DEF001":    false,
		"dev001":    false,
		"":          false,
		"   ":       false,
	}
	for code, want := range cases {
		if got := IsPlatformCompany(code); got != want {
			t.Errorf("IsPlatformCompany(%q) = %v, want %v", code, got, want)
		}
	}
}

func TestIsPlatformRole(t *testing.T) {
	cases := map[string]bool{
		"SuperAdmin":  true,
		"superadmin":  true,
		" SUPERADMIN": true,
		"Admin":       false,
		"Operator":    false,
		"":            false,
	}
	for role, want := range cases {
		if got := IsPlatformRole(role); got != want {
			t.Errorf("IsPlatformRole(%q) = %v, want %v", role, got, want)
		}
	}
}

func TestIsPlatformIdentity(t *testing.T) {
	if !IsPlatformIdentity("default", "SuperAdmin") {
		t.Error("expected platform identity for (default, SuperAdmin)")
	}
	if !IsPlatformIdentity("DEFAULT", "superadmin") {
		t.Error("expected case-insensitive platform identity")
	}
	if IsPlatformIdentity("DEF001", "SuperAdmin") {
		t.Error("tenant company with SuperAdmin role must not be platform identity")
	}
	if IsPlatformIdentity("default", "Admin") {
		t.Error("default company without SuperAdmin role must not be platform identity")
	}
}

// TestPlatformConstantsStable guards against accidental renaming: the values
// are persisted in JWT claims and seeded rows (master migration 012), so they
// are part of the wire/storage contract.
func TestPlatformConstantsStable(t *testing.T) {
	if PlatformCompanyCode != "default" {
		t.Errorf("PlatformCompanyCode changed: %q", PlatformCompanyCode)
	}
	if PlatformRole != "SuperAdmin" {
		t.Errorf("PlatformRole changed: %q", PlatformRole)
	}
}
