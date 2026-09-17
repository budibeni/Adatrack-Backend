package controllers

import (
	"testing"
	"time"

	"adatrack_gps/service-websocket/models"
)

// TestWSReconnectAndResubscribe covers "reconnect + resubscribe aman" (FR-5.3).
func TestWSReconnectAndResubscribe(t *testing.T) {
	h := newHarness(t)
	access, _ := h.login(t, "admin@dev001.io", "Admin@123")

	conn, _, err := wsDial(t, h, access, "")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	sendWS(t, conn, models.WSRequest{Action: "subscribe", VehicleIDs: []int64{1}})
	readEvent(t, conn, models.EventSubscribed, 2*time.Second)
	_ = conn.Close()

	// The hub must release the disconnected client's subscriptions.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && h.service.Hub().ActiveConnections() != 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if got := h.service.Hub().ActiveConnections(); got != 0 {
		t.Fatalf("active connections after close = %d, want 0", got)
	}

	// Reconnect + resubscribe works and delivers again.
	conn2, _, err := wsDial(t, h, access, "")
	if err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	defer func() { _ = conn2.Close() }()
	sendWS(t, conn2, models.WSRequest{Action: "subscribe", VehicleIDs: []int64{1}})
	readEvent(t, conn2, models.EventSubscribed, 2*time.Second)

	h.service.Hub().Broadcast(models.VehicleUpdateData{
		IMEI: "864201040512345", CompanyCode: "DEV001", VehicleID: 1, Speed: 12.5,
	})
	envelope := readEvent(t, conn2, models.EventVehicleUpdate, time.Second)
	if envelope.Data.(map[string]any)["speed"].(float64) != 12.5 {
		t.Fatalf("resubscribed client received the wrong payload: %v", envelope.Data)
	}
}

// TestWSUnsubscribeStopsDelivery asserts unsubscribing really detaches the client.
func TestWSUnsubscribeStopsDelivery(t *testing.T) {
	h := newHarness(t)
	access, _ := h.login(t, "admin@dev001.io", "Admin@123")

	conn, _, err := wsDial(t, h, access, "")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	sendWS(t, conn, models.WSRequest{Action: "subscribe", VehicleIDs: []int64{1}})
	readEvent(t, conn, models.EventSubscribed, 2*time.Second)
	sendWS(t, conn, models.WSRequest{Action: "unsubscribe", VehicleIDs: []int64{1}})
	readEvent(t, conn, models.EventUnsubscribed, 2*time.Second)

	h.service.Hub().Broadcast(models.VehicleUpdateData{
		IMEI: "864201040512345", CompanyCode: "DEV001", VehicleID: 1, Speed: 7,
	})
	expectNoFrame(t, conn, 300*time.Millisecond, "received an update after unsubscribe")
}

// TestWSHeartbeat asserts the client `ping` action is answered.
func TestWSHeartbeat(t *testing.T) {
	h := newHarness(t)
	access, _ := h.login(t, "admin@dev001.io", "Admin@123")

	conn, _, err := wsDial(t, h, access, "")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	sendWS(t, conn, models.WSRequest{Action: "ping"})
	readEvent(t, conn, models.EventHeartbeat, 2*time.Second)
}

// TestWSCrossTenantFanOutIsIsolated asserts a client of one tenant never receives
// another tenant's updates (PRD §3.1 fan-out filter).
func TestWSCrossTenantFanOutIsIsolated(t *testing.T) {
	h := newHarness(t)
	h.store.addUser(20, "OTHER1", "admin@other1.io", models.RoleAdmin, "Admin@123", false)
	h.store.addAccess("OTHER1", 20, models.RoleAdmin, true)
	h.store.addVehicle("OTHER1", models.Vehicle{ID: 42, IMEI: "999999999999999", PlateNumber: "X 1 XXX", Status: "active"})
	h.store.companies["OTHER1"] = true

	access, _ := h.login(t, "admin@dev001.io", "Admin@123")
	conn, _, err := wsDial(t, h, access, "")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Subscribe to a DEV001 vehicle id, then broadcast the SAME numeric id for
	// another tenant: the DEV001 client must not receive it.
	sendWS(t, conn, models.WSRequest{Action: "subscribe", VehicleIDs: []int64{1}})
	readEvent(t, conn, models.EventSubscribed, 2*time.Second)

	h.service.Hub().Broadcast(models.VehicleUpdateData{
		IMEI: "999999999999999", CompanyCode: "OTHER1", VehicleID: 1, Speed: 88,
	})
	expectNoFrame(t, conn, 300*time.Millisecond, "cross-tenant leak in the fan-out filter")
}
