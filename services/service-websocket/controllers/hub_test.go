package controllers

import (
	"testing"

	"ajb_gps/service-websocket/models"
)

// TestHubDropOldestWhenQueueFull asserts FR-5.4 ("queue 1.000 drop-oldest + log").
func TestHubDropOldestWhenQueueFull(t *testing.T) {
	hub := NewHub(Settings{WSMaxQueueSize: 2}, newPlateCache(newFakeStore()))
	client := &Client{hub: hub, companyCode: "DEV001", send: make(chan []byte, 2), subs: map[int64]struct{}{}}

	client.enqueue(models.EventVehicleUpdate, []byte("first"))
	client.enqueue(models.EventVehicleUpdate, []byte("second"))
	// The third frame must evict the OLDEST, keeping the newest.
	client.enqueue(models.EventVehicleUpdate, []byte("third"))

	if got := string(<-client.send); got != "second" {
		t.Fatalf("oldest frame not dropped: got %q, want \"second\"", got)
	}
	if got := string(<-client.send); got != "third" {
		t.Fatalf("newest frame not queued: got %q, want \"third\"", got)
	}
	if depth := client.queueDepth(); depth != 0 {
		t.Fatalf("queue depth = %d, want 0", depth)
	}
}

// TestHubSubscribePermission asserts the hub tracks subscriptions and denials.
func TestHubSubscribePermission(t *testing.T) {
	hub := NewHub(Settings{WSMaxQueueSize: 4}, newPlateCache(newFakeStore()))
	client := &Client{hub: hub, companyCode: "DEV001", send: make(chan []byte, 4), subs: map[int64]struct{}{}}
	hub.register(client)
	defer hub.unregister(client)

	if hub.subscribe(client, 5, false) {
		t.Fatalf("unpermitted subscription was granted")
	}
	if !hub.subscribe(client, 5, true) {
		t.Fatalf("permitted subscription was refused")
	}
	if hub.subscriptionCount() != 1 {
		t.Fatalf("subscription count = %d, want 1", hub.subscriptionCount())
	}
	hub.unsubscribe(client, 5)
	if hub.subscriptionCount() != 0 {
		t.Fatalf("subscription count after unsubscribe = %d, want 0", hub.subscriptionCount())
	}
}

// TestHubBroadcastOnlyMatchesTenantAndVehicle asserts the fan-out keying.
func TestHubBroadcastOnlyMatchesTenantAndVehicle(t *testing.T) {
	hub := NewHub(Settings{WSMaxQueueSize: 4}, newPlateCache(newFakeStore()))
	matching := &Client{hub: hub, companyCode: "DEV001", send: make(chan []byte, 4), subs: map[int64]struct{}{}}
	otherTenant := &Client{hub: hub, companyCode: "OTHER1", send: make(chan []byte, 4), subs: map[int64]struct{}{}}
	otherVehicle := &Client{hub: hub, companyCode: "DEV001", send: make(chan []byte, 4), subs: map[int64]struct{}{}}
	for _, client := range []*Client{matching, otherTenant, otherVehicle} {
		hub.register(client)
		defer hub.unregister(client)
	}
	hub.subscribe(matching, 1, true)
	hub.subscribe(otherTenant, 1, true)
	hub.subscribe(otherVehicle, 2, true)

	hub.Broadcast(models.VehicleUpdateData{IMEI: "864201040512345", CompanyCode: "DEV001", VehicleID: 1, Speed: 5})

	if matching.queueDepth() != 1 {
		t.Fatalf("matching client queue = %d, want 1", matching.queueDepth())
	}
	if otherTenant.queueDepth() != 0 {
		t.Fatalf("other tenant received a frame (leak)")
	}
	if otherVehicle.queueDepth() != 0 {
		t.Fatalf("other vehicle received a frame (over-broadcast)")
	}
}

// TestParseVehicleTopic asserts the topic parser rejects malformed input (§8.5).
func TestParseVehicleTopic(t *testing.T) {
	cases := []struct {
		topic string
		want  int64
		ok    bool
	}{
		{"vehicle.update.1", 1, true},
		{"vehicle.update.42", 42, true},
		{"vehicle.update.", 0, false},
		{"vehicle.update.abc", 0, false},
		{"vehicle.update.1x", 0, false},
		{"geofence.1", 0, false},
		{"vehicle.update.0", 0, false},
	}
	for _, tc := range cases {
		got, ok := parseVehicleTopic(tc.topic)
		if ok != tc.ok || got != tc.want {
			t.Fatalf("parseVehicleTopic(%q) = (%d,%v), want (%d,%v)", tc.topic, got, ok, tc.want, tc.ok)
		}
	}
}

// TestClientPermittedRule asserts the row-level rule used by the hub filter.
func TestClientPermittedRule(t *testing.T) {
	assigned := &Client{companyCode: "DEV001", assigned: map[int64]bool{3: true}}
	if assigned.permitted(1) {
		t.Fatalf("unassigned vehicle was permitted")
	}
	if !assigned.permitted(3) {
		t.Fatalf("assigned vehicle was refused")
	}
	all := &Client{companyCode: "DEV001", allVehicles: true, assigned: map[int64]bool{}}
	if !all.permitted(999) {
		t.Fatalf("tenant-wide role should be permitted for any id")
	}
}
