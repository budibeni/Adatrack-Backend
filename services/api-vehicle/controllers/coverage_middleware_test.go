package controllers

// coverage_middleware_test.go (B4 coverage api-vehicle 2026-09-09):
// CORS, rate limiting middleware & small helpers — tanpa infra nyata.

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ajb_gps/api-vehicle/models"
	"ajb_gps/internal"

	"github.com/gin-gonic/gin"
)

func init() { gin.SetMode(gin.TestMode) }

func setCORS(cfgs ...[]string) func() {
	old := appCfg
	appCfg = newTestCfg()
	if len(cfgs) > 0 {
		appCfg.HTTP.CORSOrigins = cfgs[0]
	}
	return func() { appCfg = old }
}

func TestOriginAllowed(t *testing.T) {
	cleanup := setCORS([]string{"http://localhost", "https://app.adatrack.dev"})
	defer cleanup()

	if originAllowed("") {
		t.Fatal("empty origin must be disallowed")
	}
	if !originAllowed("http://localhost") {
		t.Fatal("exact match must be allowed")
	}
	if originAllowed("http://evil.example") {
		t.Fatal("non-listed origin must be disallowed")
	}
}

func TestOriginAllowedEmptyList(t *testing.T) {
	cleanup := setCORS(nil) // default from newTestCfg → empty
	defer cleanup()
	if originAllowed("http://x") {
		t.Fatal("empty allowlist must disallow")
	}
}

func TestOriginAllowedWildcard(t *testing.T) {
	cleanup := setCORS([]string{"*", "http://localhost"})
	defer cleanup()
	if !originAllowed("http://anything.example") {
		t.Fatal("wildcard first must allow anything")
	}
}

func TestCorsMiddlewareSetsHeader(t *testing.T) {
	cleanup := setCORS([]string{"http://localhost"})
	defer cleanup()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.Header.Set("Origin", "http://localhost")

	corsMiddleware()(c)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost" {
		t.Fatalf("ACAO = %q, want http://localhost", got)
	}
	if rec.Header().Get("Vary") != "Origin" {
		t.Fatalf("Vary = %q", rec.Header().Get("Vary"))
	}
}

func TestCorsMiddlewareWildcardDev(t *testing.T) {
	cleanup := setCORS([]string{"*"})
	defer cleanup()

	// Empty Origin → originAllowed false → fallback branch sets "*".
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.Header.Set("Origin", "")

	corsMiddleware()(c)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("ACAO = %q, want *", got)
	}
}

func TestCorsMiddlewarePreflight(t *testing.T) {
	cleanup := setCORS([]string{"http://localhost"})
	defer cleanup()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodOptions, "/api/v1/vehicles", nil)
	c.Request.Header.Set("Origin", "http://localhost")

	corsMiddleware()(c)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight code = %d, want 204", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Fatal("expected Allow-Methods on preflight")
	}
	if rec.Header().Get("Access-Control-Max-Age") == "" {
		t.Fatal("expected Max-Age on preflight")
	}
}

func TestCorsMiddlewareDisallowedContinues(t *testing.T) {
	cleanup := setCORS([]string{"http://localhost"})
	defer cleanup()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.Header.Set("Origin", "http://evil.example")

	corsMiddleware()(c)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("unexpected ACAO = %q", got)
	}
}

func TestAPIRateLimiter(t *testing.T) {
	l := newAPIRateLimiter(2)
	if !l.allow("k") || !l.allow("k") || l.allow("k") {
		t.Fatal("limiter should allow 2 then block")
	}
	// Window expiry → new bucket
	l.buckets["k"] = &apiRateBucket{windowStart: time.Now().Add(-2 * time.Minute), count: 99}
	if !l.allow("k") {
		t.Fatal("expired bucket should reset")
	}
}

func TestAPIRateLimitMiddlewareUserKey(t *testing.T) {
	apiLimiter = newAPIRateLimiter(1)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/vehicles", nil)
	c.Request.RemoteAddr = "192.0.2.1:1234"

	apiRateLimitMiddleware()(c) // IP key, consumes
	apiRateLimitMiddleware()(c) // over limit

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("rate-limited code = %d, want 429", rec.Code)
	}
}

func zeroAuthUser() models.AuthUser {
	return models.AuthUser{}
}

func TestAPIRateLimitMiddlewareAuthUser(t *testing.T) {
	apiLimiter = newAPIRateLimiter(1)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/vehicles", nil)
	c.Request.RemoteAddr = "192.0.2.1:1234"
	c.Set(ctxUserKey, zeroAuthUser())

	apiRateLimitMiddleware()(c) // consumes bucket for user 0

	if c.IsAborted() {
		t.Fatal("first request must not abort")
	}

	apiRateLimitMiddleware()(c) // over limit for user 0

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request code = %d, want 429", rec.Code)
	}
}

func TestHTTPMetricsMiddleware(t *testing.T) {
	var _ = internal.HTTPRequestsTotal
	_ = internal.HTTPRequestDuration
	// Both metric vecs are registered by b4_mock init; calling the middleware
	// on an empty FullPath exercises the "unknown" label branch.
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	httpMetricsMiddleware()(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics middleware code = %d", rec.Code)
	}
}