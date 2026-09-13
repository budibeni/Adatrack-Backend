package controllers

// coverage_auth_token_extra_test.go (B4 coverage api-vehicle 2026-09-09):
// success paths authRefreshHandler + authLogoutHandler berbasis
// installFakeTokenManager (avFakeRedisCmd) — tanpa Redis nyata.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"ajb_gps/api-vehicle/models"
	"ajb_gps/internal/tokenauth"
	"github.com/gin-gonic/gin"
)

func testCtx(t *testing.T) context.Context {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	return c.Request.Context()
}

func issueTestRefresh(t *testing.T, userID uint64) string {
	t.Helper()
	m := getTokenManager()
	tok, err := m.IssueRefresh(testCtx(t), tokenauth.Payload{
		UserID:      userID,
		CompanyCode: "DEV001",
		Email:       "u@dev001.io",
		Role:        "Admin",
	}, appCfg.JWT.RefreshExpiry)
	if err != nil {
		t.Fatalf("IssueRefresh: %v", err)
	}
	return tok
}

func TestRefreshSuccess(t *testing.T) {
	t.Helper()
	_ = installFakeTokenManager(t)

	ref := issueTestRefresh(t, 7)
	c, rec, _ := companyCtx(t, true, "POST", "/auth/refresh", `{"refresh_token":"`+ref+`"}`)

	authRefreshHandler(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("refresh success = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "success" {
		t.Fatalf("status = %v", body["status"])
	}
	if body["token"] == "" || body["refresh_token"] == "" {
		t.Fatalf("refresh response missing tokens: %s", rec.Body.String())
	}
}

func TestRefreshRotationKeepsTokensDistinct(t *testing.T) {
	t.Helper()
	_ = installFakeTokenManager(t)
	ref := issueTestRefresh(t, 9)

	c, rec, _ := companyCtx(t, true, "POST", "/auth/refresh", `{"refresh_token":"`+ref+`"}`)
	authRefreshHandler(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("refresh rotation = %d, want 200", rec.Code)
	}

	// Old refresh token must no longer resolve (rotasi → invalid).
	c2, rec2, _ := companyCtx(t, true, "POST", "/auth/refresh", `{"refresh_token":"`+ref+`"}`)
	authRefreshHandler(c2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("reuse old refresh = %d, want 401: %s", rec2.Code, rec2.Body.String())
	}
}

func TestLogoutWithRefreshToken(t *testing.T) {
	t.Helper()
	_ = installFakeTokenManager(t)

	ref := issueTestRefresh(t, 7)
	c, rec, _ := companyCtx(t, true, "POST", "/auth/logout", `{"refresh_token":"`+ref+`"}`)

	authLogoutHandler(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("logout refresh = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

func TestLogoutWithBearerToken(t *testing.T) {
	t.Helper()
	_ = installFakeTokenManager(t)

	mu := models.MasterUser{ID: 7, CompanyCode: "DEV001", Email: "u@dev001.io"}
	tok := signTestAuth(t, mu, "Admin")

	c, rec, _ := companyCtx(t, true, "POST", "/auth/logout", `{}`)
	c.Request.Header.Set("Authorization", "Bearer "+tok)

	authLogoutHandler(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("logout bearer = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

func TestLogoutUnknownRefreshStillOK(t *testing.T) {
	t.Helper()
	_ = installFakeTokenManager(t)

	c, rec, _ := companyCtx(t, true, "POST", "/auth/logout", `{"refresh_token":"unknown-rt"}`)

	authLogoutHandler(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("logout unknown refresh = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}
