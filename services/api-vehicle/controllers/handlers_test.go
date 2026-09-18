package controllers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"adatrack_gps/api-vehicle/models"
)

// newTestService wires a Service with the fake store (auth is unused when the
// handlers are invoked directly; the rate limiter is disabled).
func newTestService(store *fakeStore) *Service {
	return newTestServiceWithLive(store, nil)
}

// newTestServiceWithLive additionally injects the live-state overlay source
// (a *RedisKV in production, a stub in the live-state tests).
func newTestServiceWithLive(store *fakeStore, live LiveStateReader) *Service {
	return NewService(Deps{
		Settings: Settings{
			HTTPAddr:        ":0",
			JWTSecret:       strings.Repeat("t", 40),
			JWTIssuer:       "test",
			DefaultPageSize: 100,
			MaxPageSize:     1000,
		},
		Store: store,
		Live:  live,
	})
}

// testContext builds a request context with an identity attached.
func testContext(method, target, body string, identity *tenantIdentity) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	c.Request = req
	if identity != nil {
		c.Set(ctxIdentity, identity)
	}
	return c, rec
}

// operatorIdentity builds a row-level restricted identity.
func operatorIdentity(assigned ...int64) *tenantIdentity {
	return &tenantIdentity{userID: 9, email: "op@test", role: models.RoleOperator,
		companyCode: "DEV001", assigned: assigned}
}

func adminIdentity() *tenantIdentity {
	return &tenantIdentity{userID: 1, email: "admin@test", role: models.RoleAdmin,
		companyCode: "DEV001", allVehicles: true}
}

// TestRequireVehicleAccessRowLevelDenied: a vehicle the operator is not
// assigned to is 403 UNAUTHORIZED_VEHICLE (PRD §3.1 row-level RBAC).
func TestRequireVehicleAccessRowLevelDenied(t *testing.T) {
	svc := newTestService(newFakeStore())
	c, rec := testContext(http.MethodGet, "/api/v1/vehicles/1", "",
		operatorIdentity(2, 3))
	c.Params = gin.Params{{Key: "id", Value: "1"}}

	svc.requireVehicleAccess()(c)

	if !c.IsAborted() {
		t.Fatal("request must be aborted for a non-assigned vehicle")
	}
	var body models.ErrorEnvelope
	if err := parseJSON(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if body.ErrorCode != CodeUnauthorizedVehicle || rec.Code != http.StatusForbidden {
		t.Errorf("got %d %s, want 403 UNAUTHORIZED_VEHICLE", rec.Code, body.ErrorCode)
	}
}

// TestRequireAdminGuard: soft delete is Admin-only (Manager → 403).
func TestRequireAdminGuard(t *testing.T) {
	svc := newTestService(newFakeStore())
	manager := &tenantIdentity{userID: 2, role: models.RoleManager, companyCode: "DEV001", allVehicles: true}
	c, rec := testContext(http.MethodDelete, "/api/v1/vehicles/1", "", manager)

	svc.requireAdmin()(c)

	if !c.IsAborted() || rec.Code != http.StatusForbidden {
		t.Errorf("Manager delete must be 403, got %d (aborted=%v)", rec.Code, c.IsAborted())
	}

	admin := adminIdentity()
	c2, _ := testContext(http.MethodDelete, "/api/v1/vehicles/1", "", admin)
	svc.requireAdmin()(c2)
	if c2.IsAborted() {
		t.Error("Admin delete must pass the guard")
	}
}
