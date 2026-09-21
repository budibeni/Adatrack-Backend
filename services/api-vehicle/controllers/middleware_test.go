package controllers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// newEngine builds a bare gin engine carrying the middleware under test.
func newEngine(middleware ...gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(middleware...)
	return engine
}

// serve runs one request through an engine.
func serve(engine *gin.Engine, method, target, body string, headers map[string]string) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

// TestRequestIDMiddleware: an inbound X-Request-ID is propagated verbatim, while
// a missing/blank/oversized one is replaced by a generated correlation id so the
// logs always carry one (PRD §9.4).
func TestRequestIDMiddleware(t *testing.T) {
	engine := newEngine(requestIDMiddleware())
	var seen string
	engine.GET("/probe", func(c *gin.Context) {
		seen = requestID(c)
		c.Status(http.StatusOK)
	})

	t.Run("propagated verbatim", func(t *testing.T) {
		seen = ""
		rec := serve(engine, http.MethodGet, "/probe", "", map[string]string{"X-Request-ID": "trace-123"})
		if seen != "trace-123" || rec.Header().Get("X-Request-ID") != "trace-123" {
			t.Fatalf("request id = %q (header %q), want trace-123", seen, rec.Header().Get("X-Request-ID"))
		}
	})

	t.Run("generated when absent", func(t *testing.T) {
		seen = ""
		rec := serve(engine, http.MethodGet, "/probe", "", nil)
		if len(seen) != 16 {
			t.Fatalf("generated id = %q, want 16 hex chars", seen)
		}
		if rec.Header().Get("X-Request-ID") != seen {
			t.Errorf("response header %q != context id %q", rec.Header().Get("X-Request-ID"), seen)
		}
		first := seen
		serve(engine, http.MethodGet, "/probe", "", nil)
		if seen == first {
			t.Error("each request must get a fresh correlation id")
		}
	})

	t.Run("oversized replaced", func(t *testing.T) {
		seen = ""
		serve(engine, http.MethodGet, "/probe", "", map[string]string{"X-Request-ID": strings.Repeat("a", 65)})
		if len(seen) != 16 {
			t.Fatalf("oversized id = %q, want a generated replacement", seen)
		}
	})

	t.Run("blank replaced", func(t *testing.T) {
		seen = ""
		serve(engine, http.MethodGet, "/probe", "", map[string]string{"X-Request-ID": "   "})
		if seen == "" || len(seen) != 16 {
			t.Fatalf("blank id = %q, want a generated replacement", seen)
		}
	})
}

// TestRequestIDAbsentAndWrongType documents the defensive helper.
func TestRequestIDAbsentAndWrongType(t *testing.T) {
	c, _ := testContext(http.MethodGet, "/probe", "", nil)
	if got := requestID(c); got != "" {
		t.Errorf("requestID() = %q, want empty when nothing was stored", got)
	}
	c.Set(ctxRequestID, 42) // never a string in production, but must not panic
	if got := requestID(c); got != "" {
		t.Errorf("requestID() = %q, want empty for a non-string value", got)
	}
}

// TestRandomToken asserts the correlation-id generator (n bytes hex encoded).
func TestRandomToken(t *testing.T) {
	tok, err := randomToken(8)
	if err != nil {
		t.Fatalf("randomToken: %v", err)
	}
	if len(tok) != 16 {
		t.Fatalf("len(randomToken(8)) = %d, want 16 hex chars", len(tok))
	}
	other, _ := randomToken(8)
	if tok == other {
		t.Error("two tokens must not collide")
	}
	if _, err := randomToken(0); err != nil {
		t.Errorf("randomToken(0) = %v, want nil", err)
	}
}

// TestRecoveryMiddleware converts a panic into the PRD §8.1 500 envelope instead
// of killing the connection.
func TestRecoveryMiddleware(t *testing.T) {
	engine := newEngine(recoveryMiddleware())
	engine.GET("/boom", func(*gin.Context) { panic("kaboom") })

	rec := serve(engine, http.MethodGet, "/boom", "", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("got %d, want 500", rec.Code)
	}
	var env struct {
		Status    string `json:"status"`
		ErrorCode string `json:"error_code"`
	}
	if err := parseJSON(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Status != "error" || env.ErrorCode != CodeInternalError {
		t.Errorf("envelope = %+v, want error/%s", env, CodeInternalError)
	}
}

// TestSecurityHeadersMiddleware pins the PRD §9.3 hardening headers on every
// response.
func TestSecurityHeadersMiddleware(t *testing.T) {
	engine := newEngine(securityHeadersMiddleware())
	engine.GET("/probe", func(c *gin.Context) { c.Status(http.StatusOK) })

	rec := serve(engine, http.MethodGet, "/probe", "", nil)
	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
		"Permissions-Policy":     "camera=(), microphone=(), geolocation=(), payment=()",
	}
	for header, value := range want {
		if got := rec.Header().Get(header); got != value {
			t.Errorf("%s = %q, want %q", header, got, value)
		}
	}
}

