package controllers

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"ajb_gps/service-websocket/models"

	"github.com/nats-io/nats.go"
)

// ---------------------------------------------------------------------------
// Hotfix ACC Status WebSocket (B6):
//   bridgeHandle harus membaca nilai ACC riil dari payload telemetry.raw.<IMEI>
//   (tm.ACC), BUKAN menginferensikan Speed > 0 — serta meneruskan fuel_level/
//   fuel_volume/fuel_temp_c/satellites/gsm_signal ke DTO VEHICLE_UPDATE.
// ---------------------------------------------------------------------------

// bridgeTestEnv wires package globals (vehReg + appHub) with a registered
// DEV001 client (access to vehicle 42) so bridgeHandle runs without infra.
func bridgeTestEnv(t *testing.T) (*client, *fakeWSConn) {
	t.Helper()
	vehReg = newVehicleRegistry()
	// Pre-populate cache agar lookup tidak menyentuh DB (anti-spoofing bypass).
	vehReg.cache[registryKey("DEV001", "864201040512345")] = registryEntry{
		info:   vehicleInfo{ID: 42, Model: "GT06", Plate: "B 1234 CD"},
		expire: time.Now().Add(5 * time.Minute),
	}
	appHub = newHub(100, 100)
	return mkClient(t, appHub, "DEV001", false, 42)
}

func bridgeTelemetryMsg(t *testing.T, payload []byte) *nats.Msg {
	t.Helper()
	return &nats.Msg{Subject: "telemetry.raw.864201040512345", Data: payload}
}

// Idling (ACC on, Speed == 0) — skenario inti hotfix: sebelum fix, acc=false;
// sesudah fix, acc=true (mesin menyala, kendaraan berhenti) dengan status IDLE.
func TestBridgeHandleUsesRealACCWhenIdling(t *testing.T) {
	cl, _ := bridgeTestEnv(t)

	msg := bridgeTelemetryMsg(t, []byte(`{
		"imei":"864201040512345","company_code":"DEV001","vehicle_id":11,
		"lat":-6.2,"lon":106.8,"speed":0,"heading":90,"satellites":9,"hdop":0.9,
		"battery_level":4,"gsm_signal":3,"acc":true,"mileage":120000,"fix":true,
		"timestamp":1722000000
	}`))
	if err := bridgeHandle(msg); err != nil {
		t.Fatalf("bridgeHandle unexpected error: %v", err)
	}

	var ev models.VehicleUpdateEvent
	if err := json.Unmarshal(awaitPayload(t, cl), &ev); err != nil {
		t.Fatalf("unmarshal VEHICLE_UPDATE: %v", err)
	}
	if !ev.Data.Acc {
		t.Errorf("expected acc=true for idling vehicle (ACC on, speed=0), got false")
	}
	if ev.Data.Status != "IDLE" {
		t.Errorf("expected status IDLE, got %s", ev.Data.Status)
	}
}

// Coasting/ignition off: ACC=false harus diteruskan walau speed > 0.
func TestBridgeHandleUsesRealACCWhenMoving(t *testing.T) {
	cl, _ := bridgeTestEnv(t)

	msg := bridgeTelemetryMsg(t, []byte(`{
		"imei":"864201040512345","company_code":"DEV001","vehicle_id":11,
		"lat":-6.2,"lon":106.8,"speed":45.5,"heading":90,"acc":false,
		"timestamp":1722000001
	}`))
	if err := bridgeHandle(msg); err != nil {
		t.Fatalf("bridgeHandle unexpected error: %v", err)
	}

	var ev models.VehicleUpdateEvent
	if err := json.Unmarshal(awaitPayload(t, cl), &ev); err != nil {
		t.Fatalf("unmarshal VEHICLE_UPDATE: %v", err)
	}
	if ev.Data.Acc {
		t.Errorf("expected acc=false (ignition off), got true")
	}
	if ev.Data.Status != "MOVING" {
		t.Errorf("expected status MOVING, got %s", ev.Data.Status)
	}
}

