package controllers

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestRegisterMetrics: a nil registry is a no-op and a real registry receives
// every api-vehicle collector (PRD §10.1).
func TestRegisterMetrics(t *testing.T) {
	RegisterMetrics(nil) // must not panic (metrics disabled)

	reg := prometheus.NewRegistry()
	RegisterMetrics(reg)

	// Touch each collector so a family is actually gathered.
	rbacDenied.WithLabelValues("registration_probe").Inc()
	httpErrors.WithLabelValues("500", CodeInternalError).Inc()
	liveStateErrors.Inc()

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	present := map[string]bool{}
	for _, f := range families {
		present[f.GetName()] = true
	}
	for _, want := range []string{"api_vehicle_rbac_denied_total", "http_errors_total", "live_state_read_errors_total"} {
		if !present[want] {
			t.Errorf("collector %s was not registered", want)
		}
	}
}

// rateLimitService wires a Service with an explicit rate-limit window.
func rateLimitService(kv *RedisKV, limit int, window time.Duration) *Service {
	settings := testSettings()
	settings.APIRateLimit = limit
	settings.APIRateWindow = window
	return newRBACService(newFakeStore(), settings, kv)
}

// TestAPIRateLimitDisabled: a disabled limit (PRD §8.4 knob) short-circuits
// before Redis is touched.
func TestAPIRateLimitDisabled(t *testing.T) {
	kv, srv := newMiniredisKV(t)

	for _, tc := range []struct {
		name   string
		limit  int
		window time.Duration
	}{
		{"zero limit", 0, time.Minute},
		{"zero window", 100, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := rateLimitService(kv, tc.limit, tc.window)
			c, _ := testContext(http.MethodGet, "/api/v1/vehicles", "", adminIdentity())

			svc.apiRateLimitMiddleware()(c)

			if c.IsAborted() {
				t.Fatal("the request must pass when the limiter is disabled")
			}
			if keys := srv.Keys(); len(keys) != 0 {
				t.Errorf("redis touched while disabled: %v", keys)
			}
		})
	}
}

// TestAPIRateLimitAllowThenDeny covers the fixed-window counter: requests up to
// the limit pass, the next one is a 429 RATE_LIMITED (PRD §8.4).
func TestAPIRateLimitAllowThenDeny(t *testing.T) {
	kv, srv := newMiniredisKV(t)
	svc := rateLimitService(kv, 2, time.Minute)

	for i := 1; i <= 2; i++ {
		c, _ := testContext(http.MethodGet, "/api/v1/vehicles", "", adminIdentity())
		svc.apiRateLimitMiddleware()(c)
		if c.IsAborted() {
			t.Fatalf("request %d must pass (limit 2)", i)
		}
	}

	c, rec := testContext(http.MethodGet, "/api/v1/vehicles", "", adminIdentity())
	svc.apiRateLimitMiddleware()(c)
	if !c.IsAborted() || rec.Code != http.StatusTooManyRequests {
		t.Fatalf("third request got %d, want 429", rec.Code)
	}
	if code := decodeErr(t, rec).ErrorCode; code != CodeRateLimited {
		t.Errorf("error_code = %s, want %s", code, CodeRateLimited)
	}

	// The window is armed on the first hit so the counter cannot leak forever.
	key := "adatrack_gps:auth:api:" + c.ClientIP()
	if ttl := srv.TTL(key); ttl <= 0 || ttl > time.Minute {
		t.Errorf("rate-limit key ttl = %v, want (0,1m]", ttl)
	}
}

// TestAPIRateLimitKeyedByUser: an authenticated request is limited per user id,
// an anonymous one per client IP (PRD §8.4 "100 requests / minute / user").
func TestAPIRateLimitKeyedByUser(t *testing.T) {
	kv, srv := newMiniredisKV(t)
	svc := rateLimitService(kv, 10, time.Minute)

	claims := validClaims("jti-rate")
	c, _ := testContext(http.MethodGet, "/api/v1/vehicles", "", adminIdentity())
	c.Set(ctxClaims, &claims)
	svc.apiRateLimitMiddleware()(c)

	if !srv.Exists("adatrack_gps:auth:api:user:7") {
		t.Errorf("per-user key missing; keys = %v", srv.Keys())
	}

	c2, _ := testContext(http.MethodGet, "/api/v1/vehicles", "", adminIdentity())
	svc.apiRateLimitMiddleware()(c2)
	if !srv.Exists("adatrack_gps:auth:api:" + c2.ClientIP()) {
		t.Errorf("per-IP key missing for an anonymous request; keys = %v", srv.Keys())
	}
}

