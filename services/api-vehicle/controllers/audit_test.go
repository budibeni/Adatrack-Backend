package controllers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"adatrack_gps/api-vehicle/models"
)

// --- fake audit persistence (B11) -----------------------------------------

func (f *fakeStore) WriteAudit(_ context.Context, rows []AuditRow) error {
	f.auditRows = append(f.auditRows, rows...)
	return nil
}

func (f *fakeStore) ListAuditLogs(_ context.Context, q AuditLogQuery) ([]models.AuditLog, int64, error) {
	f.auditListQuery = q
	return f.auditList, f.auditListTotal, nil
}

// auditEngine mounts the mutation-audit middleware with a fixed identity, so the
// middleware contract is testable without the full auth stack.
func auditEngine(svc *Service, engineIdentity *tenantIdentity) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	// requestIDMiddleware mirrors the production chain so the correlation id is
	// available to the audit row (§9.4).
	engine.Use(requestIDMiddleware())
	group := engine.Group("/api/v1", func(c *gin.Context) {
		if engineIdentity != nil {
			c.Set(ctxIdentity, engineIdentity)
		}
	})
	group.Use(svc.auditMutationMiddleware())
	ok := func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) }
	created := func(c *gin.Context) { c.JSON(http.StatusCreated, gin.H{"ok": true}) }
	group.POST("/vehicles", created)
	group.GET("/vehicles", ok)
	group.PATCH("/vehicles/:id", ok)
	group.DELETE("/vehicles/:id", ok)
	group.POST("/vehicles/:id/restore", ok)
	group.POST("/vehicles/:id/commands", created)
	group.POST("/alerts/:id/acknowledge", ok)
	group.PATCH("/access/menu/role/:role", ok)
	return engine
}

func auditRequest(engine *gin.Engine, method, target, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, target, http.NoBody)
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("User-Agent", "audit-test")
	req.Header.Set("X-Request-ID", "req-audit-1")
	engine.ServeHTTP(rec, req)
	return rec
}

// TestAuditActionMapping pins the action/entity derivation (PRD §9.4) so a new
// route cannot silently land in the wrong audit bucket.
func TestAuditActionMapping(t *testing.T) {
	cases := []struct {
		method, route, action, entity string
	}{
		{http.MethodPost, "/api/v1/vehicles", ActionEntityCreated, "VEHICLE"},
		{http.MethodPatch, "/api/v1/vehicles/:id", ActionEntityUpdated, "VEHICLE"},
		{http.MethodDelete, "/api/v1/vehicles/:id", ActionEntitySoftDeleted, "VEHICLE"},
		{http.MethodPost, "/api/v1/vehicles/:id/restore", ActionEntityRestored, "VEHICLE"},
		{http.MethodPost, "/api/v1/alerts/:id/acknowledge", ActionAlertAcknowledged, "ALERT"},
		{http.MethodPost, "/api/v1/alerts/:id/resolve", ActionAlertResolved, "ALERT"},
		{http.MethodPost, "/api/v1/vehicles/:id/commands", ActionCommandRequested, "VEHICLE"},
		{http.MethodPatch, "/api/v1/access/menu/role/Admin", ActionMenuAccessUpdated, "ACCESS"},
		{http.MethodPost, "/api/v1/fuel-configs", ActionEntityCreated, "FUEL_CONFIG"},
		{http.MethodDelete, "/api/v1/speed-configs/:id", ActionEntitySoftDeleted, "SPEED_CONFIG"},
		{http.MethodGet, "/api/v1/vehicles", "", ""},
	}
	for _, tc := range cases {
		if got := auditActionFor(tc.method, tc.route); got != tc.action {
			t.Errorf("auditActionFor(%s %s) = %q, want %q", tc.method, tc.route, got, tc.action)
		}
		if tc.entity != "" {
			if got := auditEntityFor(tc.route); got != tc.entity {
				t.Errorf("auditEntityFor(%s) = %q, want %q", tc.route, got, tc.entity)
			}
		}
	}
}