// Fuel sensor & kualitas sinyal diteruskan ke DTO (B5a data sampai dashboard).
func TestBridgeHandlePassesFuelAndSignalToDTO(t *testing.T) {
	cl, _ := bridgeTestEnv(t)

	fuel := 62.5
	vol := 40.25
	temp := 32.0
	msg := bridgeTelemetryMsg(t, []byte(`{
		"imei":"864201040512345","company_code":"DEV001","vehicle_id":11,
		"lat":-6.2,"lon":106.8,"speed":12,"heading":270,"satellites":11,"hdop":0.8,
		"battery_level":4,"gsm_signal":4,"acc":true,"mileage":120042,"fix":true,
		"fuel_level":62.5,"fuel_volume":40.25,"fuel_temp_c":32,
		"timestamp":1722000002
	}`))
	if err := bridgeHandle(msg); err != nil {
		t.Fatalf("bridgeHandle unexpected error: %v", err)
	}

	var ev models.VehicleUpdateEvent
	if err := json.Unmarshal(awaitPayload(t, cl), &ev); err != nil {
		t.Fatalf("unmarshal VEHICLE_UPDATE: %v", err)
	}
	if ev.Data.FuelLevel == nil || *ev.Data.FuelLevel != fuel {
		t.Errorf("expected fuel_level %v, got %v", fuel, ev.Data.FuelLevel)
	}
	if ev.Data.FuelVolume == nil || *ev.Data.FuelVolume != vol {
		t.Errorf("expected fuel_volume %v, got %v", vol, ev.Data.FuelVolume)
	}
	if ev.Data.FuelTempC == nil || *ev.Data.FuelTempC != temp {
		t.Errorf("expected fuel_temp_c %v, got %v", temp, ev.Data.FuelTempC)
	}
	if ev.Data.Satellites != 11 {
		t.Errorf("expected satellites 11, got %d", ev.Data.Satellites)
	}
	if ev.Data.GsmSignal != 4 {
		t.Errorf("expected gsm_signal 4, got %d", ev.Data.GsmSignal)
	}
	if !ev.Data.Acc {
		t.Error("expected acc=true in fuel frame")
	}
}

// Fuel-only frame (tanpa posisi) tetap di-broadcast dengan fuel & ACC.
func TestBridgeHandleForwardsFuelEmptyPosition(t *testing.T) {
	cl, _ := bridgeTestEnv(t)
	fuel := 58.0
	msg := bridgeTelemetryMsg(t, []byte(`{
		"imei":"864201040512345","company_code":"DEV001","vehicle_id":11,
		"fuel_level":58.0,"fuel_temp_c":31,"acc":true,"timestamp":1722000003
	}`))
	if err := bridgeHandle(msg); err != nil {
		t.Fatalf("bridgeHandle unexpected error: %v", err)
	}

	var ev models.VehicleUpdateEvent
	if err := json.Unmarshal(awaitPayload(t, cl), &ev); err != nil {
		t.Fatalf("unmarshal VEHICLE_UPDATE: %v", err)
	}
	if ev.Data.FuelLevel == nil || *ev.Data.FuelLevel != fuel {
		t.Errorf("expected fuel_level %v, got %v", fuel, ev.Data.FuelLevel)
	}
	if !ev.Data.Acc {
		t.Error("expected acc=true passthrough")
	}
}

// Hotfix WebSocket DTO (fuel_level/satellites/altitude): bridgeHandle harus
// meneruskan KETIGA field ke payload VEHICLE_UPDATE. fuel_level & satellites
// sudah mengalir sejak hotfix ACC/B5a; altitude kini di-decode ingestion dari
// GPS element Teltonika (Codec 8/8E/7) dan di-pipe tanpa diubah.
func TestBridgeHandlePassesFuelSatellitesAltitude(t *testing.T) {
	cl, _ := bridgeTestEnv(t)

	fuel := 62.5
	msg := bridgeTelemetryMsg(t, []byte(`{
		"imei":"864201040512345","company_code":"DEV001","vehicle_id":11,
		"lat":-6.2,"lon":106.8,"speed":40,"heading":270,"satellites":11,
		"altitude":125,"acc":true,"fuel_level":62.5,"timestamp":1722000007
	}`))
	if err := bridgeHandle(msg); err != nil {
		t.Fatalf("bridgeHandle unexpected error: %v", err)
	}

	// Payload diambil SEKALI (awaitPayload mengonsumsi channel klien).
	raw := awaitPayload(t, cl)

	// Kontrak JSON: key altitude benar-benar muncul di payload ketika ≠ 0.
	if !strings.Contains(string(raw), `"altitude":125`) {
		t.Errorf("payload missing altitude key: %s", raw)
	}

	var ev models.VehicleUpdateEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		t.Fatalf("unmarshal VEHICLE_UPDATE: %v", err)
	}
	if ev.Data.FuelLevel == nil || *ev.Data.FuelLevel != fuel {
		t.Errorf("expected fuel_level %v, got %v", fuel, ev.Data.FuelLevel)
	}
	if ev.Data.Satellites != 11 {
		t.Errorf("expected satellites 11, got %d", ev.Data.Satellites)
	}
	if ev.Data.Altitude != 125 {
		t.Errorf("expected altitude 125, got %d", ev.Data.Altitude)
	}
}

