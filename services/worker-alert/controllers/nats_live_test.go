package controllers

// nats_live_test.go (B4 coverage 2026-08-31): jalur yang butuh NATS nyata —
// publishAlert success (alert.<type>.<company>), publishCaptureRequest (critical),
// dispatch websocket → notify.alert.<vehicle_id>, publishSOSEscalation.
// Memakai embedded NATS server (JetStream off) — cepat & offline aman.

import (
	"testing"
	"time"

	"ajb_gps/internal"
	"ajb_gps/internal/tenant"
	"ajb_gps/worker-alert/models"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

func startTestNATS(t *testing.T) string {
	t.Helper()
	opts := &natsserver.Options{
		Port:      -1,
		Host:      "127.0.0.1",
		JetStream: true,
		StoreDir:  t.TempDir(),
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

func newWANATS(t *testing.T) (*WorkerAlert, *nats.Conn) {
	t.Helper()
	url := startTestNATS(t)
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	t.Cleanup(func() { nc.Close() })
	cfg := internal.LoadConfig()
	cfg.NATS.URL = url
	nac, err := internal.NewNATSClient(cfg, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewNATSClient: %v", err)
	}
	wa, err := New(cfg, nil, nac, nil, nil)
	if err != nil {
		t.Fatalf("New worker-alert: %v", err)
	}
	return wa, nc
}

func TestPublishAlert_SuccessAndCapture(t *testing.T) {
	wa, nc := newWANATS(t)
	// Subscribe ke subject yang diharapkan.
	sub, err := nc.SubscribeSync("alert.speed.dev001")
	if err != nil {
		t.Fatal(err)
	}
	captureSub, err := nc.SubscribeSync("media.capture.request.dev001")
	if err != nil {
		t.Fatal(err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}

	rec := models.AlertRecord{
		ID: 7, VehicleID: 3, AlertType: models.AlertTypeSpeed,
		Severity: "warning", Status: "open", Description: "overspeed di Jl. Sudirman",
	}
	lat, lon := -6.20, 106.80
	rec.VehicleLat, rec.VehicleLon = &lat, &lon
	wa.publishAlert("DEV001", "864201040512345", rec, tm("864201040512345", nil, 95, -6.20, 106.80, 0))

	msg, err := sub.NextMsg(2 * time.Second)
	if err != nil {
		t.Fatalf("alert.speed.dev001 tidak terima: %v", err)
	}
	if string(msg.Data) == "" {
		t.Fatal("payload kosong")
	}

	// Severity warning → TIDAK memicu capture request.
	if n, _ := captureSub.NextMsg(200 * time.Millisecond); n != nil {
		t.Fatal("warning tidak boleh memicu capture request")
	}

	// Sementara critical → publish + capture.
	sosSub, err := nc.SubscribeSync("alert.sos.dev001")
	if err != nil {
		t.Fatal(err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
	rec2 := models.AlertRecord{
		ID: 8, VehicleID: 3, AlertType: models.AlertTypeSOS,
		Severity: "critical", Status: "open", Description: "SOS dari device",
	}
	wa.publishAlert("DEV001", "864201040512345", rec2, tm("864201040512345", nil, 0, -6.20, 106.80, 0))
	if _, err := sosSub.NextMsg(2 * time.Second); err != nil {
		t.Fatalf("alert.sos.dev001 tidak terima: %v", err)
	}
	c, err := captureSub.NextMsg(2 * time.Second)
	if err != nil {
		t.Fatalf("critical harus memicu media.capture.request: %v", err)
	}
	if string(c.Data) == "" {
		t.Fatal("capture payload kosong")
	}
}

func TestDispatch_WebSocketPublishesNotify(t *testing.T) {
	wa, nc := newWANATS(t)
	sub, err := nc.SubscribeSync("notify.alert.3")
	if err != nil {
		t.Fatal(err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
	f := &fakeStore{}
	rec := models.AlertRecord{ID: 5, VehicleID: 3, AlertType: models.AlertTypeGeofence, Severity: "warning", Description: "masuk depot"}
	wa.dispatch(f, "DEV001", 1, models.NotifPreference{UserID: 1, Channel: "websocket", MinSeverity: "info"}, rec,
		tm("i", nil, 0, -6.2, 106.8, 0))
	msg, err := sub.NextMsg(2 * time.Second)
	if err != nil {
		t.Fatalf("notify.alert.3 tidak terima: %v", err)
	}
	if len(f.notifs) != 1 || f.notifs[0].status != "sent" || f.notifs[0].channel != "websocket" {
		t.Fatalf("audit websocket = %+v", f.notifs)
	}
	_ = msg
}

func TestPublishSOSEscalation_Success(t *testing.T) {
	wa, nc := newWANATS(t)
	wa.tm = &tenant.Manager{} // imeiForVehicle → newStore error → imei kosong; publish tetap jalan
	sub, err := nc.SubscribeSync("alert.sos.dev001")
	if err != nil {
		t.Fatal(err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
	entry := models.OpenSOSAlert{ID: 1, VehicleID: 3}
	wa.publishSOSEscalation("DEV001", entry, 2, 2*time.Minute)
	msg, err := sub.NextMsg(2 * time.Second)
	if err != nil {
		t.Fatalf("alert.sos.dev001 escalation tidak terima: %v", err)
	}
	if len(msg.Data) == 0 {
		t.Fatal("escalation payload kosong")
	}
}

func TestPublishAlert_NoNATSSubscribers(t *testing.T) {
	wa, _ := newWANATS(t)
	rec := models.AlertRecord{ID: 1, VehicleID: 3, AlertType: models.AlertTypeBattery, Severity: "medium", Status: "open"}
	wa.publishAlert("DEV001", "i", rec, tm("i", nil, 0, 0, 0, 0)) // no error, incPublished
}