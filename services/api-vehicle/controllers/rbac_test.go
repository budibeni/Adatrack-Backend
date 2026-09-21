package controllers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"adatrack_gps/api-vehicle/models"
)

// newRBACService wires a Service whose verifier trusts `settings` (the JWT
// secret shared with service-websocket). A nil KV disables the denylist.
func newRBACService(store Store, settings Settings, kv *RedisKV) *Service {
	return NewService(Deps{Settings: settings, Store: store, KV: kv})
}

// bearerContext builds a request context carrying the Authorization header.
func bearerContext(method, target, token string, identity *tenantIdentity) (*gin.Context, *httptest.ResponseRecorder) {
	c, rec := testContext(method, target, "", identity)
	if token != "" {
		c.Request.Header.Set("Authorization", "Bearer "+token)
	}
	return c, rec
}

// decodeErr reads the PRD §8.1 error envelope of a response.
func decodeErr(t *testing.T, rec *httptest.ResponseRecorder) models.ErrorEnvelope {
	t.Helper()
	var env models.ErrorEnvelope
	if err := parseJSON(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	return env
}

// TestExtractToken covers the bearer parsing (case-insensitive scheme, missing
// scheme, empty header) — PRD §8.6.
func TestExtractToken(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   string
	}{
		{"missing header", "", ""},
		{"canonical", "Bearer abc.def.ghi", "abc.def.ghi"},
		{"lowercase scheme", "bearer abc", "abc"},
		{"mixed case scheme", "BeArEr abc", "abc"},
		{"extra spaces", "Bearer    abc  ", "abc"},
		{"wrong scheme", "Basic abc", ""},
		{"no scheme", "abc", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := testContext(http.MethodGet, "/api/v1/vehicles", "", nil)
			if tc.header != "" {
				c.Request.Header.Set("Authorization", tc.header)
			}
			if got := extractToken(c); got != tc.want {
				t.Errorf("extractToken(%q) = %q, want %q", tc.header, got, tc.want)
			}
		})
	}
}

// TestResolveIdentityPlatform: a SuperAdmin in the `default` context is the
// governance tier — tenant-wide, never a tenant member (PRD §3.1).
func TestResolveIdentityPlatform(t *testing.T) {
	svc := newRBACService(newFakeStore(), authSettings(), nil)

	identity, err := svc.resolveIdentity(context.Background(), &UserRecord{
		ID: 1, Email: "root@platform", CompanyCode: models.PlatformCompanyCode,
		GlobalRole: models.RoleSuperAdmin, IsActive: true,
	})
	if err != nil {
		t.Fatalf("resolveIdentity: %v", err)
	}
	if !identity.platform || !identity.allVehicles {
		t.Errorf("identity = %+v, want platform + allVehicles", identity)
	}
}

// TestResolveIdentityTenantRoles covers the effective-role resolution: the
// membership override wins, Admin/Manager are tenant-wide, everyone else is
// limited to their tm_user_vehicles grants (PRD §3.1/§9.2).
func TestResolveIdentityTenantRoles(t *testing.T) {
	t.Run("membership override wins", func(t *testing.T) {
		store := newFakeStore()
		store.seedUser(&UserRecord{ID: 9, Email: "op@test", CompanyCode: "DEV001", GlobalRole: models.RoleOperator, IsActive: true})
		store.seedTenantAccess("DEV001", 9, models.RoleAdmin)
		svc := newRBACService(store, authSettings(), nil)

		identity, err := svc.resolveIdentity(context.Background(), store.users[9])
		if err != nil {
			t.Fatalf("resolveIdentity: %v", err)
		}
		if identity.role != models.RoleAdmin || !identity.allVehicles {
			t.Errorf("identity = %+v, want Admin + allVehicles", identity)
		}
	})

	t.Run("global role kept without override", func(t *testing.T) {
		store := newFakeStore()
		store.seedUser(&UserRecord{ID: 9, CompanyCode: "dev001", GlobalRole: models.RoleManager, IsActive: true})
		store.seedTenantAccess("DEV001", 9, "")
		svc := newRBACService(store, authSettings(), nil)

		identity, err := svc.resolveIdentity(context.Background(), store.users[9])
		if err != nil {
			t.Fatalf("resolveIdentity: %v", err)
		}
		if identity.role != models.RoleManager || !identity.allVehicles {
			t.Errorf("identity = %+v, want Manager + allVehicles", identity)
		}
		if identity.companyCode != "DEV001" {
			t.Errorf("companyCode = %q, want the normalised DEV001", identity.companyCode)
		}
	})

	t.Run("operator gets row-level grants", func(t *testing.T) {
		store := newFakeStore()
		store.seedUser(&UserRecord{ID: 9, CompanyCode: "DEV001", GlobalRole: models.RoleOperator, IsActive: true})
		store.seedTenantAccess("DEV001", 9, "")
		store.seedAssignedVehicles(9, 2, 5)
		svc := newRBACService(store, authSettings(), nil)

		identity, err := svc.resolveIdentity(context.Background(), store.users[9])
		if err != nil {
			t.Fatalf("resolveIdentity: %v", err)
		}
		if identity.allVehicles || len(identity.assigned) != 2 {
			t.Errorf("identity = %+v, want 2 row-level grants", identity)
		}
		if !vehicleAllowed(identity, 5) || vehicleAllowed(identity, 3) {
			t.Error("only the granted vehicles must be reachable")
		}
	})
}