// TestCORSMiddleware covers the dashboard allowlist (PRD §9.3): only a listed
// origin is echoed, and a preflight is answered without reaching a handler.
func TestCORSMiddleware(t *testing.T) {
	settings := Settings{AllowedOrigins: []string{"https://dashboard.example"}}
	svc := newRBACService(newFakeStore(), settings, nil)

	engine := newEngine(svc.corsMiddleware())
	handled := false
	engine.Any("/api/v1/vehicles", func(c *gin.Context) {
		handled = true
		c.Status(http.StatusOK)
	})

	rec := serve(engine, http.MethodGet, "/api/v1/vehicles", "",
		map[string]string{"Origin": "https://dashboard.example"})
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://dashboard.example" {
		t.Fatalf("allow-origin = %q, want the listed origin", got)
	}
	if rec.Header().Get("Vary") != "Origin" || rec.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Error("CORS response must carry Vary: Origin + credentials")
	}
	if !strings.Contains(rec.Header().Get("Access-Control-Allow-Headers"), "Authorization") {
		t.Error("Authorization must be an allowed request header")
	}

	rec2 := serve(engine, http.MethodGet, "/api/v1/vehicles", "",
		map[string]string{"Origin": "https://evil.example"})
	if got := rec2.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("unlisted origin echoed back: %q (CORS must never widen)", got)
	}

	handled = false
	rec3 := serve(engine, http.MethodOptions, "/api/v1/vehicles", "",
		map[string]string{"Origin": "https://dashboard.example"})
	if rec3.Code != http.StatusNoContent || handled {
		t.Fatalf("preflight = %d (handler ran=%v), want 204 without touching the handler", rec3.Code, handled)
	}

	// A request without an Origin header (non-browser client) still reaches the
	// handler.
	handled = false
	serve(engine, http.MethodGet, "/api/v1/vehicles", "", nil)
	if !handled {
		t.Error("a request without Origin must pass through")
	}
}

// TestAccessLogMiddleware covers the three log/status classes and the
// "unmatched" route label used for 404s.
func TestAccessLogMiddleware(t *testing.T) {
	engine := newEngine(accessLogMiddleware("api-vehicle"))
	engine.GET("/ok", func(c *gin.Context) { c.Status(http.StatusOK) })
	engine.GET("/bad", func(c *gin.Context) { c.Status(http.StatusBadRequest) })
	engine.GET("/boom", func(c *gin.Context) { c.Status(http.StatusInternalServerError) })

	cases := []struct {
		target string
		want   int
	}{
		{"/ok", http.StatusOK},
		{"/bad", http.StatusBadRequest},
		{"/boom", http.StatusInternalServerError},
		{"/missing", http.StatusNotFound}, // route == "" → the "unmatched" label
	}
	for _, tc := range cases {
		if rec := serve(engine, http.MethodGet, tc.target, "", nil); rec.Code != tc.want {
			t.Errorf("%s = %d, want %d", tc.target, rec.Code, tc.want)
		}
	}
}

// TestBodyLimitMiddleware caps oversized bodies (PRD §8.5 rule 4) and stays a
// no-op when the limit is disabled.
func TestBodyLimitMiddleware(t *testing.T) {
	readAll := func(c *gin.Context) {
		if _, err := io.ReadAll(c.Request.Body); err != nil {
			c.String(http.StatusRequestEntityTooLarge, "too large")
			return
		}
		c.Status(http.StatusOK)
	}

	limited := newRBACService(newFakeStore(), Settings{MaxBodyBytes: 8}, nil)
	engine := newEngine(limited.bodyLimitMiddleware())
	engine.POST("/body", readAll)

	if rec := serve(engine, http.MethodPost, "/body", strings.Repeat("x", 64), nil); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized body = %d, want 413", rec.Code)
	}
	if rec := serve(engine, http.MethodPost, "/body", "small", nil); rec.Code != http.StatusOK {
		t.Errorf("small body = %d, want 200", rec.Code)
	}

	unlimited := newRBACService(newFakeStore(), Settings{}, nil)
	engine2 := newEngine(unlimited.bodyLimitMiddleware())
	engine2.POST("/body", readAll)
	if rec := serve(engine2, http.MethodPost, "/body", strings.Repeat("x", 64), nil); rec.Code != http.StatusOK {
		t.Errorf("disabled limit = %d, want 200", rec.Code)
	}
}