// TestAuditMiddlewareWritesEveryMutation proves the B11 acceptance criterion:
// every mutation (create/update/delete/restore/ack/command) writes an audit row
// with the resolved actor, tenant, entity id and correlation id, while a GET
// writes nothing.
func TestAuditMiddlewareWritesEveryMutation(t *testing.T) {
	store := newFakeStore()
	svc := NewService(Deps{
		Settings: authSettings(),
		Store:    store,
		Auditor:  NewAuditor(store, nil, true),
	})
	engine := auditEngine(svc, adminIdentity())

	auditRequest(engine, http.MethodPost, "/api/v1/vehicles", `{"plate_number":"B 1234 XY"}`)
	auditRequest(engine, http.MethodPatch, "/api/v1/vehicles/7", `{"plate_number":"B 9999 ZZ"}`)
	auditRequest(engine, http.MethodDelete, "/api/v1/vehicles/7", `{"reason":"unit scrapped"}`)
	auditRequest(engine, http.MethodPost, "/api/v1/vehicles/7/restore", "")
	auditRequest(engine, http.MethodPost, "/api/v1/alerts/3/acknowledge", "")
	auditRequest(engine, http.MethodPost, "/api/v1/vehicles/7/commands", `{"command":"DYD#"}`)
	// A read must never be audited by this middleware.
	auditRequest(engine, http.MethodGet, "/api/v1/vehicles", "")

	if len(store.auditRows) != 6 {
		t.Fatalf("audit rows = %d, want 6 (one per mutation, none for GET)", len(store.auditRows))
	}
	byAction := map[string]AuditRow{}
	for _, row := range store.auditRows {
		byAction[row.Action] = row
		if row.ActorUserID != 1 || row.CompanyCode != "DEV001" || row.ActorRole != models.RoleAdmin {
			t.Errorf("actor not resolved on %s: %+v", row.Action, row)
		}
		if row.RequestID != "req-audit-1" {
			t.Errorf("request_id = %q, want correlation id", row.RequestID)
		}
		if row.Outcome != OutcomeSuccess {
			t.Errorf("%s outcome = %q, want success", row.Action, row.Outcome)
		}
	}
	for _, action := range []string{ActionEntityCreated, ActionEntityUpdated, ActionEntitySoftDeleted,
		ActionEntityRestored, ActionAlertAcknowledged, ActionCommandRequested} {
		if _, ok := byAction[action]; !ok {
			t.Errorf("missing audit action %q", action)
		}
	}
	if id := byAction[ActionEntityUpdated].EntityID; id != "7" {
		t.Errorf("entity_id = %q, want 7", id)
	}
	if reason := byAction[ActionEntitySoftDeleted].Reason; reason != "unit scrapped" {
		t.Errorf("delete reason = %q, want the request body reason", reason)
	}
}

// TestAuditMiddlewareRedactsSensitivePayload proves secrets never reach the trail.
func TestAuditMiddlewareRedactsSensitivePayload(t *testing.T) {
	store := newFakeStore()
	svc := NewService(Deps{Settings: authSettings(), Store: store, Auditor: NewAuditor(store, nil, true)})
	engine := auditEngine(svc, adminIdentity())

	auditRequest(engine, http.MethodPost, "/api/v1/vehicles",
		`{"plate_number":"B 1","api_key":"super-secret","nested":{"password":"p@ss"}}`)

	if len(store.auditRows) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(store.auditRows))
	}
	state, ok := store.auditRows[0].AfterState.(map[string]any)
	if !ok {
		t.Fatalf("after_state = %#v, want an object", store.auditRows[0].AfterState)
	}
	if state["api_key"] != "[REDACTED]" {
		t.Errorf("api_key = %v, want [REDACTED]", state["api_key"])
	}
	nested, ok := state["nested"].(map[string]any)
	if !ok || nested["password"] != "[REDACTED]" {
		t.Errorf("nested password not redacted: %#v", state["nested"])
	}
}

// TestAuditDisabledWritesNothing: AUDIT_ENABLED=false disables the writer.
func TestAuditDisabledWritesNothing(t *testing.T) {
	store := newFakeStore()
	svc := NewService(Deps{Settings: authSettings(), Store: store, Auditor: NewAuditor(store, nil, false)})
	engine := auditEngine(svc, adminIdentity())

	auditRequest(engine, http.MethodPost, "/api/v1/vehicles", `{"plate_number":"B 1"}`)
	if len(store.auditRows) != 0 {
		t.Fatalf("audit rows = %d, want 0 when audit is disabled", len(store.auditRows))
	}
}

// TestDenialIsAudited proves an RBAC denial writes ACCESS_DENIED (PRD §9.4:
// 100% security events recorded).
func TestDenialIsAudited(t *testing.T) {
	store := newFakeStore()
	svc := NewService(Deps{Settings: authSettings(), Store: store, Auditor: NewAuditor(store, nil, true)})

	c, rec := testContext(http.MethodGet, "/api/v1/audit-logs", "", adminIdentity())
	svc.denyRequest(c, &Claims{UserID: 1, Email: "admin@test", Role: models.RoleAdmin},
		errForbidden(CodeForbidden, "nope"), "admin_role")

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if len(store.auditRows) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(store.auditRows))
	}
	row := store.auditRows[0]
	if row.Action != ActionAccessDenied || row.Outcome != OutcomeDenied {
		t.Errorf("row = %+v, want ACCESS_DENIED/denied", row)
	}
	if row.Reason != "admin_role" || row.CompanyCode != "DEV001" {
		t.Errorf("row = %+v, want the denial reason + tenant", row)
	}
	if row.AfterState == nil {
		t.Error("denial audit must record the error_code for investigation")
	}
}
