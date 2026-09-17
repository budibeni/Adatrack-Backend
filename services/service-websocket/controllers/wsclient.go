package controllers

import (
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"adatrack_gps/service-websocket/models"
)

// Client is one authenticated WebSocket connection (PRD §8.3, FR-5.4).
type Client struct {
	hub      *Hub
	conn     *websocket.Conn
	settings Settings

	// identity / RBAC snapshot taken at handshake time
	userID      int64
	email       string
	role        string
	companyCode string
	allVehicles bool
	assigned    map[int64]bool

	remoteIP string

	send chan []byte

	mu     sync.Mutex
	subs   map[int64]struct{}
	closed bool
}

// newClient snapshots the identity of the upgraded connection.
func newClient(hub *Hub, conn *websocket.Conn, remoteIP string, identity *tenantIdentity) *Client {
	assigned := make(map[int64]bool, len(identity.assigned))
	for _, id := range identity.assigned {
		assigned[id] = true
	}
	queueSize := hub.settings.WSMaxQueueSize
	if queueSize <= 0 {
		queueSize = 1000
	}
	return &Client{
		hub:         hub,
		conn:        conn,
		settings:    hub.settings,
		userID:      identity.user.ID,
		email:       identity.user.Email,
		role:        identity.user.Role,
		companyCode: identity.user.CompanyCode,
		allVehicles: identity.allVehicles,
		assigned:    assigned,
		remoteIP:    remoteIP,
		send:        make(chan []byte, queueSize),
		subs:        map[int64]struct{}{},
	}
}

// permitted reports whether the client may receive updates for a vehicle
// (row-level RBAC, PRD §3.1). Admins/Managers see the whole tenant; every other
// role only sees explicitly assigned vehicles.
func (c *Client) permitted(vehicleID int64) bool {
	if vehicleID <= 0 {
		return false
	}
	if c.allVehicles {
		return true
	}
	return c.assigned[vehicleID]
}

// enqueue pushes a pre-marshalled frame into the client queue. When the queue is
// full the OLDEST message is dropped (never the newest) and the drop is logged +
// counted (FR-5.4 "drop-oldest + log").
func (c *Client) enqueue(event string, payload []byte) {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return
	}
	select {
	case c.send <- payload:
		return
	default:
	}
	// Queue full: evict the oldest frame to make room for the newest.
	select {
	case dropped := <-c.send:
		slog.Warn("service-websocket: client queue full — dropping oldest frame",
			"user_id", c.userID, "company", c.companyCode, "event", event, "dropped_bytes", len(dropped))
	default:
	}
	wsMessagesDropped.WithLabelValues(event).Inc()
	select {
	case c.send <- payload:
	default:
	}
}

// queueDepth reports the pending frames of this client.
func (c *Client) queueDepth() int { return len(c.send) }

// subscriptions snapshots the vehicle ids this client is subscribed to.
func (c *Client) subscriptions() map[int64]struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[int64]struct{}, len(c.subs))
	for id := range c.subs {
		out[id] = struct{}{}
	}
	return out
}

// addSubscription records a subscription.
func (c *Client) addSubscription(vehicleID int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.subs[vehicleID] = struct{}{}
}

// removeSubscription drops one subscription.
func (c *Client) removeSubscription(vehicleID int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.subs, vehicleID)
}

// removeAllSubscriptions clears the subscription set (disconnect).
func (c *Client) removeAllSubscriptions() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.subs = map[int64]struct{}{}
}

// isClosed reports whether the connection was torn down.
func (c *Client) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// close tears the connection down exactly once.
func (c *Client) close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.mu.Unlock()
	_ = c.conn.Close()
}

// sendEnvelope serialises and queues a server→client frame.
func (c *Client) sendEnvelope(event string, data any, errorCode, message string) {
	envelope := models.WSEnvelope{
		Event:     event,
		Data:      data,
		ErrorCode: errorCode,
		Message:   message,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		slog.Warn("service-websocket: could not encode ws frame", "event", event, "error", err)
		return
	}
	c.enqueue(event, payload)
}

// sendError reports a protocol/RBAC failure to the client (PRD §8.3 ERROR event).
func (c *Client) sendError(code, message string) {
	c.sendEnvelope(models.EventError, nil, code, message)
	wsMessagesSent.WithLabelValues(models.EventError).Inc()
}

