package main

import (
	"context"
	"net/http"
	"strings"
)

// checkHealthz asserts the readiness endpoint (PRD §10.2).
func checkHealthz(ctx context.Context, api *httpClient) checkResult {
	const name = "http.healthz"
	resp := api.get(ctx, "/healthz", "")
	if resp.RequestErr != nil {
		return fail(name, "service unreachable", resp.RequestErr)
	}
	if resp.Status != http.StatusOK {
		return fail(name, statusDetail(resp), nil)
	}
	if status, _ := resp.Body["status"].(string); status != "ok" {
		return fail(name, statusDetail(resp), nil)
	}
	return pass(name, "status=ok")
}

// checkLogin authenticates a principal and returns its session.
func checkLogin(ctx context.Context, api *httpClient, email, password string) (*session, checkResult) {
	name := "auth.login " + email
	sess, resp := api.login(ctx, email, password)
	if resp.RequestErr != nil {
		return nil, fail(name, "request failed", resp.RequestErr)
	}
	if resp.Status != http.StatusOK || sess == nil || sess.AccessToken == "" || sess.RefreshToken == "" {
		return nil, fail(name, statusDetail(resp), nil)
	}
	if sess.CompanyCode == "" || sess.Role == "" {
		return nil, fail(name, "identity claims missing: "+statusDetail(resp), nil)
	}
	return sess, pass(name, "role="+sess.Role+" company="+sess.CompanyCode)
}

// checkLoginBadPassword asserts a wrong password yields 401 INVALID_CREDENTIALS
// (and, indirectly, that a LOGIN_FAILURE audit row is written — asserted later).
func checkLoginBadPassword(ctx context.Context, api *httpClient, email string) checkResult {
	const name = "auth.invalid_credentials"
	resp := api.request(ctx, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"email": email, "password": "definitely-wrong-password",
	})
	if resp.RequestErr != nil {
		return fail(name, "request failed", resp.RequestErr)
	}
	if resp.Status != http.StatusUnauthorized {
		return fail(name, statusDetail(resp), nil)
	}
	if code := resp.errorCode(); code != "INVALID_CREDENTIALS" {
		return fail(name, "error_code="+code, nil)
	}
	return pass(name, "401 INVALID_CREDENTIALS")
}

// checkNoToken asserts a token-less request is rejected (PRD §3.1).
func checkNoToken(ctx context.Context, api *httpClient) checkResult {
	const name = "auth.missing_token"
	resp := api.get(ctx, "/api/v1/vehicles", "")
	if resp.RequestErr != nil {
		return fail(name, "request failed", resp.RequestErr)
	}
	if resp.Status != http.StatusUnauthorized {
		return fail(name, statusDetail(resp), nil)
	}
	return pass(name, "401 UNAUTHORIZED")
}

// checkRefreshRotation asserts FR-5.7 rotation (single-use refresh token).
func checkRefreshRotation(ctx context.Context, api *httpClient, sess *session) checkResult {
	const name = "auth.refresh_rotation"
	resp := api.request(ctx, http.MethodPost, "/api/v1/auth/refresh", "", map[string]string{
		"refresh_token": sess.RefreshToken,
	})
	if resp.RequestErr != nil {
		return fail(name, "request failed", resp.RequestErr)
	}
	if resp.Status != http.StatusOK {
		return fail(name, statusDetail(resp), nil)
	}
	rotated, _ := resp.data()["refresh_token"].(string)
	if rotated == "" || rotated == sess.RefreshToken {
		return fail(name, "refresh token was not rotated", nil)
	}
	// The consumed token must now be rejected.
	replay := api.request(ctx, http.MethodPost, "/api/v1/auth/refresh", "", map[string]string{
		"refresh_token": sess.RefreshToken,
	})
	if replay.Status != http.StatusUnauthorized {
		return fail(name, "replayed refresh token was accepted: "+statusDetail(replay), nil)
	}
	// Keep using the freshest access token for the remaining checks.
	if access, _ := resp.data()["access_token"].(string); access != "" {
		sess.AccessToken = access
		sess.RefreshToken = rotated
	}
	return pass(name, "rotated; replay rejected (401)")
}

// checkPlatformScope asserts a platform token cannot use tenant routes (§3.1).
func checkPlatformScope(ctx context.Context, api *httpClient, platform *session) checkResult {
	const name = "rbac.platform_scope"
	resp := api.get(ctx, "/api/v1/vehicles", platform.AccessToken)
	if resp.RequestErr != nil {
		return fail(name, "request failed", resp.RequestErr)
	}
	if resp.Status != http.StatusForbidden || resp.errorCode() != "PLATFORM_SCOPE" {
		return fail(name, statusDetail(resp), nil)
	}
	return pass(name, "403 PLATFORM_SCOPE")
}

// checkPlatformOnly asserts a tenant token cannot use platform routes (§3.1).
func checkPlatformOnly(ctx context.Context, api *httpClient, tenant *session) checkResult {
	const name = "rbac.platform_only"
	resp := api.request(ctx, http.MethodPost, "/api/v1/companies", tenant.AccessToken, map[string]string{
		"code": "E2EB2", "name": "E2E Probe",
	})
	if resp.RequestErr != nil {
		return fail(name, "request failed", resp.RequestErr)
	}
	if resp.Status != http.StatusForbidden || resp.errorCode() != "PLATFORM_ONLY" {
		return fail(name, statusDetail(resp), nil)
	}
	return pass(name, "403 PLATFORM_ONLY")
}

// checkLogoutAndRevocation asserts FR-5.7: logout denylists the access token.
func checkLogoutAndRevocation(ctx context.Context, api *httpClient, sess *session) checkResult {
	const name = "auth.logout_revocation"
	resp := api.request(ctx, http.MethodPost, "/api/v1/auth/logout", sess.AccessToken, nil)
	if resp.RequestErr != nil {
		return fail(name, "request failed", resp.RequestErr)
	}
	if resp.Status != http.StatusOK {
		return fail(name, statusDetail(resp), nil)
	}
	reuse := api.get(ctx, "/api/v1/vehicles", sess.AccessToken)
	if reuse.Status != http.StatusUnauthorized {
		return fail(name, "revoked token still works: "+statusDetail(reuse), nil)
	}
	if code := reuse.errorCode(); !strings.EqualFold(code, "TOKEN_REVOKED") {
		return fail(name, "error_code="+code+" (want TOKEN_REVOKED)", nil)
	}
	return pass(name, "401 TOKEN_REVOKED")
}
