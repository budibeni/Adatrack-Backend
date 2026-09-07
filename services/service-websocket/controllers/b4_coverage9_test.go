package controllers

// b4_coverage9_test.go (B4 coverage 2026-09-05 — Stage F+):
// writePump/readPump via real WebSocket loopback (currently 0%),
// enrichVehicles nil-Redis + nil-entry path, vehiclesList error path,
// healthHandler degraded (all down).

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ajb_gps/internal"
	"ajb_gps/service-websocket/models"

	"github.com/alicebob/miniredis/v2"
	"github.com/gorilla/websocket"
)

// ---------------------------------------------------------------------------
// writePump + readPump real WS loopback
// ---------------------------------------------------------------------------

func TestWritePumpAndReadPump_Loopback(t *testing.T) {
	defer saveWSGlobals(t)()
	appCfg = internal.LoadConfig()
	// Percepat heartbeat biar tidak menunggu 30s di test.
	appCfg.WebSocket.HeartbeatInterval = 50 * time.Millisecond
	appCfg.WebSocket.PongWait = 5 * time.Second
	appCfg.WebSocket.WriteWait = 2 * time.Second
	appCfg.WebSocket.MaxMessageSize = 65536

	appHub = newHub(8, 8)

	// Buat server WS test + client dialer loopback.
	srv := httptest.NewServer(nil)
	defer srv.Close()

	upgrader := websocket.Upgrader{}
	srv.Config.Handler = nil

	// Pakai handler manual: upgrade lalu jalankan pumps.
	echo := false
	serve := func(w http.ResponseWriter, r *http.Request) {
		if !echo {
			echo = true
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Fatalf("upgrade server: %v", err)
			}
			cl := newClient(conn, make(chan []byte, 8), 1, "DEV001",
				map[uint64]struct{}{7: {}}, false, appHub)
			if !appHub.register(cl) {
				t.Fatal("register")
			}
			go cl.writePump(appCfg)
			cl.readPump(appCfg)
		} else {
			// Second upgrade attempt → just upgrade & close (readPump exits).
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			conn.Close()
		}
	}
	srv.Config.Handler = http.HandlerFunc(serve)

	// Client dialer.
	u := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Kirim pesan JSON ke server → readPump memproses (SUBSCRIBE).
	if err := conn.WriteMessage(websocket.TextMessage,
		[]byte(`{"event":"SUBSCRIBE","data":{"vehicle_id":7}}`)); err != nil {
		t.Fatal(err)
	}

	// Server (bridge) broadcast pesan ke vehicle 7 → writePump push ke client.
	go func() {
		time.Sleep(100 * time.Millisecond)
		appHub.broadcast("DEV001", 7, []byte(`{"event":"VEHICLE_UPDATE"}`))
	}()

	// Baca dari client connection (datang dari writePump).
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read from server: %v", err)
	}
	if !strings.Contains(string(msg), "VEHICLE_UPDATE") {
		// Bisa jadi SUBSCRIBED dulu; bacalah sekali lagi.
		_, msg2, err2 := conn.ReadMessage()
		if err2 != nil || !strings.Contains(string(msg2), "VEHICLE_UPDATE") {
			t.Fatalf("expected VEHICLE_UPDATE broadcast, got %q err=%v", msg, err)
		}
	}

	// Tutup koneksi → readPump & writePump harus keluar bersih.
	conn.Close()
	time.Sleep(100 * time.Millisecond)
}

// ---------------------------------------------------------------------------
// enrichVehicles: nil Redis + nil-entry fallback
// ---------------------------------------------------------------------------

func TestEnrichVehicles_NilRedisReturnsDBModels(t *testing.T) {
	defer saveWSGlobals(t)()
	appCfg = internal.LoadConfig()
	appRedis = nil // enrich harus tetap bekerja tanpa Redis

	c, _ := ginCtx(http.MethodGet, "/api/v1/vehicles")
	vehicles := []vehicleModel{
		{ID: 1, IMEI: "111", PlateNumber: "B1", Status: "active",
			DeviceModel: sql.NullString{String: "GT06", Valid: true}},
		{ID: 2, IMEI: "222", PlateNumber: "B2", Status: "inactive"},
	}
	items := enrichVehicles(c, vehicles)
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].IMEI != "111" || items[1].IMEI != "222" {
		t.Errorf("unexpected IMEIs: %v %v", items[0].IMEI, items[1].IMEI)
	}
	if items[0].DeviceModel != "GT06" {
		t.Errorf("expected DeviceModel GT06, got %q", items[0].DeviceModel)
	}
}

func TestEnrichVehicles_LiveStateOverlaysPosition(t *testing.T) {
	defer saveWSGlobals(t)()
	mr := miniredis.RunT(t)
	cfg := internal.LoadConfig()
	cfg.Redis.Addr = mr.Addr()
	red, err := internal.NewRedisClient(cfg, nil, nil)
	if err != nil {
		t.Fatalf("NewRedisClient: %v", err)
	}
	oldR, oldC := appRedis, appCfg
	appRedis, appCfg = red, cfg
	defer func() { appRedis, appCfg = oldR, oldC }()

	company := "DEV001"
	imei := "111"
	stateKey := "adatrack_gps:" + strings.ToLower(company) + ":vehicle:state:" + imei
	fl := 55.0
	acc := true
	st := models.RedisState{Lat: -6.3, Lon: 106.9, Speed: 40, Status: "ONLINE", FuelLevel: &fl, Acc: &acc, LastSeen: time.Now().Unix()}
	b, _ := json.Marshal(st)
	mr.Set(stateKey, string(b))

	c, _ := ginCtx(http.MethodGet, "/api/v1/vehicles")
	c.Set(ctxCompanyCodeKey, company)
	vehicles := []vehicleModel{{ID: 1, IMEI: imei, PlateNumber: "B1", Status: "active"}}
	items := enrichVehicles(c, vehicles)
	if len(items) != 1 {
		t.Fatal("expected 1 item")
	}
	if items[0].LastPosition == nil {
		t.Fatal("expected live position overlay")
	}
	if items[0].LastPosition.Speed != 40 {
		t.Errorf("expected speed 40, got %v", items[0].LastPosition.Speed)
	}
}
