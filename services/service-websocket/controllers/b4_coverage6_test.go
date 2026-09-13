package controllers

// b4_coverage6_test.go (B4 coverage 2026-09-04 — Stage D):
// readPump/writePump via pasangan WebSocket nyata (httptest + gorilla dialer),
// enqueueWS, Router(), fetchByID, loadAuthUserID.

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"ajb_gps/internal"
	"ajb_gps/service-websocket/models"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gorilla/websocket"
)

// wsConnPair builds a real WebSocket pair: server (returned first) + client.
func wsConnPair(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()
	up := websocket.Upgrader{}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	serverCh := make(chan *websocket.Conn, 1)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		serverCh <- c
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		_ = srv.Close()
		_ = ln.Close()
	})

	dialer := websocket.Dialer{}
	cc, _, err := dialer.Dial("ws://"+ln.Addr().String()+"/", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = cc.Close() })
	sc := <-serverCh
	t.Cleanup(func() { _ = sc.Close() })
	return sc, cc
}

func wsTestCfg() *internal.Config {
	cfg := internal.LoadConfig()
	cfg.WebSocket.PongWait = time.Second
	cfg.WebSocket.WriteWait = time.Second
	cfg.WebSocket.HeartbeatInterval = 20 * time.Millisecond
	cfg.WebSocket.MaxMessageSize = 1024
	return cfg
}

// ---------------------------------------------------------------------------
// readPump — SUBSCRIBE / UNSUBSCRIBE / invalid / close
// ---------------------------------------------------------------------------

func TestClientReadPump_SubscribeAndClose(t *testing.T) {
	sc, cc := wsConnPair(t)
	cfg := wsTestCfg()
	cl := newClient(sc, make(chan []byte, 16), 1, "DEV001", map[uint64]struct{}{42: {}}, true, nil)

	done := make(chan struct{})
	go func() { cl.readPump(cfg); close(done) }()

	if err := cc.WriteMessage(websocket.TextMessage, []byte(`{"event":"SUBSCRIBE","data":{"vehicle_id":42}}`)); err != nil {
		t.Fatalf("write subscribe: %v", err)
	}
	payload := recvChan(t, cl.send)
	var ev models.ConnectionEvent
	if err := json.Unmarshal(payload, &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ev.Event != "SUBSCRIBED" {
		t.Fatalf("expected SUBSCRIBED, got %s", ev.Event)
	}

	// Invalid JSON → ignored (no payload).
	_ = cc.WriteMessage(websocket.TextMessage, []byte(`not-json`))
	// UNSUBSCRIBE → no crash.
	_ = cc.WriteMessage(websocket.TextMessage, []byte(`{"event":"UNSUBSCRIBE","data":{"vehicle_id":42}}`))

	// Close → readPump returns.
	if err := cc.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseGoingAway, "")); err != nil {
		t.Fatalf("write close: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("readPump did not return after close")
	}
}

func TestClientReadPump_UnauthorizedVehicle(t *testing.T) {
	sc, cc := wsConnPair(t)
	cfg := wsTestCfg()
	cl := newClient(sc, make(chan []byte, 16), 2, "DEV001", map[uint64]struct{}{42: {}}, false, nil)

	done := make(chan struct{})
	go func() { cl.readPump(cfg); close(done) }()

	// Vehicle 99 is NOT in allowed set → UNAUTHORIZED_VEHICLE error.
	if err := cc.WriteMessage(websocket.TextMessage, []byte(`{"event":"SUBSCRIBE","data":{"vehicle_id":99}}`)); err != nil {
		t.Fatalf("write subscribe: %v", err)
	}
	payload := recvChan(t, cl.send)
	var ev models.WsErrorEvent
	if err := json.Unmarshal(payload, &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ev.ErrorCode != "UNAUTHORIZED_VEHICLE" {
		t.Fatalf("expected UNAUTHORIZED_VEHICLE, got %s", ev.ErrorCode)
	}

	_ = cc.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("readPump did not return")
	}
}