// TestResolveIdentityFailureModes: revoked membership (401), no membership
// (403) and an unavailable authorization backend (503).
func TestResolveIdentityFailureModes(t *testing.T) {
	t.Run("inactive membership", func(t *testing.T) {
		store := newFakeStore()
		store.seedUser(&UserRecord{ID: 9, CompanyCode: "DEV001", GlobalRole: models.RoleOperator, IsActive: true})
		store.seedTenantAccessInactive("DEV001", 9)
		svc := newRBACService(store, authSettings(), nil)

		_, err := svc.resolveIdentity(context.Background(), store.users[9])
		apiErr, ok := err.(*APIError)
		if !ok || apiErr.Status != 401 || apiErr.Code != CodeAccountInactive {
			t.Fatalf("err = %v, want 401 %s", err, CodeAccountInactive)
		}
	})

	t.Run("no membership", func(t *testing.T) {
		store := newFakeStore()
		store.seedUser(&UserRecord{ID: 9, CompanyCode: "DEV001", GlobalRole: models.RoleOperator, IsActive: true})
		svc := newRBACService(store, authSettings(), nil)

		_, err := svc.resolveIdentity(context.Background(), store.users[9])
		apiErr, ok := err.(*APIError)
		if !ok || apiErr.Status != 403 || apiErr.Code != CodeForbidden {
			t.Fatalf("err = %v, want 403 %s", err, CodeForbidden)
		}
	})

	t.Run("platform superadmin without membership still resolves", func(t *testing.T) {
		// The membership row (not the global role alone) is what grants
		// tenant-wide access: without it the role is kept and the row-level
		// grants decide visibility (an empty list == zero vehicles).
		store := newFakeStore()
		store.seedUser(&UserRecord{ID: 1, CompanyCode: "DEV001", GlobalRole: models.RoleSuperAdmin, IsActive: true})
		svc := newRBACService(store, authSettings(), nil)

		identity, err := svc.resolveIdentity(context.Background(), store.users[1])
		if err != nil {
			t.Fatalf("a missing membership row must not fail a platform admin: %v", err)
		}
		if identity.role != models.RoleSuperAdmin {
			t.Errorf("role = %q, want the global SuperAdmin role", identity.role)
		}
		if identity.allVehicles || identity.platform || len(identity.assigned) != 0 {
			t.Errorf("identity = %+v, want neither tenant-wide nor platform", identity)
		}
	})

	t.Run("authorization backend down", func(t *testing.T) {
		svc := newRBACService(tenantErrStore{newFakeStore()}, authSettings(), nil)
		_, err := svc.resolveIdentity(context.Background(), &UserRecord{
			ID: 9, CompanyCode: "DEV001", GlobalRole: models.RoleOperator, IsActive: true})
		apiErr, ok := err.(*APIError)
		if !ok || apiErr.Status != 503 || apiErr.Code != CodeServiceUnavailable {
			t.Fatalf("err = %v, want 503 %s", err, CodeServiceUnavailable)
		}
	})

	t.Run("grant lookup down", func(t *testing.T) {
		store := newFakeStore()
		store.seedUser(&UserRecord{ID: 9, CompanyCode: "DEV001", GlobalRole: models.RoleOperator, IsActive: true})
		store.seedTenantAccess("DEV001", 9, "")
		svc := newRBACService(grantsErrStore{store}, authSettings(), nil)

		_, err := svc.resolveIdentity(context.Background(), store.users[9])
		apiErr, ok := err.(*APIError)
		if !ok || apiErr.Status != 503 {
			t.Fatalf("err = %v, want 503", err)
		}
	})
}

