package controllers

// ws_extra_test.go (B4 coverage 2026-08-31): hub broadcast RBAC/tenant-filter,
// bridge NATS→WS (telemetry/notify/media), healthHandler, Setup/Shutdown E2E
// dengan embedded NATS + miniredis — tanpa infra eksternal.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"ajb_gps/internal"
	"ajb_gps/internal/tenant"
	"ajb_gps/service-websocket/models"

	"github.com/alicebob/miniredis/v2"
	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

// startMiniredis boots an in-memory Redis for tests (auto-closed via t.Cleanup).
func startMiniredis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	return miniredis.RunT(t)
}

// ---------------------------------------------------------------------------
// fixture
// ---------------------------------------------------------------------------

// wsFixture sets package globals for bridge/health tests and restores them.
func wsFixture(t *testing.T) *hub {
	t.Helper()
	mr := startMiniredis(t)
	cfg := internal.LoadConfig()
	cfg.Redis.Addr = mr.Addr()
	red, err := internal.NewRedisClient(cfg, nil, nil)
	if err != nil {
		t.Fatalf("NewRedisClient: %v", err)
	}
	appCfg = cfg
	appRedis = red
	appNATS = nil
	appTenant = &tenant.Manager{}
	appHub = newHub(8, 8)
	vehReg = newVehicleRegistry()
	// Pre-seed cache registry: DEV001:1 → vehicle 7 (skip DB fetch).
	vehReg.cache[registryKey("DEV001", "1")] = registryEntry{
		info:   vehicleInfo{ID: 7, Model: "GT06", Plate: "B 1234 XYZ"},
		expire: time.Now().Add(5 * time.Minute),
	}
	t.Cleanup(func() {
		appCfg, appRedis, appNATS, appTenant = nil, nil, nil, nil
		appHub, vehReg = nil, nil
		appSub, appNotifySub, appMediaSub = nil, nil, nil
	})
	return appHub
}

// fakeWSConn records writes without a real socket.
type fakeWSConn struct {
	mu     sync.Mutex
	writes [][]byte
	closed bool
}

func (f *fakeWSConn) WriteMessage(_ int, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, append([]byte(nil), data...))
	return nil
}

func (f *fakeWSConn) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakeWSConn) writeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.writes)
}

func (f *fakeWSConn) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

func mkClient(t *testing.T, h *hub, company string, allowAll bool, allowed ...uint64) (*client, *fakeWSConn) {
	t.Helper()
	set := make(map[uint64]struct{})
	for _, id := range allowed {
		set[id] = struct{}{}
	}
	conn := &fakeWSConn{}
	cl := newClient(conn, make(chan []byte, 16), 42, company, set, allowAll, h)
	if !h.register(cl) {
		t.Fatal("register failed")
	}
	return cl, conn
}

func awaitPayload(t *testing.T, cl *client) []byte {
	t.Helper()
	select {
	case p := <-cl.send:
		return p
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting payload")
		return nil
	}
}

func noPayload(cl *client) bool {
	select {
	case <-cl.send:
		return false
	default:
		return true
	}
}

// ---------------------------------------------------------------------------
// hub: register/capacity/broadcast matrix/enqueue drop/close
// ---------------------------------------------------------------------------

