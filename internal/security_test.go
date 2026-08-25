package internal

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestApplySecurityHeadersSetsAllHeaders verifies every security header is set.
func TestApplySecurityHeadersSetsAllHeaders(t *testing.T) {
	w := httptest.NewRecorder()
	ApplySecurityHeaders(w)

	expected := map[string]string{
		"Strict-Transport-Security": "max-age=31536000; includeSubDomains",
		"X-Frame-Options":           "DENY",
		"X-Content-Type-Options":    "nosniff",
		"Referrer-Policy":           "strict-origin-when-cross-origin",
		"Content-Security-Policy":   "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; connect-src 'self' wss:; frame-ancestors 'none'; base-uri 'self'",
		"Permissions-Policy":        "geolocation=(), microphone=(), camera=()",
		"X-XSS-Protection":          "1; mode=block",
		"Cache-Control":             "no-store, no-cache, must-revalidate, max-age=0",
	}

	for k, v := range expected {
		got := w.Header().Get(k)
		if got != v {
			t.Errorf("header %q = %q, want %q", k, got, v)
		}
	}
}

// TestSecurityHeadersMiddleware applies headers to a request.
func TestSecurityHeadersMiddleware(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := SecurityHeadersMiddleware(next)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)

	handler.ServeHTTP(w, req)

	if !called {
		t.Error("next handler was not called")
	}
	if w.Header().Get("X-Frame-Options") != "DENY" {
		t.Error("security headers not applied by middleware")
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("X-Content-Type-Options header not set")
	}
}