// tenantErrStore fails the membership lookup.
type tenantErrStore struct{ *fakeStore }

func (tenantErrStore) TenantAccess(context.Context, string, int64) (string, bool, bool, error) {
	return "", false, false, errors.New("postgres down")
}

// grantsErrStore fails the row-level grant lookup.
type grantsErrStore struct{ *fakeStore }

func (grantsErrStore) AssignedVehicleIDs(context.Context, string, int64) ([]int64, error) {
	return nil, errors.New("postgres down")
}

// userErrStore fails the master `tm_users` authority lookup.
type userErrStore struct{ *fakeStore }

func (userErrStore) UserByID(context.Context, int64) (*UserRecord, error) {
	return nil, errors.New("postgres down")
}

// TestAuthenticateMiddleware covers the whole PRD §8.6/FR-5.7 authentication
// flow: bearer parsing, signature/issuer/expiry verification, the Redis
// revocation denylist and the fresh authority + membership resolution.
func TestAuthenticateMiddleware(t *testing.T) {
	settings := authSettings()
	// The denylist is consulted on every request (revocation is enabled), so the
	// suite shares one in-process Redis; the down-KV case gets its own.
	kv, srv := newMiniredisKV(t)
	token := func(jti string) string {
		return signToken(t, settings.JWTIssuer, settings.JWTSecret, validClaims(jti), jwt.SigningMethodHS256)
	}
	// authorisedUser is the identity the valid token belongs to.
	authorisedUser := &UserRecord{ID: 7, Email: "op@test", CompanyCode: "DEV001",
		GlobalRole: models.RoleOperator, IsActive: true}

	run := func(t *testing.T, svc *Service, raw string) (*gin.Context, *httptest.ResponseRecorder) {
		t.Helper()
		c, rec := bearerContext(http.MethodGet, "/api/v1/vehicles", raw, nil)
		svc.authenticate()(c)
		return c, rec
	}

	t.Run("missing bearer token", func(t *testing.T) {
		svc := newRBACService(newFakeStore(), settings, kv)
		c, rec := run(t, svc, "")
		if !c.IsAborted() || rec.Code != http.StatusUnauthorized {
			t.Fatalf("got %d (aborted=%v), want 401", rec.Code, c.IsAborted())
		}
		if code := decodeErr(t, rec).ErrorCode; code != CodeUnauthorized {
			t.Errorf("error_code = %s, want %s", code, CodeUnauthorized)
		}
	})

	t.Run("malformed token", func(t *testing.T) {
		svc := newRBACService(newFakeStore(), settings, kv)
		_, rec := run(t, svc, "not-a-jwt")
		if rec.Code != http.StatusUnauthorized || decodeErr(t, rec).ErrorCode != CodeTokenInvalid {
			t.Fatalf("got %d %s, want 401 %s", rec.Code, decodeErr(t, rec).ErrorCode, CodeTokenInvalid)
		}
	})

	t.Run("token signed with a foreign key", func(t *testing.T) {
		svc := newRBACService(newFakeStore(), settings, kv)
		foreign := signToken(t, settings.JWTIssuer, strings.Repeat("z", 40), validClaims("jti"), jwt.SigningMethodHS256)
		_, rec := run(t, svc, foreign)
		if rec.Code != http.StatusUnauthorized || decodeErr(t, rec).ErrorCode != CodeTokenInvalid {
			t.Fatalf("got %d, want 401 %s", rec.Code, CodeTokenInvalid)
		}
	})

	t.Run("revoked token", func(t *testing.T) {
		store := newFakeStore()
		store.seedUser(authorisedUser)
		store.seedTenantAccess("DEV001", 7, "")
		srv.Set(settings.DenylistPrefix+"jti-logout", "1")

		svc := newRBACService(store, settings, kv)
		c, rec := run(t, svc, token("jti-logout"))
		if !c.IsAborted() || rec.Code != http.StatusUnauthorized {
			t.Fatalf("got %d, want 401", rec.Code)
		}
		if code := decodeErr(t, rec).ErrorCode; code != CodeTokenRevoked {
			t.Errorf("error_code = %s, want %s", code, CodeTokenRevoked)
		}
	})

	t.Run("revocation store unavailable", func(t *testing.T) {
		svc := newRBACService(newFakeStore(), settings, newDownKV(t))
		_, rec := run(t, svc, token("jti"))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("got %d, want 503 (fail-closed, never trust an unverifiable token)", rec.Code)
		}
	})

	t.Run("unknown account", func(t *testing.T) {
		svc := newRBACService(newFakeStore(), settings, kv)
		_, rec := run(t, svc, token("jti"))
		if rec.Code != http.StatusUnauthorized || decodeErr(t, rec).ErrorCode != CodeAccountInactive {
			t.Fatalf("got %d %s, want 401 %s", rec.Code, decodeErr(t, rec).ErrorCode, CodeAccountInactive)
		}
	})

	t.Run("deactivated account", func(t *testing.T) {
		store := newFakeStore()
		inactive := *authorisedUser
		inactive.IsActive = false
		store.seedUser(&inactive)
		svc := newRBACService(store, settings, kv)

		_, rec := run(t, svc, token("jti"))
		if rec.Code != http.StatusUnauthorized || decodeErr(t, rec).ErrorCode != CodeAccountInactive {
			t.Fatalf("got %d %s, want 401 %s", rec.Code, decodeErr(t, rec).ErrorCode, CodeAccountInactive)
		}
	})

	t.Run("authority backend unavailable", func(t *testing.T) {
		svc := newRBACService(userErrStore{newFakeStore()}, settings, kv)
		_, rec := run(t, svc, token("jti"))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("got %d, want 503", rec.Code)
		}
	})

	t.Run("successful authentication", func(t *testing.T) {
		store := newFakeStore()
		store.seedUser(authorisedUser)
		store.seedTenantAccess("DEV001", 7, "")
		store.seedAssignedVehicles(7, 5)
		svc := newRBACService(store, settings, kv)

		c, rec := run(t, svc, token("jti-ok"))
		if rec.Code != http.StatusOK || c.IsAborted() {
			t.Fatalf("valid token rejected: %d (aborted=%v) %s", rec.Code, c.IsAborted(), rec.Body.String())
		}
		identity, ok := currentIdentity(c)
		if !ok || identity.userID != 7 || identity.companyCode != "DEV001" {
			t.Fatalf("identity = %+v (ok=%v), want user 7 in DEV001", identity, ok)
		}
		if !vehicleAllowed(identity, 5) {
			t.Error("the row-level grants must be attached to the identity")
		}
		if claims, ok := currentClaims(c); !ok || claims.ID != "jti-ok" {
			t.Errorf("claims not published on the context: %+v", claims)
		}
	})
}

