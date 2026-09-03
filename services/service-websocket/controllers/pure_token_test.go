package controllers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func ginTestCtx(t *testing.T, header, queryToken string) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if header != "" {
		req.Header.Set("Authorization", header)
	}
	if queryToken != "" {
		req.URL.RawQuery = "token=" + queryToken
	}
	c.Request = req
	return c
}

func TestExtractTokenBearer(t *testing.T) {
	c := ginTestCtx(t, "Bearer abc.def.ghi", "")
	if got := extractToken(c); got != "abc.def.ghi" {
		t.Errorf("bearer token = %q, want abc.def.ghi", got)
	}
}

func TestExtractTokenBearerWithWhitespace(t *testing.T) {
	c := ginTestCtx(t, "Bearer   abc.def  ", "")
	if got := extractToken(c); got != "abc.def" {
		t.Errorf("token dgn whitespace = %q, want abc.def", got)
	}
}

func TestExtractTokenQueryFallback(t *testing.T) {
	c := ginTestCtx(t, "", "q.token")
	if got := extractToken(c); got != "q.token" {
		t.Errorf("query token = %q, want q.token", got)
	}
}

func TestExtractTokenMissingReturnsEmpty(t *testing.T) {
	c := ginTestCtx(t, "", "")
	if got := extractToken(c); got != "" {
		t.Errorf("tanpa token = %q, ingin kosong", got)
	}
}

func TestExtractTokenNonBearerIgnoresHeader(t *testing.T) {
	c := ginTestCtx(t, "Basic dXNlcjpwdw==", "")
	if got := extractToken(c); got != "" {
		t.Errorf("non-Bearer = %q, ingin kosong (fall back ke query)", got)
	}
}

func TestWSRedisVehicleStateKey(t *testing.T) {
	t.Setenv("REDIS_KEY_PREFIX", "")
	cases := []struct {
		company, imei, want string
	}{
		{"DEV001", "123", "adatrack_gps:dev001:vehicle:state:123"},
		{" able01 ", "456", "adatrack_gps:able01:vehicle:state:456"},
		{"", "789", "adatrack_gps:default:vehicle:state:789"},
	}
	for _, c := range cases {
		if got := redisVehicleStateKey(c.company, c.imei); got != c.want {
			t.Errorf("redisVehicleStateKey(%q,%q) = %q, want %q", c.company, c.imei, got, c.want)
		}
	}
}

func TestWSRedisVehicleStateKeyCustomPrefix(t *testing.T) {
	t.Setenv("REDIS_KEY_PREFIX", "prefix:")
	if got := redisVehicleStateKey("ABC", "1"); got != "prefix:abc:vehicle:state:1" {
		t.Errorf("custom prefix = %q", got)
	}
}