func TestHub_RegisterCapacityBroadcastMatrix(t *testing.T) {
	// --- Kapasitas & duplikat pada hub kecil (maxConn 2). ---
	hcap := newHub(2, 8)
	c1, conn1 := mkClient(t, hcap, "DEV001", true)
	mkClient(t, hcap, "DEV001", false, 7)
	c3 := newClient(&fakeWSConn{}, make(chan []byte, 4), 43, "DEV001",
		map[uint64]struct{}{7: {}}, false, hcap)
	if hcap.register(c3) {
		t.Fatal("capacity 2 harus menolak klien ke-3")
	}
	// Duplikat register ditolak.
	if hcap.register(c1) {
		t.Fatal("duplicate register harus ditolak")
	}
	if hcap.count() != 2 {
		t.Fatalf("count = %d, want 2", hcap.count())
	}

	// --- Broadcast matrix pada hub berkapasitas cukup. ---
	h := newHub(8, 8)
	admin, _ := mkClient(t, h, "DEV001", true)
	foreign, _ := mkClient(t, h, "QA001", true)
	unallowed, _ := mkClient(t, h, "DEV001", false, 8)
	subbed, _ := mkClient(t, h, "DEV001", false, 7)
	subbed.subs = map[uint64]struct{}{8: {}} // SUBSCRIBE eksplisit beda vehicle
	h.broadcast("dev001", 7, []byte(`{"x":1}`))

	if p := awaitPayload(t, admin); !strings.Contains(string(p), `"x":1`) {
		t.Fatalf("admin payload = %s", p)
	}
	if !noPayload(foreign) {
		t.Fatal("cross-tenant client TIDAK boleh menerima")
	}
	if !noPayload(unallowed) {
		t.Fatal("client tanpa akses vehicle 7 TIDAK boleh menerima")
	}
	if !noPayload(subbed) {
		t.Fatal("subs eksplisit ke vehicle 8 tidak boleh menerima vehicle 7")
	}

	// canReceive: allowAll menerima semua.
	if !admin.canReceive(999) {
		t.Fatal("allowAll harus menerima semua vehicle")
	}

	// Enqueue drop-oldest (FR-5.4): chan 1 slot, 3 enqueue → tersisa terbaru.
	tiny := newClient(&fakeWSConn{}, make(chan []byte, 1), 44, "DEV001",
		map[uint64]struct{}{7: {}}, true, h)
	tiny.enqueue([]byte("a"))
	tiny.enqueue([]byte("b"))
	tiny.enqueue([]byte("c"))
	if p := <-tiny.send; string(p) != "c" {
		t.Fatalf("queue penuh harus drop-oldest, got %q", p)
	}
	select {
	case p := <-tiny.send:
		t.Fatalf("expected single payload, got extra %q", p)
	default:
	}

	// close idempotent + unregister + closeAll.
	admin.close()
	admin.close()
	h.unregister(admin)
	if h.count() != 3 {
		t.Fatalf("count after unregister = %d, want 3", h.count())
	}
	h.closeAll()

	// close menutup conn (cek pada klien hub kecil).
	c1.close()
	c1.close()
	if !conn1.closed {
		t.Fatal("close harus menutup conn")
	}
	_ = c3
}

func TestClient_CanReceiveSubsVsAllowed(t *testing.T) {
	h := newHub(4, 4)
	cl := newClient(&fakeWSConn{}, make(chan []byte, 4), 1, "DEV001",
		map[uint64]struct{}{5: {}, 6: {}}, false, h)
	if !cl.canReceive(5) || !cl.canReceive(6) {
		t.Fatal("allowed set harus diterima")
	}
	if cl.canReceive(7) {
		t.Fatal("vehicle di luar allowed harus ditolak")
	}
	cl.subs[9] = struct{}{}
	if cl.canReceive(5) || cl.canReceive(6) {
		t.Fatal("subs eksplisit harus menggantikan allowed set")
	}
	if !cl.canReceive(9) {
		t.Fatal("vehicle dalam subs harus diterima")
	}
}

// ---------------------------------------------------------------------------
// bridgeHandle — telemetry.raw → VEHICLE_UPDATE
// ---------------------------------------------------------------------------

