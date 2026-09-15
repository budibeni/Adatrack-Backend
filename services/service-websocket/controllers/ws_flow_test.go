package controllers

import (
	"testing"
	"time"

	"ajb_gps/service-websocket/models"
)

// TestWSPushEndToEndUnderOneSecond covers the acceptance criterion "WS push
// end-to-end < 1 s dari publish worker-live" using the real broadcast path.
func TestWSPushEndToEndUnderOneSecond(t *testing.T) {
	h := newHarness(t)
	access, _ := h.login(t, "admin@dev001.io", "Admin@123")

	conn, _, err := wsDial(t, h, access, "")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	sendWS(t, conn, models.WSRequest{Action: "subscribe", VehicleIDs: []int64{1}})
	readEvent(t, conn, models.EventSubscribed, 2*time.Second)

	acc := true
	fuel := 61.5
	start := time.Now()
	h.service.Hub().Broadcast(models.VehicleUpdateData{
		IMEI: "864201040512345", CompanyCode: "DEV001", VehicleID: 1, PlateNumber: "B 1234 XYZ",
		Lat: -6.2088, Lon: 106.8456, Speed: 45.2, Heading: 180, ACC: &acc,
		Status: models.StatusOnline, Battery: 85, FuelLevel: &fuel,
		Satellites: 9, Altitude: 112, GsmSignal: 4, Timestamp: time.Now().UTC().Format(time.RFC3339),
	})

	envelope := readEvent(t, conn, models.EventVehicleUpdate, time.Second)
	latency := time.Since(start)
	if latency >= time.Second {
		t.Fatalf("push latency = %dms, want < 1000 ms", latency.Milliseconds())
	}

	// FR-5.2 payload contract.
	data, ok := envelope.Data.(map[string]any)
	if !ok {
		t.Fatalf("data block missing: %v", envelope)
	}
	if data["imei"] != "864201040512345" || data["plate_number"] != "B 1234 XYZ" {
		t.Fatalf("identity fields wrong: %v", data)
	}
	if data["acc"] != true {
		t.Fatalf("acc = %v, want the real tracker value", data["acc"])
	}
	if data["fuel_level"].(float64) != 61.5 || data["satellites"].(float64) != 9 ||
		data["altitude"].(float64) != 112 || data["gsm_signal"].(float64) != 4 {
		t.Fatalf("enrichment fields wrong: %v", data)
	}
	if _, err := time.Parse(time.RFC3339, data["timestamp"].(string)); err != nil {
		t.Fatalf("timestamp is not RFC3339: %v", data["timestamp"])
	}
}

// TestWSSubscribeUnauthorizedVehicle asserts the RBAC filter on subscribe
// (PRD §3.1/§8.3: ERROR UNAUTHORIZED_VEHICLE).
func TestWSSubscribeUnauthorizedVehicle(t *testing.T) {
	h := newHarness(t)
	access, _ := h.login(t, "driver@dev001.io", "Admin@123")

	conn, _, err := wsDial(t, h, access, "")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// The driver only owns vehicle 3 → vehicle 1 must be refused.
	sendWS(t, conn, models.WSRequest{Action: "subscribe", VehicleIDs: []int64{1}})
	envelope := readEvent(t, conn, models.EventError, 2*time.Second)
	if envelope.ErrorCode != CodeUnauthorizedVehicle {
		t.Fatalf("error_code = %q, want %s", envelope.ErrorCode, CodeUnauthorizedVehicle)
	}
	ack := readEvent(t, conn, models.EventSubscribed, 2*time.Second)
	if granted := ack.Data.(map[string]any)["vehicle_ids"].([]any); len(granted) != 0 {
		t.Fatalf("granted = %v, want none", granted)
	}

	// The unauthorized subscription must never deliver an update.
	h.service.Hub().Broadcast(models.VehicleUpdateData{
		IMEI: "864201040512345", CompanyCode: "DEV001", VehicleID: 1, Speed: 99,
	})
	expectNoFrame(t, conn, 300*time.Millisecond, "received an update for an unauthorized vehicle")
}

// TestWSTopicSubscription asserts the documented `vehicle.update.{id}` topic form.
func TestWSTopicSubscription(t *testing.T) {
	h := newHarness(t)
	access, _ := h.login(t, "admin@dev001.io", "Admin@123")

	conn, _, err := wsDial(t, h, access, "")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	sendWS(t, conn, models.WSRequest{Action: "subscribe", Topic: "vehicle.update.2"})
	ack := readEvent(t, conn, models.EventSubscribed, 2*time.Second)
	granted := ack.Data.(map[string]any)["vehicle_ids"].([]any)
	if len(granted) != 1 || int64(granted[0].(float64)) != 2 {
		t.Fatalf("granted = %v, want [2]", granted)
	}

	// An unsupported topic is rejected with a clear error.
	sendWS(t, conn, models.WSRequest{Action: "subscribe", Topic: "geofence.1"})
	envelope := readEvent(t, conn, models.EventError, 2*time.Second)
	if envelope.ErrorCode != CodeValidationError {
		t.Fatalf("error_code = %q, want %s", envelope.ErrorCode, CodeValidationError)
	}
}