// guardContext runs one RBAC middleware and reports whether it let the request
// through (PRD §3.1 role matrix).
func guardContext(t *testing.T, middleware gin.HandlerFunc, identity *tenantIdentity, id string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	c, rec := testContext(http.MethodGet, "/api/v1/vehicles", "", identity)
	if id != "" {
		c.Params = gin.Params{{Key: "id", Value: id}}
	}
	middleware(c)
	return c, rec
}

// TestRequireTenantScope: a platform (governance) identity may never read
// tenant fleet data (PRD §3.1).
func TestRequireTenantScope(t *testing.T) {
	svc := newRBACService(newFakeStore(), authSettings(), nil)

	platform := &tenantIdentity{userID: 1, role: models.RoleSuperAdmin, companyCode: models.PlatformCompanyCode, platform: true, allVehicles: true}
	c, rec := guardContext(t, svc.requireTenantScope(), platform, "")
	if !c.IsAborted() || rec.Code != http.StatusForbidden {
		t.Fatalf("platform identity got %d, want 403", rec.Code)
	}
	if code := decodeErr(t, rec).ErrorCode; code != CodePlatformScope {
		t.Errorf("error_code = %s, want %s", code, CodePlatformScope)
	}

	tenant := &tenantIdentity{userID: 9, role: models.RoleOperator, companyCode: "DEV001"}
	c2, _ := guardContext(t, svc.requireTenantScope(), tenant, "")
	if c2.IsAborted() {
		t.Error("a tenant identity must pass the tenant scope guard")
	}
}

