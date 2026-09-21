package controllers

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
)

// TestRouterRouteTable pins the PRD §8.2 route surface: the operational
// endpoints stay unauthenticated and every fleet resource is mounted under
// /api/v1.
func TestRouterRouteTable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newRBACService(newFakeStore(), testSettings(), nil)

	mounted := map[string]bool{}
	for _, r := range svc.Handler().Routes() {
		mounted[r.Method+" "+r.Path] = true
	}

	want := []string{
		"GET /healthz", "GET /livez",
		"GET /api/v1/vehicles", "POST /api/v1/vehicles",
		"GET /api/v1/vehicles/:id", "PATCH /api/v1/vehicles/:id",
		"DELETE /api/v1/vehicles/:id", "POST /api/v1/vehicles/:id/restore",
		"GET /api/v1/vehicles/:id/fuel/history",
		"GET /api/v1/geofences", "POST /api/v1/geofences",
		"GET /api/v1/geofences/:id", "PATCH /api/v1/geofences/:id",
		"DELETE /api/v1/geofences/:id", "POST /api/v1/geofences/:id/restore",
		"GET /api/v1/routes", "POST /api/v1/routes",
		"GET /api/v1/routes/:id", "PATCH /api/v1/routes/:id",
		"DELETE /api/v1/routes/:id", "POST /api/v1/routes/:id/restore",
		"GET /api/v1/routes/:id/assignments", "POST /api/v1/routes/:id/assignments",
		"PATCH /api/v1/routes/:id/assignments/:assignmentId",
		"DELETE /api/v1/routes/:id/assignments/:assignmentId",
		"GET /api/v1/speed-configs", "POST /api/v1/speed-configs",
		"GET /api/v1/speed-configs/:id", "PATCH /api/v1/speed-configs/:id",
		"DELETE /api/v1/speed-configs/:id", "POST /api/v1/speed-configs/:id/restore",
		"GET /api/v1/fuel-configs", "POST /api/v1/fuel-configs",
		"GET /api/v1/fuel-configs/:id", "PATCH /api/v1/fuel-configs/:id",
		"DELETE /api/v1/fuel-configs/:id", "POST /api/v1/fuel-configs/:id/restore",
		"GET /api/v1/alerts",
		"POST /api/v1/alerts/:id/acknowledge", "POST /api/v1/alerts/:id/resolve",
	}
	for _, w := range want {
		if !mounted[w] {
			t.Errorf("route %q is not mounted", w)
		}
	}
}

// TestRouterNotFoundAndMethodNotAllowed covers the 404/405 fallbacks, which must
// still answer the PRD §8.1 envelope.
func TestRouterNotFoundAndMethodNotAllowed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newRBACService(newFakeStore(), testSettings(), nil)
	engine := svc.Handler()

	rec := serve(engine, http.MethodGet, "/api/v1/nope", "", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown route = %d, want 404", rec.Code)
	}
	if code := decodeErr(t, rec).ErrorCode; code != CodeEndpointNotFound {
		t.Errorf("error_code = %s, want %s", code, CodeEndpointNotFound)
	}

	rec2 := serve(engine, http.MethodPut, "/api/v1/vehicles", "{}", nil)
	if rec2.Code != http.StatusMethodNotAllowed {
		t.Fatalf("unsupported method = %d, want 405", rec2.Code)
	}
	if code := decodeErr(t, rec2).ErrorCode; code != CodeMethodNotAllowed {
		t.Errorf("error_code = %s, want %s", code, CodeMethodNotAllowed)
	}
}

// TestRouterLivez is the liveness probe: no dependency is consulted.
func TestRouterLivez(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newRBACService(newFakeStore(), testSettings(), nil)

	rec := serve(svc.Handler(), http.MethodGet, "/livez", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("livez = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Errorf("livez body = %s", rec.Body.String())
	}
}

// TestRouterUnauthenticatedFleetAccess: the JWT middleware protects every fleet
// route and the correlation id is always echoed (PRD §9.4).
func TestRouterUnauthenticatedFleetAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newRBACService(newFakeStore(), testSettings(), newDownKV(t))

	rec := serve(svc.Handler(), http.MethodGet, "/api/v1/vehicles", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401 without a bearer token", rec.Code)
	}
	if code := decodeErr(t, rec).ErrorCode; code != CodeUnauthorized {
		t.Errorf("error_code = %s, want %s", code, CodeUnauthorized)
	}
	if rec.Header().Get("X-Request-ID") == "" {
		t.Error("the request-id middleware must run before the auth layer")
	}
}

// TestHealthzWithoutDependencies reports readiness even when no backend is
// wired (useful in unit tests): status ok and no checks.
func TestHealthzWithoutDependencies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newRBACService(newFakeStore(), testSettings(), nil)

	rec := serve(svc.Handler(), http.MethodGet, "/healthz", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Errorf("healthz body = %s", rec.Body.String())
	}
}

// TestHealthzRedisChecks: Redis backs the rate limiter and the revocation
// denylist, so an outage must degrade the probe (PRD §10.2).
func TestHealthzRedisChecks(t *testing.T) {
	gin.SetMode(gin.TestMode)

	kv, _ := newMiniredisKV(t)
	healthy := newRBACService(newFakeStore(), testSettings(), kv)
	rec := serve(healthy.Handler(), http.MethodGet, "/healthz", "", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"redis":"ok"`) {
		t.Fatalf("healthy redis: %d %s", rec.Code, rec.Body.String())
	}

	down := newRBACService(newFakeStore(), testSettings(), newDownKV(t))
	rec2 := serve(down.Handler(), http.MethodGet, "/healthz", "", nil)
	if rec2.Code != http.StatusServiceUnavailable {
		t.Fatalf("degraded healthz = %d, want 503", rec2.Code)
	}
	body := rec2.Body.String()
	if !strings.Contains(body, `"status":"degraded"`) || !strings.Contains(body, "error:") {
		t.Errorf("degraded body = %s, want status degraded + the error string", body)
	}
}

// TestMetricsEndpoint: /metrics is only served when a registry was injected
// (PRD §14.2) and it exposes the registered collectors.
func TestMetricsEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)

	off := newRBACService(newFakeStore(), testSettings(), nil)
	if rec := serve(off.Handler(), http.MethodGet, "/metrics", "", nil); rec.Code != http.StatusNotFound {
		t.Errorf("metrics without a registry = %d, want 404", rec.Code)
	}

	reg := prometheus.NewRegistry()
	RegisterMetrics(reg)
	on := NewService(Deps{Settings: testSettings(), Store: newFakeStore(), Registry: reg})
	rec := serve(on.Handler(), http.MethodGet, "/metrics", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "http_errors_total") {
		t.Errorf("metrics body does not expose http_errors_total: %s", rec.Body.String())
	}
}
