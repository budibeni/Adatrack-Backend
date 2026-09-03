package controllers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ajb_gps/internal"
	"ajb_gps/internal/tenant"
	"ajb_gps/service-websocket/models"

	"github.com/gin-gonic/gin"
	nats "github.com/nats-io/nats.go"
)

// saveWSGlobals menyimpan state global paket dan mengembalikan restore func —
// agar test bridge/gates tidak saling mencemari.
func saveWSGlobals(t *testing.T) func() {
	t.Helper()
	oc, or, on, ot, oh, ov := appCfg, appRedis, appNATS, appTenant, appHub, vehReg
	ol := apiLimiter
	return func() {
		appCfg, appRedis, appNATS, appTenant, appHub, vehReg = oc, or, on, ot, oh, ov
		apiLimiter = ol
	}
}

// seedRegistry menanam satu entry cache registry (company, imei) → vehicle.
func seedRegistry(r *vehicleRegistry, company, imei string, info vehicleInfo) {
	r.mu.Lock()
	r.cache[registryKey(company, imei)] = registryEntry{info: info, expire: time.Now().Add(time.Minute)}
	r.mu.Unlock()
}

// recvChan membaca satu payload dari channel send client dgn timeout.
func recvChan(t *testing.T, ch chan []byte) []byte {
	t.Helper()
	select {
	case b := <-ch:
		return b
	case <-time.After(2 * time.Second):
		t.Fatal("timeout menunggu payload broadcast")
		return nil
	}
}

// ---------------------------------------------------------------------------
// bridgeHandle — telemetry.raw.<IMEI> → VEHICLE_UPDATE (RBAC + tenant filter)
// ---------------------------------------------------------------------------