// TestAPIRateLimitUnavailable: a Redis outage fails CLOSED with 503 instead of
// silently letting traffic through.
func TestAPIRateLimitUnavailable(t *testing.T) {
	svc := rateLimitService(newDownKV(t), 10, time.Minute)

	c, rec := testContext(http.MethodGet, "/api/v1/vehicles", "", adminIdentity())
	svc.apiRateLimitMiddleware()(c)
	if !c.IsAborted() || rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want 503", rec.Code)
	}
	if code := decodeErr(t, rec).ErrorCode; code != CodeServiceUnavailable {
		t.Errorf("error_code = %s, want %s", code, CodeServiceUnavailable)
	}
}

// TestDenyRequestCountsAndEnvelope asserts the RBAC denial path both answers the
// contract envelope and feeds the `api_vehicle_rbac_denied_total` counter.
func TestDenyRequestCountsAndEnvelope(t *testing.T) {
	svc := newRBACService(newFakeStore(), authSettings(), nil)
	before := testutil.ToFloat64(rbacDenied.WithLabelValues("unit_denial"))

	c, rec := testContext(http.MethodGet, "/api/v1/vehicles", "", adminIdentity())
	svc.denyRequest(c, nil, errForbidden(CodeForbidden, "no"), "unit_denial")

	if !c.IsAborted() || rec.Code != http.StatusForbidden {
		t.Fatalf("got %d (aborted=%v), want 403", rec.Code, c.IsAborted())
	}
	if code := decodeErr(t, rec).ErrorCode; code != CodeForbidden {
		t.Errorf("error_code = %s, want %s", code, CodeForbidden)
	}
	if after := testutil.ToFloat64(rbacDenied.WithLabelValues("unit_denial")); after != before+1 {
		t.Errorf("rbac_denied counter = %v, want %v", after, before+1)
	}
}

// TestCountHTTPError feeds the shared http_errors_total counter.
func TestCountHTTPError(t *testing.T) {
	svc := newRBACService(newFakeStore(), authSettings(), nil)
	counter := httpErrors.WithLabelValues(strconv.Itoa(http.StatusNotFound), CodeEndpointNotFound)
	before := testutil.ToFloat64(counter)

	svc.countHTTPError(http.StatusNotFound, CodeEndpointNotFound)

	if after := testutil.ToFloat64(counter); after != before+1 {
		t.Errorf("http_errors_total = %v, want %v", after, before+1)
	}
}

// TestCurrentIdentityAndClaims documents the context accessors: absent or
// wrongly-typed values are never fatal.
func TestCurrentIdentityAndClaims(t *testing.T) {
	c, _ := testContext(http.MethodGet, "/api/v1/vehicles", "", nil)
	if _, ok := currentIdentity(c); ok {
		t.Error("currentIdentity must report ok=false when nothing was resolved")
	}
	if _, ok := currentClaims(c); ok {
		t.Error("currentClaims must report ok=false when nothing was verified")
	}

	c.Set(ctxIdentity, "not-an-identity")
	c.Set(ctxClaims, 42)
	if _, ok := currentIdentity(c); ok {
		t.Error("a non-identity value must not be accepted")
	}
	if _, ok := currentClaims(c); ok {
		t.Error("a non-claims value must not be accepted")
	}

	identity := adminIdentity()
	claims := validClaims("jti-ctx")
	c.Set(ctxIdentity, identity)
	c.Set(ctxClaims, &claims)
	got, ok := currentIdentity(c)
	if !ok || got != identity {
		t.Errorf("currentIdentity = (%+v,%v), want the stored identity", got, ok)
	}
	gotClaims, ok := currentClaims(c)
	if !ok || gotClaims.ID != "jti-ctx" {
		t.Errorf("currentClaims = (%+v,%v), want jti-ctx", gotClaims, ok)
	}
}

// TestDenyRequestNilErrorIsInternal: a wiring bug (nil error) must still answer
// a contract-compliant 500, never panic.
func TestDenyRequestNilErrorIsInternal(t *testing.T) {
	svc := newRBACService(newFakeStore(), authSettings(), nil)
	c, rec := testContext(http.MethodGet, "/api/v1/vehicles", "", adminIdentity())

	svc.denyRequest(c, nil, nil, "nil_error")

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("got %d, want 500", rec.Code)
	}
	if code := decodeErr(t, rec).ErrorCode; code != CodeInternalError {
		t.Errorf("error_code = %s, want %s", code, CodeInternalError)
	}
}
