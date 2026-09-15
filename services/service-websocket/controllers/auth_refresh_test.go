package controllers

import (
	"net/http"
	"testing"
	"time"

	"ajb_gps/service-websocket/models"
)

// TestAccessTokenExpiry verifies the `exp` claim is enforced (with clock skew).
func TestAccessTokenExpiry(t *testing.T) {
	h := newHarness(t)
	access, _ := h.login(t, "admin@dev001.io", "Admin@123")

	claims, err := h.service.auth.ParseAccessToken(access)
	if err != nil {
		t.Fatalf("parse token: %v", err)
	}
	if claims.ExpiresAt == nil || claims.ExpiresAt.Time.Before(time.Now()) {
		t.Fatalf("expiry not set: %v", claims.ExpiresAt)
	}

	// A token minted "48 h ago" (expiry 24 h) must be rejected as expired.
	user, _ := h.store.UserByEmail(t.Context(), "admin@dev001.io")
	identity, rerr := h.service.auth.resolveIdentity(t.Context(), user)
	if rerr != nil {
		t.Fatalf("resolve identity: %v", rerr)
	}
	h.service.auth.now = func() time.Time { return time.Now().Add(-48 * time.Hour) }
	expired, terr := h.service.auth.issueTokens(t.Context(), identity, nil)
	h.service.auth.now = time.Now
	if terr != nil {
		t.Fatalf("issue expired token: %v", terr)
	}

	_, perr := h.service.auth.ParseAccessToken(expired.AccessToken)
	if perr == nil {
		t.Fatalf("expired token accepted")
	}
	if apiErr, ok := perr.(*APIError); !ok || apiErr.Code != CodeTokenExpired {
		t.Fatalf("expired token error = %v, want %s", perr, CodeTokenExpired)
	}
}

// TestAccessTokenWrongSecretIsRejected verifies the HS256 signature check.
func TestAccessTokenWrongSecretIsRejected(t *testing.T) {
	h := newHarness(t)
	access, _ := h.login(t, "admin@dev001.io", "Admin@123")

	h.service.auth.cfg.JWTSecret = "another-secret-key-with-32-characters!!"
	if _, err := h.service.auth.ParseAccessToken(access); err == nil {
		t.Fatalf("token signed with another key was accepted")
	}
}

// TestRefreshRotationAndLogout verifies FR-5.7: rotation invalidates the old
// refresh token and logout denylists the access token.
func TestRefreshRotationAndLogout(t *testing.T) {
	h := newHarness(t)
	access, refresh := h.login(t, "admin@dev001.io", "Admin@123")

	// First refresh succeeds and returns a NEW refresh token.
	resp, body := h.do(t, http.MethodPost, "/api/v1/auth/refresh", "", models.RefreshRequest{RefreshToken: refresh})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("refresh status = %d, want 200 (body %v)", resp.StatusCode, body)
	}
	data := body["data"].(map[string]any)
	rotated, _ := data["refresh_token"].(string)
	if rotated == "" || rotated == refresh {
		t.Fatalf("refresh token was not rotated: %q", rotated)
	}

	// Replaying the consumed token must fail (single-use rotation).
	resp, body = h.do(t, http.MethodPost, "/api/v1/auth/refresh", "", models.RefreshRequest{RefreshToken: refresh})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("replayed refresh status = %d, want 401 (body %v)", resp.StatusCode, body)
	}

	// Logout denylists the access token → subsequent use is 401 TOKEN_REVOKED.
	resp, _ = h.do(t, http.MethodPost, "/api/v1/auth/logout", access, map[string]string{"refresh_token": rotated})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("logout status = %d, want 200", resp.StatusCode)
	}
	resp, body = h.do(t, http.MethodGet, "/api/v1/vehicles", access, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked token status = %d, want 401 (body %v)", resp.StatusCode, body)
	}
	if got := errorCode(t, body); got != CodeTokenRevoked {
		t.Fatalf("error_code = %q, want %s", got, CodeTokenRevoked)
	}
	if countAction(h.store.auditsSnapshot(), ActionTokenRevoked) == 0 {
		t.Fatalf("TOKEN_REVOKED audit missing")
	}
}

// TestLogoutIsFailClosedWhenAuditFails asserts PRD §9.4 ("aksi sensitif
// gagal-audit → request ditolak").
func TestLogoutIsFailClosedWhenAuditFails(t *testing.T) {
	h := newHarness(t)
	access, _ := h.login(t, "admin@dev001.io", "Admin@123")

	h.store.mu.Lock()
	h.store.auditErr = errAuditDown
	h.store.mu.Unlock()

	resp, body := h.do(t, http.MethodPost, "/api/v1/auth/logout", access, nil)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body %v)", resp.StatusCode, body)
	}
}

// TestLoginFailClosedWhenAuditFails: a login that cannot be audited must fail.
func TestLoginFailClosedWhenAuditFails(t *testing.T) {
	h := newHarness(t)
	h.store.mu.Lock()
	h.store.auditErr = errAuditDown
	h.store.mu.Unlock()

	resp, body := h.do(t, http.MethodPost, "/api/v1/auth/login", "", models.LoginRequest{
		Email: "admin@dev001.io", Password: "Admin@123",
	})
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body %v)", resp.StatusCode, body)
	}
}

// TestAccessDeniedIsAudited asserts PRD §9.4 "log all 403 (audit ACCESS_DENIED)".
func TestAccessDeniedIsAudited(t *testing.T) {
	h := newHarness(t)

	resp, body := h.do(t, http.MethodGet, "/api/v1/vehicles", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	if got := errorCode(t, body); got != CodeUnauthorized {
		t.Fatalf("error_code = %q, want %s", got, CodeUnauthorized)
	}
	waitForAudit(t, h, func(rows []AuditRow) bool { return countAction(rows, ActionAccessDenied) >= 1 },
		"ACCESS_DENIED audit row for a token-less request")
}