func TestBridgeHandle_DeliversVehicleUpdate(t *testing.T) {
	defer saveWSGlobals(t)()
	appCfg = internal.LoadConfig()
	vehReg = newVehicleRegistry()
	seedRegistry(vehReg, "DEV001", "111", vehicleInfo{ID: 42, Model: "GT06", Plate: "B1234XYZ"})
	appHub = newHub(10, 16)

	fc := &fakeConn{}
	cl := newTestClient(fc, make(chan []byte, 8), 1, "DEV001", map[uint64]struct{}{42: {}}, false)
	if !appHub.register(cl) {
		t.Fatal("register gagal")
	}

	payload := []byte(`{"imei":"111","company_code":"DEV001","lat":-6.2,"lon":106.8,` +
		`"speed":35.5,"battery":80,"timestamp":1700000000}`)
	if err := bridgeHandle(&nats.Msg{Subject: "telemetry.raw.111", Data: payload}); err != nil {
		t.Fatalf("bridgeHandle: %v", err)
	}

	var ev models.VehicleUpdateEvent
	if err := json.Unmarshal(recvChan(t, cl.send), &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ev.Event != "VEHICLE_UPDATE" || ev.Data.VehicleID != 42 ||
		ev.Data.PlateNumber != "B1234XYZ" || ev.Data.Status != "MOVING" || !ev.Data.Acc {
		t.Fatalf("event tak sesuai: %+v", ev)
	}
}

func TestBridgeHandle_IdleUnknownAndBadPayloads(t *testing.T) {
	defer saveWSGlobals(t)()
	appCfg = internal.LoadConfig()
	vehReg = newVehicleRegistry()
	appHub = newHub(10, 16)

	// Invalid JSON → nil, tanpa panic; IMEI kosong → diabaikan.
	if err := bridgeHandle(&nats.Msg{Data: []byte("{broken")}); err != nil {
		t.Fatalf("invalid json harus nil, got %v", err)
	}
	if err := bridgeHandle(&nats.Msg{Data: []byte(`{"company_code":"DEV001"}`)}); err != nil {
		t.Fatalf("empty imei harus nil, got %v", err)
	}

	// Device terdaftar tapi speed 0 → IDLE + ACC false.
	seedRegistry(vehReg, "DEV001", "222", vehicleInfo{ID: 43})
	fc := &fakeConn{}
	cl := newTestClient(fc, make(chan []byte, 8), 1, "DEV001",
		map[uint64]struct{}{43: {}}, false)
	appHub.register(cl)

	// Unknown IMEI → tidak ada broadcast (anti-leak), lalu IDLE untuk yang dikenal.
	if err := bridgeHandle(&nats.Msg{Data: []byte(`{"imei":"noliste","company_code":"DEV001","speed":10}`)}); err != nil {
		t.Fatalf("unknown imei: %v", err)
	}
	if err := bridgeHandle(&nats.Msg{Data: []byte(`{"imei":"222","company_code":"DEV001","speed":0}`)}); err != nil {
		t.Fatalf("idle msg: %v", err)
	}
	var ev models.VehicleUpdateEvent
	if err := json.Unmarshal(recvChan(t, cl.send), &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ev.Data.Status != "IDLE" || ev.Data.Acc {
		t.Fatalf("expected IDLE/acc=false, got %+v", ev.Data)
	}
}

// ---------------------------------------------------------------------------
// notifyHandle — notify.alert.<vehicle_id> → ALERT_NOTIFICATION
// ---------------------------------------------------------------------------

func TestNotifyHandle_FanoutAndBadSubjects(t *testing.T) {
	defer saveWSGlobals(t)()
	appCfg = internal.LoadConfig()
	vehReg = newVehicleRegistry()
	appHub = newHub(10, 16)
	fc := &fakeConn{}
	cl := newTestClient(fc, make(chan []byte, 8), 1, "DEV001",
		map[uint64]struct{}{42: {}}, false)
	appHub.register(cl)

	notif := []byte(`{"alert_id":"700251","vehicle_id":42,"imei":"111",` +
		`"company_code":"DEV001","alert_type":"FUEL_DROP","severity":"critical",` +
		`"message":"Turun 12%","triggered_at":1700000000}`)
	if err := notifyHandle(&nats.Msg{Subject: "notify.alert.42", Data: notif}); err != nil {
		t.Fatalf("notifyHandle: %v", err)
	}
	var ev models.AlertNotificationEvent
	if err := json.Unmarshal(recvChan(t, cl.send), &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ev.Event != "ALERT_NOTIFICATION" || ev.Data.AlertID != "700251" ||
		ev.Data.VehicleID != 42 {
		t.Fatalf("event tak sesuai: %+v", ev)
	}

	// Subject vid bukan angka → diabaikan; payload korup → diabaikan.
	if err := notifyHandle(&nats.Msg{Subject: "notify.alert.abc", Data: notif}); err != nil {
		t.Fatalf("bad vid subject: %v", err)
	}
	if err := notifyHandle(&nats.Msg{Subject: "notify.alert.42", Data: []byte("{nope")}); err != nil {
		t.Fatalf("bad json: %v", err)
	}
}

// ---------------------------------------------------------------------------
// mediaHandle — media.event.<company> → MEDIA_EVENT
// ---------------------------------------------------------------------------

func TestMediaHandle_FanoutGuards(t *testing.T) {
	defer saveWSGlobals(t)()
	appCfg = internal.LoadConfig()
	vehReg = newVehicleRegistry()
	appHub = newHub(10, 16)
	fc := &fakeConn{}
	// ADMIN company DEV001 (allowAll) menerima semua vehicle di company-nya.
	cl := newTestClient(fc, make(chan []byte, 8), 1, "DEV001", nil, true)
	appHub.register(cl)

	if err := mediaHandle(&nats.Msg{Subject: "media.event.DEV001",
		Data: []byte(`{"company_code":"DEV001","vehicle_id":42,"media_id":7}`)}); err != nil {
		t.Fatalf("mediaHandle: %v", err)
	}
	var ev models.MediaEventWS
	if err := json.Unmarshal(recvChan(t, cl.send), &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ev.Event != "MEDIA_EVENT" || ev.Data.CompanyCode != "DEV001" || ev.Data.VehicleID != 42 {
		t.Fatalf("event tak sesuai: %+v", ev)
	}

	// Guard: company kosong & payload korup → nil tanpa panic.
	if err := mediaHandle(&nats.Msg{Subject: "media.event.X",
		Data: []byte(`{"vehicle_id":42}`)}); err != nil {
		t.Fatalf("missing company: %v", err)
	}
	if err := mediaHandle(&nats.Msg{Subject: "media.event.DEV001",
		Data: []byte("{bad")}); err != nil {
		t.Fatalf("bad json: %v", err)
	}
}

// ---------------------------------------------------------------------------
// hub: register duplikat, unregister, closeAll, count
// ---------------------------------------------------------------------------

func TestHub_RegisterDuplicateUnregisterCloseAll(t *testing.T) {
	h := newHub(8, 16)
	fc := &fakeConn{}
	cl := newTestClient(fc, make(chan []byte, 4), 1, "DEV001", nil, true)
	if !h.register(cl) {
		t.Fatal("register pertama harus sukses")
	}
	if h.register(cl) {
		t.Fatal("register duplikat harus ditolak")
	}
	if h.count() != 1 {
		t.Fatalf("count = %d, want 1", h.count())
	}
	h.unregister(cl)
	if h.count() != 0 {
		t.Fatalf("count setelah unregister = %d, want 0", h.count())
	}
	fc.mu.Lock()
	closed := fc.closed
	fc.mu.Unlock()
	if !closed {
		t.Fatal("unregister harus menutup koneksi")
	}

	// closeAll menutup semua client yang terdaftar.
	fc2, fc3 := &fakeConn{}, &fakeConn{}
	c2 := newTestClient(fc2, make(chan []byte, 4), 2, "DEV001", nil, true)
	c3 := newTestClient(fc3, make(chan []byte, 4), 3, "DEV001", nil, true)
	h.register(c2)
	h.register(c3)
	h.closeAll()
	for _, f := range []*fakeConn{fc2, fc3} {
		f.mu.Lock()
		c := f.closed
		f.mu.Unlock()
		if !c {
			t.Fatal("closeAll harus menutup semua koneksi")
		}
	}
}

// ---------------------------------------------------------------------------
// base.go helpers — companyReadByCode/masterDB/auditDB/healthHandler
// ---------------------------------------------------------------------------

func TestBase_DBHelpers_ZeroManagerSafe(t *testing.T) {
	defer saveWSGlobals(t)()
	appTenant = &tenant.Manager{} // zero value: tanpa pool apa pun

	if db := masterDB(); db != nil {
		t.Fatal("masterDB zero manager harus nil")
	}
	if db := auditDB(); db != nil {
		t.Fatal("auditDB zero manager harus nil")
	}
	if db, err := companyReadByCode("DEV001"); err == nil || db != nil {
		t.Fatalf("companyReadByCode zero manager harus gagal, got db=%v err=%v", db, err)
	}
}

func TestHealthHandler_DegradedWithoutInfra(t *testing.T) {
	defer saveWSGlobals(t)()
	appTenant = &tenant.Manager{}
	appRedis = nil
	appNATS = nil

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	healthHandler(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 degraded", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{"mysql:ok", "redis:down", "nats:down"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body %q tidak memuat %q", body, want)
		}
	}
}

// ---------------------------------------------------------------------------
// middleware: CORS + security headers + API rate limit
// ---------------------------------------------------------------------------

func TestCORSMiddleware_AllowedOptionsAndDenied(t *testing.T) {
	defer saveWSGlobals(t)()
	appCfg = internal.LoadConfig()
	appCfg.HTTP.CORSOrigins = []string{"http://localhost:3000"}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(corsMiddleware())
	r.Use(securityHeadersMiddleware())
	r.GET("/ping", func(c *gin.Context) { c.String(http.StatusOK, "pong") })

	// Origin di-allowlist → ACAO + security header.
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	r.ServeHTTP(w, req)
	if w.Header().Get("Access-Control-Allow-Origin") != "http://localhost:3000" {
		t.Fatalf("ACAO = %q", w.Header().Get("Access-Control-Allow-Origin"))
	}
	if w.Header().Get("X-Frame-Options") == "" {
		t.Fatal("security headers tidak diterapkan")
	}

	// Origin asing → tanpa ACAO.
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req2.Header.Set("Origin", "http://evil.example")
	r.ServeHTTP(w2, req2)
	if w2.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("origin asing tidak boleh dapat ACAO")
	}

	// Preflight OPTIONS → 204 + method headers.
	w3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodOptions, "/ping", nil)
	req3.Header.Set("Origin", "http://localhost:3000")
	r.ServeHTTP(w3, req3)
	if w3.Code != http.StatusNoContent || w3.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Fatalf("preflight = %d, headers=%v", w3.Code, w3.Header())
	}
}