// readPump consumes client frames (subscription model) and enforces the pong
// deadline (FR-5.3: server ping 30 s, 60 s pong timeout).
func (c *Client) readPump() {
	defer func() {
		c.hub.unregister(c)
		c.close()
	}()

	if c.settings.WSReadLimit > 0 {
		c.conn.SetReadLimit(c.settings.WSReadLimit)
	}
	_ = c.conn.SetReadDeadline(time.Now().Add(c.settings.WSPongTimeout))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(c.settings.WSPongTimeout))
	})

	for {
		_, raw, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				slog.Debug("service-websocket: client read error", "user_id", c.userID, "error", err)
			}
			return
		}
		c.handleMessage(raw)
	}
}

// writePump drains the send queue and keeps the connection alive with pings.
func (c *Client) writePump() {
	interval := c.settings.WSPingInterval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer func() {
		ticker.Stop()
		c.close()
	}()

	for {
		select {
		case payload, ok := <-c.send:
			if !ok {
				return
			}
			_ = c.conn.SetWriteDeadline(time.Now().Add(c.settings.WSWriteTimeout))
			if err := c.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
				slog.Debug("service-websocket: write failed", "user_id", c.userID, "error", err)
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(c.settings.WSWriteTimeout))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-c.hub.ctx.Done():
			_ = c.conn.SetWriteDeadline(time.Now().Add(c.settings.WSWriteTimeout))
			_ = c.conn.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseGoingAway, "server shutting down"))
			return
		}
	}
}

// handleMessage applies one client→server frame (PRD §8.3 subscription model).
func (c *Client) handleMessage(raw []byte) {
	var req models.WSRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		c.sendError(CodeValidationError, "malformed message")
		return
	}

	switch strings.ToLower(strings.TrimSpace(req.Action)) {
	case "subscribe":
		ids := req.VehicleIDs
		if len(ids) == 0 && req.Topic != "" {
			if id, ok := parseVehicleTopic(req.Topic); ok {
				ids = []int64{id}
			} else {
				c.sendError(CodeValidationError, "unsupported topic (expected vehicle.update.{id})")
				return
			}
		}
		if len(ids) == 0 {
			c.sendError(CodeValidationError, "vehicle_ids is required")
			return
		}
		if max := c.settings.WSSubscribeMax; max > 0 && len(ids) > max {
			c.sendError(CodeValidationError, "too many vehicle_ids in one subscribe")
			return
		}
		granted := make([]int64, 0, len(ids))
		for _, id := range ids {
			if c.hub.subscribe(c, id, c.permitted(id)) {
				granted = append(granted, id)
				continue
			}
			c.sendError(CodeUnauthorizedVehicle, "subscription to vehicle "+itoa(id)+" is not permitted")
		}
		c.sendEnvelope(models.EventSubscribed, map[string]any{"vehicle_ids": granted}, "", "")
		wsMessagesSent.WithLabelValues(models.EventSubscribed).Inc()

	case "unsubscribe":
		if len(req.VehicleIDs) == 0 {
			c.sendError(CodeValidationError, "vehicle_ids is required")
			return
		}
		for _, id := range req.VehicleIDs {
			c.hub.unsubscribe(c, id)
		}
		c.sendEnvelope(models.EventUnsubscribed, map[string]any{"vehicle_ids": req.VehicleIDs}, "", "")
		wsMessagesSent.WithLabelValues(models.EventUnsubscribed).Inc()

	case "ping":
		c.sendEnvelope(models.EventHeartbeat, nil, "", "")
		wsMessagesSent.WithLabelValues(models.EventHeartbeat).Inc()

	default:
		c.sendError(CodeValidationError, "unsupported action")
	}
}

// parseVehicleTopic extracts the id from `vehicle.update.{id}` (PRD §3.1).
func parseVehicleTopic(topic string) (int64, bool) {
	const prefix = "vehicle.update."
	trimmed := strings.TrimSpace(topic)
	if !strings.HasPrefix(trimmed, prefix) {
		return 0, false
	}
	raw := strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
	if raw == "" || len(raw) > 18 {
		return 0, false
	}
	var id int64
	for _, r := range raw {
		if r < '0' || r > '9' {
			return 0, false
		}
		id = id*10 + int64(r-'0')
	}
	if id <= 0 {
		return 0, false
	}
	return id, true
}
