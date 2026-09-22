package controllers

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"

	"adatrack_gps/service-websocket/models"
)

// subKey identifies one tenant+vehicle fan-out bucket. Tenants are always part
// of the key so cross-tenant leakage is structurally impossible (PRD §3.1).
type subKey struct {
	company   string
	vehicleID int64
}

// Hub fans live updates out to the WebSocket clients that are entitled to see
// them (PRD §3.1 "AppHub fan-out filter per company + per vehicle permission").
type Hub struct {
	settings Settings
	plates   *plateCache

	mu      sync.RWMutex
	clients map[*Client]struct{}
	index   map[subKey]map[*Client]struct{}

	queuedMessages int64

	ctx    context.Context
	cancel context.CancelFunc
}

// NewHub builds the hub.
func NewHub(settings Settings, plates *plateCache) *Hub {
	ctx, cancel := context.WithCancel(context.Background())
	return &Hub{
		settings: settings,
		plates:   plates,
		clients:  map[*Client]struct{}{},
		index:    map[subKey]map[*Client]struct{}{},
		ctx:      ctx,
		cancel:   cancel,
	}
}

// Start marks the hub ready (no background workers are required: each client
// runs its own read/write pumps).
func (h *Hub) Start() {
	slog.Info("websocket hub ready", "max_connections", h.settings.WSMaxConnections,
		"max_queue", h.settings.WSMaxQueueSize)
}

// Stop closes every connection and waits for the pumps to finish (graceful
// shutdown: no goroutine is leaked — FR-4.4).
func (h *Hub) Stop() {
	h.cancel()
	h.mu.Lock()
	clients := make([]*Client, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.Unlock()
	for _, c := range clients {
		c.close()
	}
}

// ActiveConnections reports the current connection count (healthz/metrics).
func (h *Hub) ActiveConnections() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// canAccept reports whether a new connection fits the budget (FR-5.4). It is
// checked BEFORE the upgrade so an over-capacity client receives a clean HTTP 503
// instead of an accepted-then-dropped socket.
func (h *Hub) canAccept() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.settings.WSMaxConnections <= 0 {
		return true
	}
	return len(h.clients) < h.settings.WSMaxConnections
}

// register adds a client, rejecting it when the connection budget is exhausted
// (FR-5.4: max 5000 connections). It re-checks the budget under the write lock so
// two simultaneous handshakes can never exceed the limit.
func (h *Hub) register(c *Client) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.settings.WSMaxConnections > 0 && len(h.clients) >= h.settings.WSMaxConnections {
		wsConnectionsRejected.WithLabelValues("capacity").Inc()
		return false
	}
	h.clients[c] = struct{}{}
	wsConnectionsActive.Set(float64(len(h.clients)))
	wsConnectionsTotal.Inc()
	return true
}

// unregister removes a client and every subscription it held.
func (h *Hub) unregister(c *Client) {
	h.mu.Lock()
	if _, ok := h.clients[c]; !ok {
		h.mu.Unlock()
		return
	}
	delete(h.clients, c)
	for id := range c.subscriptions() {
		key := subKey{company: c.companyCode, vehicleID: id}
		if bucket, ok := h.index[key]; ok {
			delete(bucket, c)
			if len(bucket) == 0 {
				delete(h.index, key)
			}
		}
	}
	wsConnectionsActive.Set(float64(len(h.clients)))
	h.mu.Unlock()

	c.removeAllSubscriptions()
	wsSubscriptions.Set(float64(h.subscriptionCount()))
}

// subscribe grants a vehicle subscription when the client's RBAC permits it
// (PRD §3.1: reject UNAUTHORIZED_VEHICLE). `allowed` is evaluated by the caller
// (row-level check) so this stays a pure bookkeeping operation.
func (h *Hub) subscribe(c *Client, vehicleID int64, permitted bool) bool {
	if !permitted {
		rbacDenied.WithLabelValues("vehicle").Inc()
		return false
	}
	h.mu.Lock()
	key := subKey{company: c.companyCode, vehicleID: vehicleID}
	bucket, ok := h.index[key]
	if !ok {
		bucket = map[*Client]struct{}{}
		h.index[key] = bucket
	}
	bucket[c] = struct{}{}
	h.mu.Unlock()

	c.addSubscription(vehicleID)
	wsSubscriptions.Set(float64(h.subscriptionCount()))
	return true
}

// unsubscribe drops one vehicle subscription.
func (h *Hub) unsubscribe(c *Client, vehicleID int64) {
	h.mu.Lock()
	key := subKey{company: c.companyCode, vehicleID: vehicleID}
	if bucket, ok := h.index[key]; ok {
		delete(bucket, c)
		if len(bucket) == 0 {
			delete(h.index, key)
		}
	}
	h.mu.Unlock()

	c.removeSubscription(vehicleID)
	wsSubscriptions.Set(float64(h.subscriptionCount()))
}

// subscriptionCount sums the buckets (metrics only).
func (h *Hub) subscriptionCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	total := 0
	for _, bucket := range h.index {
		total += len(bucket)
	}
	return total
}

// Broadcast delivers one live update to every entitled subscriber. The payload
// is marshalled ONCE and the queue depth is tracked so a slow consumer can be
// observed before it is dropped (FR-5.4).
func (h *Hub) Broadcast(data models.VehicleUpdateData) {
	start := time.Now()
	envelope := models.WSEnvelope{
		Event:     models.EventVehicleUpdate,
		Data:      data,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		slog.Error("service-websocket: could not encode vehicle update", "imei", data.IMEI, "error", err)
		return
	}

	key := subKey{company: data.CompanyCode, vehicleID: data.VehicleID}
	h.mu.RLock()
	bucket := h.index[key]
	targets := make([]*Client, 0, len(bucket))
	for c := range bucket {
		targets = append(targets, c)
	}
	h.mu.RUnlock()

	for _, c := range targets {
		c.enqueue(models.EventVehicleUpdate, payload)
	}
	wsMessagesSent.WithLabelValues(models.EventVehicleUpdate).Add(float64(len(targets)))
	h.setQueueDepth()
	observeBroadcast(start)
}

// PublishEvent delivers one pre-decoded payload to every client subscribed to a
// (company, vehicle) pair — the shared fan-out used by the alert-notification and
// media-event bridges (FR-8.5, `notify.alert.<vehicle_id>`). The tenant is always
// part of the bucket key, so cross-tenant leakage stays structurally impossible.
func (h *Hub) PublishEvent(companyCode string, vehicleID int64, event string, data any) int {
	envelope := models.WSEnvelope{
		Event:     event,
		Data:      data,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		slog.Error("service-websocket: could not encode event", "event", event, "error", err)
		return 0
	}

	key := subKey{company: strings.ToUpper(strings.TrimSpace(companyCode)), vehicleID: vehicleID}
	h.mu.RLock()
	bucket := h.index[key]
	targets := make([]*Client, 0, len(bucket))
	for c := range bucket {
		targets = append(targets, c)
	}
	h.mu.RUnlock()

	for _, c := range targets {
		c.enqueue(event, payload)
	}
	wsMessagesSent.WithLabelValues(event).Add(float64(len(targets)))
	h.setQueueDepth()
	return len(targets)
}

// setQueueDepth publishes the pending-queue gauge.
func (h *Hub) setQueueDepth() {
	h.mu.RLock()
	var total int
	for c := range h.clients {
		total += c.queueDepth()
	}
	h.mu.RUnlock()
	wsMessageQueueSize.Set(float64(total))
}
