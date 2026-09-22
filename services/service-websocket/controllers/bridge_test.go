package controllers

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"adatrack_gps/internal"
	"adatrack_gps/service-websocket/models"
)

// TestBridgeBroadcastsLiveUpdate asserts the `telemetry.live.* → hub` path
// (PRD §4.1 step 7) including the FR-5.2 plate enrichment.
func TestBridgeBroadcastsLiveUpdate(t *testing.T) {
	store := newTestStore()
	hub := NewHub(Settings{WSMaxQueueSize: 8, WSMaxConnections: 10}, newPlateCache(store))
	// Warm the plate cache so the enrichment is deterministic.
	if plate := hub.plates.Plate(context.Background(), "DEV001", 1); plate != "B 1234 XYZ" {
		t.Fatalf("plate cache warm-up = %q, want B 1234 XYZ", plate)
	}

	client := &Client{hub: hub, companyCode: "DEV001", send: make(chan []byte, 8), subs: map[int64]struct{}{}}
	hub.register(client)
	defer hub.unregister(client)
	hub.subscribe(client, 1, true)

	bridge := NewBridge(internal.LoadConfig(), nil, hub)
	acc := true
	fuel := 61.5
	payload, err := json.Marshal(models.LiveState{
		IMEI: "864201040512345", CompanyCode: "DEV001", VehicleID: 1,
		Lat: -6.2088, Lon: 106.8456, Speed: 45.2, Heading: 180, ACC: &acc,
		Status: models.StatusOnline, Battery: 85, FuelLevel: &fuel,
		Satellites: 9, Altitude: 112, GsmSignal: 4, Timestamp: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	if err := bridge.handleMessage(&nats.Msg{Subject: "telemetry.live.864201040512345", Data: payload}); err != nil {
		t.Fatalf("handleMessage: %v", err)
	}
	if client.queueDepth() != 1 {
		t.Fatalf("client queue = %d, want 1", client.queueDepth())
	}

	var envelope models.WSEnvelope
	if err := json.Unmarshal(<-client.send, &envelope); err != nil {
		t.Fatalf("decode frame: %v", err)
	}
	if envelope.Event != models.EventVehicleUpdate {
		t.Fatalf("event = %q, want %s", envelope.Event, models.EventVehicleUpdate)
	}
	data, _ := json.Marshal(envelope.Data)
	var update models.VehicleUpdateData
	if err := json.Unmarshal(data, &update); err != nil {
		t.Fatalf("decode update: %v", err)
	}
	if update.PlateNumber != "B 1234 XYZ" {
		t.Fatalf("plate_number = %q, want B 1234 XYZ (FR-5.2)", update.PlateNumber)
	}
	if update.ACC == nil || !*update.ACC {
		t.Fatalf("acc = %v, want the real tracker value", update.ACC)
	}
	if update.FuelLevel == nil || *update.FuelLevel != 61.5 {
		t.Fatalf("fuel_level = %v, want 61.5", update.FuelLevel)
	}
	if _, err := time.Parse(time.RFC3339, update.Timestamp); err != nil {
		t.Fatalf("timestamp = %q, want RFC3339", update.Timestamp)
	}
}

// TestBridgeIgnoresMalformedPayloads asserts a bad payload is counted and never
// panics or blocks the subscription.
func TestBridgeIgnoresMalformedPayloads(t *testing.T) {
	hub := NewHub(Settings{WSMaxQueueSize: 4}, newPlateCache(newFakeStore()))
	client := &Client{hub: hub, companyCode: "DEV001", send: make(chan []byte, 4), subs: map[int64]struct{}{}}
	hub.register(client)
	defer hub.unregister(client)
	hub.subscribe(client, 1, true)

	bridge := NewBridge(internal.LoadConfig(), nil, hub)

	if err := bridge.handleMessage(&nats.Msg{Subject: "telemetry.live.1", Data: []byte("{not json")}); err != nil {
		t.Fatalf("malformed payload returned an error: %v", err)
	}
	// A payload without tenant/vehicle must be skipped as well.
	incomplete, _ := json.Marshal(models.LiveState{IMEI: "1", Timestamp: time.Now().Unix()})
	if err := bridge.handleMessage(&nats.Msg{Subject: "telemetry.live.1", Data: incomplete}); err != nil {
		t.Fatalf("incomplete payload returned an error: %v", err)
	}
	if client.queueDepth() != 0 {
		t.Fatalf("malformed payloads produced %d frames, want 0", client.queueDepth())
	}
}

// TestBridgeUsesDeviceTimeFallback asserts a missing device timestamp falls back
// to server time instead of 1970 (FR-5.2 timestamp sanity).
func TestBridgeUsesDeviceTimeFallback(t *testing.T) {
	hub := NewHub(Settings{WSMaxQueueSize: 4}, newPlateCache(newFakeStore()))
	client := &Client{hub: hub, companyCode: "DEV002", send: make(chan []byte, 4), subs: map[int64]struct{}{}}
	hub.register(client)
	defer hub.unregister(client)
	hub.subscribe(client, 7, true)

	bridge := NewBridge(internal.LoadConfig(), nil, hub)
	payload, _ := json.Marshal(models.LiveState{IMEI: "864201040512999", CompanyCode: "DEV002", VehicleID: 7})
	if err := bridge.handleMessage(&nats.Msg{Subject: "telemetry.live.864201040512999", Data: payload}); err != nil {
		t.Fatalf("handleMessage: %v", err)
	}

	var envelope models.WSEnvelope
	if err := json.Unmarshal(<-client.send, &envelope); err != nil {
		t.Fatalf("decode frame: %v", err)
	}
	raw, _ := json.Marshal(envelope.Data)
	var update models.VehicleUpdateData
	if err := json.Unmarshal(raw, &update); err != nil {
		t.Fatalf("decode update: %v", err)
	}
	ts, err := time.Parse(time.RFC3339, update.Timestamp)
	if err != nil {
		t.Fatalf("timestamp = %q, want RFC3339", update.Timestamp)
	}
	if time.Since(ts) > time.Minute {
		t.Fatalf("timestamp fallback used the epoch: %v", ts)
	}
}

// TestBridgeFansOutAlertNotify covers FR-8.5's sibling contract: a
// `notify.alert.<vehicle_id>` frame reaches the clients subscribed to that
// vehicle with the documented event name (PRD §8.3).
func TestBridgeFansOutAlertNotify(t *testing.T) {
	hub := NewHub(Settings{WSMaxQueueSize: 8, WSMaxConnections: 10}, newPlateCache(newFakeStore()))
	client := &Client{hub: hub, companyCode: "DEV001", send: make(chan []byte, 8), subs: map[int64]struct{}{}}
	hub.register(client)
	defer hub.unregister(client)
	hub.subscribe(client, 5, true)

	// A client subscribed to a different vehicle must NOT receive the frame.
	other := &Client{hub: hub, companyCode: "DEV001", send: make(chan []byte, 8), subs: map[int64]struct{}{}}
	hub.register(other)
	defer hub.unregister(other)
	hub.subscribe(other, 6, true)

	bridge := NewBridge(internal.LoadConfig(), nil, hub)
	payload, _ := json.Marshal(map[string]any{
		"alert_id": 42, "type": "fuel_drop", "severity": "critical",
		"company": "DEV001", "vehicle_id": 5, "imei": "864201040512345",
		"detected_at": time.Now().UTC().Format(time.RFC3339),
	})
	if err := bridge.handleAlertNotify(&nats.Msg{Subject: "notify.alert.5", Data: payload}); err != nil {
		t.Fatalf("handleAlertNotify: %v", err)
	}
	if client.queueDepth() != 1 {
		t.Fatalf("subscribed client queue = %d, want 1", client.queueDepth())
	}
	if other.queueDepth() != 0 {
		t.Fatalf("unrelated subscriber queue = %d, want 0 (RBAC fan-out)", other.queueDepth())
	}

	var envelope models.WSEnvelope
	if err := json.Unmarshal(<-client.send, &envelope); err != nil {
		t.Fatalf("decode frame: %v", err)
	}
	if envelope.Event != models.EventNotifyAlertPrefix+"5" {
		t.Fatalf("event = %q, want notify.alert.5", envelope.Event)
	}
}

// TestBridgeFansOutMediaEvent covers FR-8.5: `media.event.<company>` becomes a
// MEDIA_EVENT frame for the clients subscribed to the affected vehicle.
func TestBridgeFansOutMediaEvent(t *testing.T) {
	hub := NewHub(Settings{WSMaxQueueSize: 8, WSMaxConnections: 10}, newPlateCache(newFakeStore()))
	client := &Client{hub: hub, companyCode: "DEV001", send: make(chan []byte, 8), subs: map[int64]struct{}{}}
	hub.register(client)
	defer hub.unregister(client)
	hub.subscribe(client, 3, true)

	bridge := NewBridge(internal.LoadConfig(), nil, hub)
	payload, _ := json.Marshal(models.MediaEventData{
		ID: 9, CompanyCode: "DEV001", VehicleID: 3, IMEI: "864201040512345",
		EventType: "sos", ObjectKey: "dev001/3/202609/abc.jpg", MimeType: "image/jpeg",
		FileSize: 1024, Status: "complete", URL: "http://minio/adhoc",
		CapturedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err := bridge.handleMediaEvent(&nats.Msg{Subject: "media.event.DEV001", Data: payload}); err != nil {
		t.Fatalf("handleMediaEvent: %v", err)
	}
	if client.queueDepth() != 1 {
		t.Fatalf("client queue = %d, want 1", client.queueDepth())
	}

	var envelope models.WSEnvelope
	if err := json.Unmarshal(<-client.send, &envelope); err != nil {
		t.Fatalf("decode frame: %v", err)
	}
	if envelope.Event != models.EventMediaEvent {
		t.Fatalf("event = %q, want MEDIA_EVENT", envelope.Event)
	}
	data, _ := json.Marshal(envelope.Data)
	var media models.MediaEventData
	if err := json.Unmarshal(data, &media); err != nil {
		t.Fatalf("decode media payload: %v", err)
	}
	if media.ID != 9 || media.VehicleID != 3 || media.URL == "" {
		t.Fatalf("media payload = %+v, want the published frame", media)
	}

	// A malformed or tenant-less frame is skipped, never broadcast.
	if err := bridge.handleMediaEvent(&nats.Msg{Subject: "media.event.DEV001", Data: []byte("{not json")}); err != nil {
		t.Fatalf("malformed media payload returned an error: %v", err)
	}
	if err := bridge.handleAlertNotify(&nats.Msg{Subject: "notify.alert.3", Data: []byte(`{}`)}); err != nil {
		t.Fatalf("incomplete alert payload returned an error: %v", err)
	}
	if client.queueDepth() != 0 {
		t.Fatalf("malformed frames produced %d extra frames, want 0", client.queueDepth())
	}
}