func TestAPIRateLimitMiddleware_DeniesAfterMax(t *testing.T) {
	defer saveWSGlobals(t)()
	appCfg = internal.LoadConfig()
	apiLimiter = newAPIRateLimiter(1) // 1 req → request kedua 429

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(apiRateLimitMiddleware())
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, httptest.NewRequest(http.MethodGet, "/x", nil))
	if w1.Code != http.StatusOK {
		t.Fatalf("request pertama = %d, want 200", w1.Code)
	}
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/x", nil))
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("request kedua = %d, want 429", w2.Code)
	}
	var errResp models.ApiErrorResponse
	if err := json.Unmarshal(w2.Body.Bytes(), &errResp); err != nil ||
		errResp.ErrorCode != "RATE_LIMIT_EXCEEDED" {
		t.Fatalf("envelope error tak sesuai: %s (%v)", w2.Body.String(), err)
	}
}

// ---------------------------------------------------------------------------
// websocketHandler gates: 401 tanpa token / token korup, 403 authorize gagal
// ---------------------------------------------------------------------------

func wsGateEngine() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/v1/ws", websocketHandler)
	return r
}

func TestWSHandler_MissingAndBadToken(t *testing.T) {
	defer saveWSGlobals(t)()
	appCfg = internal.LoadConfig()
	appTenant = &tenant.Manager{}
	r := wsGateEngine()

	// Tanpa token → 401.
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil))
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "UNAUTHORIZED") {
		t.Fatalf("no token: %d %s", w.Code, w.Body.String())
	}

	// Token korup → 401.
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/api/v1/ws?token=garbage", nil))
	if w2.Code != http.StatusUnauthorized {
		t.Fatalf("bad token: %d", w2.Code)
	}
}

func TestWSHandler_ValidTokenAuthorizeFails(t *testing.T) {
	defer saveWSGlobals(t)()
	appCfg = internal.LoadConfig()
	appTenant = &tenant.Manager{} // authorize butuh master DB → gagal → 403

	token, _, err := signToken(appCfg, models.MasterUser{
		ID: 7, Email: "admin@dev001.io", CompanyCode: "DEV001",
	}, "ADMIN", nil)
	if err != nil {
		t.Fatalf("signToken: %v", err)
	}

	w := httptest.NewRecorder()
	wsGateEngine().ServeHTTP(w, httptest.NewRequest(http.MethodGet,
		"/api/v1/ws?token="+token, nil))
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "FORBIDDEN") {
		t.Fatalf("authorize gagal harus 403, got %d %s", w.Code, w.Body.String())
	}
}