func TestBridgeHandle_FanoutAndGuards(t *testing.T) {
	h := wsFixture(t)
	admin, _ := mkClient(t, h, "DEV001", true)
	foreign, _ := mkClient(t, h, "QA001", true)

	// Valid: admin DEV001 menerima VEHICLE_UPDATE vehicle 7.
	bridgeHandle(&nats.Msg{Subject: "telemetry.raw.1", Data: []byte(
		`{"imei":"1","company_code":"DEV001","speed":40,"lat":-6.2,"lon":106.8,"battery":88,"timestamp":1750000000}`)})
	p := awaitPayload(t, admin)
	if !strings.Contains(string(p), `"VEHICLE_UPDATE"`) || !strings.Contains(string(p), `"plate_number":"B 1234 XYZ"`) {
		t.Fatalf("unexpected payload: %s", p)
	}
	var ev models.VehicleUpdateEvent
	if err := json.Unmarshal(p, &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ev.Data.VehicleID != 7 || ev.Data.Status != "MOVING" || !ev.Data.Acc {
		t.Fatalf("unexpected data: %+v", ev.Data)
	}
	if !noPayload(foreign) {
		t.Fatal("cross-tenant tidak boleh menerima")
	}

	// IMEI tak dikenal (anti-leak: fetch DB gagal → negative cache) → tidak push.
	bridgeHandle(&nats.Msg{Subject: "telemetry.raw.9", Data: []byte(
		`{"imei":"999","company_code":"DEV001","speed":40}`)})
	if !noPayload(admin) {
		t.Fatal("unknown IMEI tidak boleh di-push")
	}

	// Guard: IMEI kosong, JSON rusak — tidak panic, tidak push.
	bridgeHandle(&nats.Msg{Subject: "telemetry.raw.x", Data: []byte(`{"speed":1}`)})
	bridgeHandle(&nats.Msg{Subject: "telemetry.raw.x", Data: []byte(`{broken`)})
	if !noPayload(admin) {
		t.Fatal("guard harus skip tanpa push")
	}
}

// ---------------------------------------------------------------------------
// notifyHandle — notify.alert.<vehicle_id> → ALERT_NOTIFICATION
// ---------------------------------------------------------------------------

func TestNotifyHandle_SubjectTrustAndGuards(t *testing.T) {
	h := wsFixture(t)
	admin, _ := mkClient(t, h, "DEV001", true)

	// vehicle_id di subject (7) menimpa payload (99) — authoritative target.
	body, _ := json.Marshal(models.AlertNotification{
		AlertID: "a1", VehicleID: 99, CompanyCode: "DEV001",
		AlertType: "OVERSPEEDING", Severity: "critical", Status: "OPEN",
	})
	notifyHandle(&nats.Msg{Subject: "notify.alert.7", Data: body})
	p := awaitPayload(t, admin)
	var ev models.AlertNotificationEvent
	if err := json.Unmarshal(p, &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ev.Event != "ALERT_NOTIFICATION" || ev.Data.VehicleID != 7 || ev.Data.AlertID != "a1" {
		t.Fatalf("unexpected event: %+v", ev)
	}

	// Guard: subject suffix bukan angka, JSON rusak → tidak panic.
	notifyHandle(&nats.Msg{Subject: "notify.alert.abc", Data: body})
	notifyHandle(&nats.Msg{Subject: "notify.alert.7", Data: []byte(`{broken`)})
	if !noPayload(admin) {
		t.Fatal("guard harus skip tanpa push")
	}
}

// ---------------------------------------------------------------------------
// mediaHandle — media.event.<company> → MEDIA_EVENT
// ---------------------------------------------------------------------------

func TestMediaHandle_FanoutAndGuards(t *testing.T) {
	h := wsFixture(t)
	admin, _ := mkClient(t, h, "DEV001", true)

	body, _ := json.Marshal(models.MediaEventData{
		MediaID: 3, VehicleID: 7, IMEI: "1", CompanyCode: "DEV001",
		MediaType: "image/jpeg", TriggerType: "sos", Status: "available",
	})
	mediaHandle(&nats.Msg{Subject: "media.event.DEV001", Data: body})
	p := awaitPayload(t, admin)
	var ev models.MediaEventWS
	if err := json.Unmarshal(p, &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ev.Event != "MEDIA_EVENT" || ev.Data.MediaID != 3 || ev.Data.VehicleID != 7 {
		t.Fatalf("unexpected event: %+v", ev)
	}

	// Guard: company_code kosong + JSON rusak → skip.
	mediaHandle(&nats.Msg{Subject: "media.event.X", Data: []byte(`{"media_id":1}`)})
	mediaHandle(&nats.Msg{Subject: "media.event.X", Data: []byte(`{broken`)})
	if !noPayload(admin) {
		t.Fatal("guard harus skip tanpa push")
	}
}

// ---------------------------------------------------------------------------
// healthHandler — ok vs degraded
// ---------------------------------------------------------------------------

func TestHealthHandler_OKAndDegraded(t *testing.T) {
	wsFixture(t) // appTenant zero (Health nil-safe), appRedis miniredis, appNATS nil

	// NATS nil → degraded 503.
	rec := httptest.NewRecorder()
	healthHandler(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("tanpa NATS status = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "nats:down") {
		t.Fatalf("body = %s", rec.Body.String())
	}

	// NATS nyata (embedded) → semua ok → 200.
	cfg := internal.LoadConfig()
	cfg.NATS.URL = startTestNATSServer(t)
	nac, err := internal.NewNATSClient(cfg, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewNATSClient: %v", err)
	}
	appNATS = nac

	rec2 := httptest.NewRecorder()
	healthHandler(rec2, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec2.Code != http.StatusOK {
		t.Fatalf("dengan NATS status = %d, want 200; body=%s", rec2.Code, rec2.Body.String())
	}
	for _, want := range []string{"mysql:ok", "redis:ok", "nats:ok"} {
		if !strings.Contains(rec2.Body.String(), want) {
			t.Fatalf("body %q missing %q", rec2.Body.String(), want)
		}
	}
}

// ---------------------------------------------------------------------------
// Setup + Shutdown — subscribe nyata 3 bridge + publish raw E2E
// ---------------------------------------------------------------------------

func startTestNATSServer(t *testing.T) string {
	t.Helper()
	opts := &natsserver.Options{
		Port: -1, Host: "127.0.0.1", JetStream: true, StoreDir: t.TempDir(),
	}
	s, err := natsserver.NewServer(opts)
	if err != nil {
		t.Fatalf("nats server: %v", err)
	}
	go s.Start()
	if !s.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats server not ready")
	}
	t.Cleanup(func() { s.Shutdown() })
	return s.ClientURL()
}

func TestSetupShutdownAndRawBridgeE2E(t *testing.T) {
	wsFixture(t)

	cfg := internal.LoadConfig()
	cfg.NATS.URL = startTestNATSServer(t)
	nac, err := internal.NewNATSClient(cfg, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewNATSClient: %v", err)
	}
	appNATS = nac

	unsub, err := Setup(cfg, appRedis, nac, nil, appTenant)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if unsub == nil {
		t.Fatal("Setup harus mengembalikan unsubscribe")
	}
	if err := nac.Flush(2 * time.Second); err != nil {
		t.Fatal(err)
	}

	// Setup MENGGANTI appHub & vehReg dgn instance produksi baru — klien dan
	// registry seed harus ditanam SETELAH Setup (bukan pada hub fixture lama).
	h2 := appHub
	admin, conn := mkClient(t, h2, "DEV001", true)
	seedRegistry(vehReg, "DEV001", "1",
		vehicleInfo{ID: 7, Model: "GT06", Plate: "B 1234 XYZ"})

	// Publish telemetry raw → bridge → hub → klien admin.
	if err := nac.Publish(appNATS.Subject("raw", "1"), []byte(
		`{"imei":"1","company_code":"DEV001","speed":25,"lat":-6.2,"lon":106.8,"timestamp":1750000000}`)); err != nil {
		t.Fatal(err)
	}
	p := awaitPayload(t, admin)
	if !strings.Contains(string(p), `"VEHICLE_UPDATE"`) {
		t.Fatalf("payload = %s", p)
	}

	// idempotent-ish: unsubscribe twice + Shutdown (hub closeAll + tenant close).
	unsub()
	unsub()
	Shutdown()
	if !conn.isClosed() {
		t.Fatal("Shutdown harus menutup koneksi klien (closeAll)")
	}
	_ = admin
}