package controllers

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"adatrack_gps/internal"
	"adatrack_gps/service-websocket/models"
)

// accPtr builds the tri-state ACC value used by the B6 tests.
func accPtr(v bool) *bool { return &v }

// TestVehicleUpdateDTOIsComplete asserts the B6 acceptance for the FR-5.2
// payload: every field of the documented DTO reaches the client (fuel_level,
// fuel_volume, fuel_temp_c, satellites, altitude, gsm_signal) with the device
// values, and ACC keeps its tri-state semantics.
func TestVehicleUpdateDTOIsComplete(t *testing.T) {
	hub := NewHub(Settings{WSMaxQueueSize: 8, WSMaxConnections: 10}, nil)
	client := &Client{hub: hub, companyCode: "DEV001", send: make(chan []byte, 8), subs: map[int64]struct{}{}}
	hub.register(client)
	t.Cleanup(func() { hub.unregister(client) })
	hub.subscribe(client, 42, true)

	bridge := NewBridge(internal.LoadConfig(), nil, hub)

	fuelLevel, fuelVolume, fuelTemp := 61.5, 42.25, 27.5
	payload, err := json.Marshal(models.LiveState{
		IMEI: "864201040512345", CompanyCode: "DEV001", VehicleID: 42,
		Lat: -6.2088, Lon: 106.8456, Speed: 45.2, Heading: 180,
		ACC: accPtr(false), Status: models.StatusOnline, Battery: 85,
		FuelLevel: &fuelLevel, FuelVolume: &fuelVolume, FuelTempC: &fuelTemp,
		Satellites: 9, Altitude: 112, GsmSignal: 4, Timestamp: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("marshal live state: %v", err)
	}
	if err := bridge.handleMessage(&nats.Msg{Subject: "telemetry.live.864201040512345", Data: payload}); err != nil {
		t.Fatalf("handleMessage: %v", err)
	}

	var envelope models.WSEnvelope
	if err := json.Unmarshal(<-client.send, &envelope); err != nil {
		t.Fatalf("decode frame: %v", err)
	}
	raw, err := json.Marshal(envelope.Data)
	if err != nil {
		t.Fatalf("re-marshal envelope data: %v", err)
	}
	body := string(raw)
	for _, key := range []string{
		`"fuel_level":61.5`, `"fuel_volume":42.25`, `"fuel_temp_c":27.5`,
		`"satellites":9`, `"altitude":112`, `"gsm_signal":4`,
		`"vehicle_id":42`, `"timestamp":`,
	} {
		if !strings.Contains(body, key) {
			t.Errorf("VEHICLE_UPDATE payload %s is missing %s", body, key)
		}
	}
	// The device reported ACC OFF: the frame must carry a literal false.
	if !strings.Contains(body, `"acc":false`) {
		t.Errorf("payload %s must carry the literal device acc=false", body)
	}
	if _, err := time.Parse(time.RFC3339, envelope.Timestamp); err != nil && envelope.Timestamp != "" {
		t.Errorf("envelope timestamp %q is not RFC3339", envelope.Timestamp)
	}
}

// TestVehicleUpdateACCAbsentWhenUnreported asserts that a device which never
// reported ACC produces a frame WITHOUT the `acc` key (no inferred false) —
// the core of the B6 audit fix on the real-time path.
func TestVehicleUpdateACCAbsentWhenUnreported(t *testing.T) {
	hub := NewHub(Settings{WSMaxQueueSize: 8, WSMaxConnections: 10}, nil)
	client := &Client{hub: hub, companyCode: "DEV001", send: make(chan []byte, 8), subs: map[int64]struct{}{}}
	hub.register(client)
	t.Cleanup(func() { hub.unregister(client) })
	hub.subscribe(client, 7, true)

	bridge := NewBridge(internal.LoadConfig(), nil, hub)
	payload, err := json.Marshal(models.LiveState{
		IMEI: "86001", CompanyCode: "DEV001", VehicleID: 7,
		Lat: -6.2, Lon: 106.8, Speed: 12, Heading: 90,
		Status: models.StatusOnline, Satellites: 8, Timestamp: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("marshal live state: %v", err)
	}
	if err := bridge.handleMessage(&nats.Msg{Subject: "telemetry.live.86001", Data: payload}); err != nil {
		t.Fatalf("handleMessage: %v", err)
	}

	var envelope models.WSEnvelope
	if err := json.Unmarshal(<-client.send, &envelope); err != nil {
		t.Fatalf("decode frame: %v", err)
	}
	raw, err := json.Marshal(envelope.Data)
	if err != nil {
		t.Fatalf("re-marshal envelope data: %v", err)
	}
	if strings.Contains(string(raw), `"acc"`) {
		t.Fatalf("payload %s must omit acc when the device never reported it", raw)
	}
}
