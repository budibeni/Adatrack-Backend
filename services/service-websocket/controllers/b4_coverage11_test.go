package controllers

// b4_coverage11_test.go (B4 coverage 2026-09-05 — Stage H):
// websocketHandler FULL success path (tenant admin, real loopback), SERVER_FULL
// rejection, bad-origin upgrade denial; authRefreshHandler success rotation;
// authLogoutHandler full (bearer + refresh) via miniredis.

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ajb_gps/internal"
	"ajb_gps/internal/tokenauth"
	"ajb_gps/service-websocket/models"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// wsTokenFor signs a tenant-admin JWT and installs globals so authorize() succeeds.
func wsTenantAdminFixture(t *testing.T) (string, func()) {
	t.Helper()
	oldCfg, oldHub, oldComp := appCfg, appHub, companyDBByCodeFn
	appCfg = internal.LoadConfig()
	appCfg.WebSocket.HeartbeatInterval = 50 * time.Millisecond
	appCfg.WebSocket.PongWait = 5 * time.Second
	appCfg.WebSocket.WriteWait = 2 * time.Second
	appCfg.WebSocket.MaxMessageSize = 65536
	appCfg.WebSocket.MaxQueue = 8
	appHub = newHub(16, 16)

	db, m := mockDB(t)
	companyDBByCodeFn = func(string) (*sql.DB, error) { return db, nil }
	m.ExpectQuery(`SELECT id, role_override, is_active FROM user_company_access`).
		WithArgs(uint64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "role_override", "is_active"}).
			AddRow(uint64(5), nil, true))

	restore := func() {
		appCfg, appHub, companyDBByCodeFn = oldCfg, oldHub, oldComp
	}

	token, _, err := signToken(appCfg, models.MasterUser{
		ID: 7, Email: "a@b.io", CompanyCode: "DEV001",
	}, "Admin", nil)
	if err != nil {
		restore()
		t.Fatalf("signToken: %v", err)
	}
	return token, restore
}

func wsEngine() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/v1/ws", websocketHandler)
	return r
}

func wsURL(server *httptest.Server, token string) string {
	return "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/ws?token=" + token
}

func TestWebsocketHandler_FullSuccess(t *testing.T) {
	token, restore := wsTenantAdminFixture(t)
	defer restore()

	srv := httptest.NewServer(wsEngine())
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL(srv, token), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(msg), "CONNECTION_STATUS") {
		t.Fatalf("expected CONNECTION_STATUS, got %s", msg)
	}

	// Kirim SUBSCRIBE ke vehicle 7 (admin allowAll → diterima).
	if err := conn.WriteMessage(websocket.TextMessage,
		[]byte(`{"event":"SUBSCRIBE","data":{"vehicle_id":7}}`)); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, msg2, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read2: %v", err)
	}
	if !strings.Contains(string(msg2), "SUBSCRIBED") {
		t.Fatalf("expected SUBSCRIBED, got %s", msg2)
	}
}

func TestWebsocketHandler_ServerFull(t *testing.T) {
	token, restore := wsTenantAdminFixture(t)
	defer restore()
	// Kapasitas habis → register ditolak → pesan SERVER_FULL dikirim lalu conn ditutup.
	appHub = newHub(0, 0)

	srv := httptest.NewServer(wsEngine())
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL(srv, token), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil && !websocket.IsCloseError(err, websocket.CloseNormalClosure) {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(msg), "SERVER_FULL") {
		t.Fatalf("expected SERVER_FULL, got %s", msg)
	}
}

func TestWebsocketHandler_OriginRejected(t *testing.T) {
	token, restore := wsTenantAdminFixture(t)
	defer restore()
	appCfg.HTTP.CORSOrigins = []string{"http://localhost:3000"}

	srv := httptest.NewServer(wsEngine())
	defer srv.Close()

	dialer := websocket.Dialer{}
	reqHeader := http.Header{}
	reqHeader.Set("Origin", "https://evil.example")
	_, _, err := dialer.Dial(wsURL(srv, token), reqHeader)
	if err == nil {
		t.Fatal("expected handshake to be rejected for disallowed origin")
	}
}

// tokenStoreFixture boots miniredis + token manager and returns restore.
func tokenStoreFixture(t *testing.T) func() {
	t.Helper()
	mr := miniredis.RunT(t)
	cfg := internal.LoadConfig()
	cfg.Redis.Addr = mr.Addr()
	red, rerr := internal.NewRedisClient(cfg, nil, nil)
	if rerr != nil {
		t.Fatalf("NewRedisClient: %v", rerr)
	}
	oldR, oldC, oldM := appRedis, appCfg, tokenMgr
	appRedis, appCfg, tokenMgr = red, cfg, nil
	return func() { appRedis, appCfg, tokenMgr = oldR, oldC, oldM }
}

// ---------------------------------------------------------------------------
// authRefreshHandler — full success rotation (miniredis token store)
// ---------------------------------------------------------------------------

func TestAuthRefreshHandler_SuccessRotation(t *testing.T) {
	restore := tokenStoreFixture(t)
	defer restore()

	// Keluarkan refresh token nyata dari store.
	ctx := t.Context()
	refresh, rerr := getTokenManager().IssueRefresh(ctx, tokenauth.Payload{
		UserID: 7, CompanyCode: "DEV001", Email: "a@b.io", Role: "Admin",
	}, appCfg.JWT.RefreshExpiry)
	if rerr != nil {
		t.Fatalf("IssueRefresh: %v", rerr)
	}

	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh",
		strings.NewReader(`{"refresh_token":"`+refresh+`"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	authRefreshHandler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "refresh_token") {
		t.Errorf("response missing refresh_token: %s", rec.Body.String())
	}
}

func TestAuthRefreshHandler_InvalidRefresh(t *testing.T) {
	restore := tokenStoreFixture(t)
	defer restore()

	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh",
		strings.NewReader(`{"refresh_token":"bogus-token"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	authRefreshHandler(c)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// authLogoutHandler — full (bearer + refresh) success
// ---------------------------------------------------------------------------

func TestAuthLogoutHandler_BearerAndRefresh(t *testing.T) {
	restore := tokenStoreFixture(t)
	defer restore()

	ctx := t.Context()
	refresh, _ := getTokenManager().IssueRefresh(ctx, tokenauth.Payload{
		UserID: 7, CompanyCode: "DEV001", Email: "a@b.io", Role: "Admin",
	}, appCfg.JWT.RefreshExpiry)
	token, _, _ := signToken(appCfg, models.MasterUser{
		ID: 7, Email: "a@b.io", CompanyCode: "DEV001",
	}, "Admin", nil)

	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout",
		strings.NewReader(`{"refresh_token":"`+refresh+`"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("Authorization", "Bearer "+token)

	authLogoutHandler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAuthLogoutHandler_NoTokens(t *testing.T) {
	restore := tokenStoreFixture(t)
	defer restore()

	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout",
		strings.NewReader(`{}`))
	c.Request.Header.Set("Content-Type", "application/json")

	authLogoutHandler(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

// keep sql referenced in all build configurations
var _ = sql.ErrNoRows