// ---------------------------------------------------------------------------
// writePump — payload + ping + done
// ---------------------------------------------------------------------------

func TestClientWritePump_PayloadPingDone(t *testing.T) {
	sc, cc := wsConnPair(t)
	cfg := wsTestCfg()
	cl := newClient(sc, make(chan []byte, 8), 1, "DEV001", nil, true, nil)

	go cl.writePump(cfg)

	// Text payload flows to the peer.
	cl.enqueue([]byte(`{"event":"TEST"}`))
	if err := cc.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	mt, data, err := cc.ReadMessage()
	if err != nil {
		t.Fatalf("read text: %v", err)
	}
	if mt != websocket.TextMessage || string(data) != `{"event":"TEST"}` {
		t.Fatalf("unexpected message: mt=%d data=%s", mt, data)
	}

	// Heartbeat ping (20ms ticker) — gorilla menangani ping internal, jadi
	// verifikasi via handler callback (bukan ReadMessage); perlu satu reader
	// aktif agar control frame diproses.
	pingCh := make(chan struct{}, 1)
	cc.SetPingHandler(func(string) error {
		select {
		case pingCh <- struct{}{}:
		default:
		}
		return nil
	})
	go func() {
		_ = cc.SetReadDeadline(time.Now().Add(5 * time.Second))
		for {
			if _, _, err := cc.ReadMessage(); err != nil {
				return
			}
		}
	}()
	select {
	case <-pingCh:
	case <-time.After(2 * time.Second):
		t.Fatal("no heartbeat ping received")
	}

	// done → writePump closes conn; reader goroutine di atas akan keluar
	// dengan error koneksi tertutup.
	cl.close()
	time.Sleep(100 * time.Millisecond)
}

// ---------------------------------------------------------------------------
// enqueueWS / Router / fetchByID / loadAuthUserID
// ---------------------------------------------------------------------------

func TestEnqueueWS(t *testing.T) {
	cl := newClient(&fakeWSConn{}, make(chan []byte, 4), 1, "DEV001", nil, true, nil)
	if err := cl.enqueueWS([]byte(`{"event":"X"}`)); err != nil {
		t.Fatalf("enqueueWS: %v", err)
	}
	if got := string(recvChan(t, cl.send)); got != `{"event":"X"}` {
		t.Fatalf("unexpected payload: %s", got)
	}
}

func TestRouter(t *testing.T) {
	oldCfg := appCfg
	oldLimiter := loginLimiter
	oldAPILimiter := apiLimiter
	defer func() {
		appCfg = oldCfg
		loginLimiter = oldLimiter
		apiLimiter = oldAPILimiter
	}()
	appCfg = internal.LoadConfig()
	appCfg.HTTP.CORSOrigins = []string{"*"}
	loginLimiter = newFailureRateLimiter(5, 15*time.Minute)
	apiLimiter = newAPIRateLimiter(100)

	h := Router()
	if h == nil {
		t.Fatal("Router() returned nil")
	}
}

func TestFetchByID(t *testing.T) {
	db, m := mockDB(t)
	m.ExpectQuery(`SELECT id, name, waypoints, estimated_duration_sec, created_by, is_active, created_at, updated_at
FROM routes WHERE id = \? AND is_active = TRUE`).
		WithArgs(uint64(1)).
		WillReturnRows(sqlmock.NewRows(strings.Split(routeListCols, ",")).
			AddRow(routeRowVals(1, "R1")...))
	r, err := fetchByID(db, 1)
	if err != nil || r.ID != 1 {
		t.Fatalf("fetchByID: r=%+v err=%v", r, err)
	}
}

func TestLoadAuthUserID(t *testing.T) {
	c, _ := ginCtx(http.MethodGet, "/api/v1/vehicles")
	if got := loadAuthUserID(c); got != 0 {
		t.Fatalf("expected 0 without user, got %d", got)
	}
	c.Set(ctxUserKey, models.AuthUser{ID: 7})
	if got := loadAuthUserID(c); got != 7 {
		t.Fatalf("expected 7, got %d", got)
	}
}