// TestRequireWriteMatrix: only Admin and Manager may mutate fleet resources.
func TestRequireWriteMatrix(t *testing.T) {
	svc := newRBACService(newFakeStore(), authSettings(), nil)

	for _, role := range []string{models.RoleAdmin, models.RoleManager} {
		c, _ := guardContext(t, svc.requireWrite(), &tenantIdentity{userID: 9, role: role, companyCode: "DEV001"}, "")
		if c.IsAborted() {
			t.Errorf("%s must be allowed to write", role)
		}
	}
	for _, role := range []string{models.RoleOperator, models.RoleDriver} {
		c, rec := guardContext(t, svc.requireWrite(), &tenantIdentity{userID: 9, role: role, companyCode: "DEV001"}, "")
		if !c.IsAborted() || rec.Code != http.StatusForbidden {
			t.Errorf("%s got %d, want 403", role, rec.Code)
		}
	}
	// A request without identity can only be a wiring bug: 401, never 500.
	c, rec := guardContext(t, svc.requireWrite(), nil, "")
	if !c.IsAborted() || rec.Code != http.StatusUnauthorized {
		t.Errorf("missing identity got %d, want 401", rec.Code)
	}
}

// TestRequirePasswordRotated pins the FR-5.5 default-password veil.
func TestRequirePasswordRotated(t *testing.T) {
	svc := newRBACService(newFakeStore(), authSettings(), nil)

	pending := &tenantIdentity{userID: 9, role: models.RoleOperator, companyCode: "DEV001", mustChangePassword: true}
	c, rec := guardContext(t, svc.requirePasswordRotated(), pending, "")
	if !c.IsAborted() || rec.Code != http.StatusForbidden {
		t.Fatalf("got %d, want 403", rec.Code)
	}
	if code := decodeErr(t, rec).ErrorCode; code != CodePasswordChangeNeeded {
		t.Errorf("error_code = %s, want %s", code, CodePasswordChangeNeeded)
	}

	rotated := &tenantIdentity{userID: 9, role: models.RoleOperator, companyCode: "DEV001"}
	c2, _ := guardContext(t, svc.requirePasswordRotated(), rotated, "")
	if c2.IsAborted() {
		t.Error("a rotated password must pass the guard")
	}

	c3, rec3 := guardContext(t, svc.requirePasswordRotated(), nil, "")
	if !c3.IsAborted() || rec3.Code != http.StatusUnauthorized {
		t.Errorf("missing identity got %d, want 401", rec3.Code)
	}
}

// TestVehicleAllowedMatrix documents the row-level rule: tenant-wide roles see
// everything, an empty grant list means ZERO vehicles, never "all".
func TestVehicleAllowedMatrix(t *testing.T) {
	if !vehicleAllowed(&tenantIdentity{allVehicles: true}, 42) {
		t.Error("allVehicles must grant every row")
	}
	if !vehicleAllowed(&tenantIdentity{assigned: []int64{7, 42}}, 42) {
		t.Error("a granted vehicle must be allowed")
	}
	if vehicleAllowed(&tenantIdentity{assigned: []int64{7}}, 42) {
		t.Error("an ungranted vehicle must be denied")
	}
	if vehicleAllowed(&tenantIdentity{}, 42) {
		t.Error("an empty grant list must deny every row")
	}
}

// TestRequireVehicleAccessBadID: the guard validates the path id BEFORE the
// grant check (400 instead of a misleading 403).
func TestRequireVehicleAccessBadID(t *testing.T) {
	svc := newRBACService(newFakeStore(), authSettings(), nil)
	admin := adminIdentity()

	c, rec := guardContext(t, svc.requireVehicleAccess(), admin, "abc")
	if !c.IsAborted() || rec.Code != http.StatusBadRequest {
		t.Errorf("non-numeric id got %d, want 400", rec.Code)
	}

	c2, _ := guardContext(t, svc.requireVehicleAccess(), admin, "42")
	if c2.IsAborted() {
		t.Error("an Admin must be allowed to reach any vehicle of the tenant")
	}
}