// IMEI tak dikenal TIDAK boleh di-broadcast (anti-leak).
func TestBridgeHandleSkipsUnknownIMEI(t *testing.T) {
	cl, _ := bridgeTestEnv(t)

	msg := bridgeTelemetryMsg(t, []byte(`{
		"imei":"999999999999999","company_code":"DEV001",
		"lat":-6.2,"lon":106.8,"speed":10,"acc":true,"timestamp":1722000004
	}`))
	if err := bridgeHandle(msg); err != nil {
		t.Fatalf("bridgeHandle unexpected error: %v", err)
	}
	if !noPayload(cl) {
		t.Fatal("unexpected broadcast for unknown IMEI")
	}
}

// Company lain tidak boleh menerima paket (tenant isolation via hub companyCode).
func TestBridgeHandleTenantIsolation(t *testing.T) {
	vehReg = newVehicleRegistry()
	vehReg.cache[registryKey("DEV001", "864201040512345")] = registryEntry{
		info:   vehicleInfo{ID: 42, Model: "GT06", Plate: "B 1234 CD"},
		expire: time.Now().Add(5 * time.Minute),
	}
	appHub = newHub(100, 100)
	// Klien DEV002 (tenant lain) — tidak boleh menerima update DEV001.
	other, _ := mkClient(t, appHub, "DEV002", false, 42)

	msg := bridgeTelemetryMsg(t, []byte(`{
		"imei":"864201040512345","company_code":"DEV001","vehicle_id":11,
		"lat":-6.2,"lon":106.8,"speed":10,"acc":true,"timestamp":1722000005
	}`))
	if err := bridgeHandle(msg); err != nil {
		t.Fatalf("bridgeHandle unexpected error: %v", err)
	}
	if !noPayload(other) {
		t.Fatal("cross-tenant client received a VEHICLE_UPDATE (leak)")
	}
}

// Malformed JSON tidak boleh panic / crash bridge.
func TestBridgeHandleMalformedPayload(t *testing.T) {
	cl, _ := bridgeTestEnv(t)
	msg := bridgeTelemetryMsg(t, []byte(`{not-json`))
	if err := bridgeHandle(msg); err != nil {
		t.Fatalf("bridgeHandle unexpected error: %v", err)
	}
	if !noPayload(cl) {
		t.Fatal("expected no broadcast for malformed payload")
	}
}

// Empty IMEI harus no-op.
func TestBridgeHandleEmptyIMEINoOp(t *testing.T) {
	cl, _ := bridgeTestEnv(t)
	msg := bridgeTelemetryMsg(t, []byte(`{"company_code":"DEV001","timestamp":1722000006}`))
	if err := bridgeHandle(msg); err != nil {
		t.Fatalf("bridgeHandle unexpected error: %v", err)
	}
	if !noPayload(cl) {
		t.Fatal("expected no broadcast for empty IMEI")
	}
}

// RedisState (live state Redis) menangkap fuel_level/acc dari worker-live —
// dipakai REST enrichVehicles untuk surfaced last_position.fuel_level/acc.
func TestRedisStateParsesFuelAndAcc(t *testing.T) {
	accTrue := true
	var st models.RedisState
	data := `{"imei":"864201040512345","company_code":"DEV001","lat":-6.2,"lon":106.8,
		"speed":0,"heading":90,"status":"ONLINE","last_seen":1722000000,
		"fuel_level":57.25,"fuel_temp_c":31.5,"acc":true}`
	if err := json.Unmarshal([]byte(data), &st); err != nil {
		t.Fatalf("unmarshal RedisState: %v", err)
	}
	if st.FuelLevel == nil || *st.FuelLevel != 57.25 {
		t.Errorf("expected fuel_level 57.25, got %v", st.FuelLevel)
	}
	if st.FuelTempC == nil || *st.FuelTempC != 31.5 {
		t.Errorf("expected fuel_temp_c 31.5, got %v", st.FuelTempC)
	}
	if st.Acc == nil || *st.Acc != accTrue {
		t.Errorf("expected acc true, got %v", st.Acc)
	}
}
