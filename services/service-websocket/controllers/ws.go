package controllers

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"ajb_gps/internal"
	"ajb_gps/service-websocket/models"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// websocketHandler upgrades a connection after JWT auth + RBAC scoping
// (same pipeline as REST: master auth + company DB role + user_vehicles,
// FR-5.1).
func websocketHandler(c *gin.Context) {
	tokenStr := c.Query("token")
	if tokenStr == "" {
		tokenStr = extractToken(c)
	}
	if tokenStr == "" {
		writeError(c, http.StatusUnauthorized, "UNAUTHORIZED", "missing websocket token")
		return
	}
	claims, err := parseToken(appCfg, tokenStr)
	if err != nil {
		slog.Warn("ws auth failed", "error", err.Error())
		writeError(c, http.StatusUnauthorized, "UNAUTHORIZED", "invalid or expired token")
		return
	}

	// Authoritative RBAC scope dari master→company DB (row-level security, FR-5.1).
	allowed, _, _, role, isAdmin, err := authorize(claims)
	if err != nil {
		slog.Warn("ws authorize failed", "error", err, "company", claims.CompanyCode, "user_id", claims.UserID)
		writeError(c, http.StatusForbidden, "FORBIDDEN", "no access for this token")
		return
	}

	// Resource limits (FR-5.4): send buffer / max message size.
	upgrader := websocket.Upgrader{
		ReadBufferSize:    appCfg.WebSocket.ReadBufferSize,
		WriteBufferSize:   appCfg.WebSocket.WriteBufferSize,
		HandshakeTimeout:  10 * time.Second,
		EnableCompression: false,
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			if origin == "" {
				// Non-browser client (device/CLI/tool) tidak mengirim header Origin.
				// Ini langkah aman: tanpa Origin mustahil ada serangan cross-origin
				// dari browser, jadi diizinkan (pola WebSocket umum).
				return true
			}
			return originAllowed(origin)
		},
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		slog.Warn("websocket upgrade failed", "error", err, "user_id", claims.UserID)
		return
	}

	cl := newClient(conn, make(chan []byte, appCfg.WebSocket.MaxQueue),
		claims.UserID, claims.CompanyCode, allowed, isAdmin, appHub)
	if !appHub.register(cl) {
		_ = conn.WriteMessage(websocket.TextMessage,
			mustJSON(models.WsErrorEvent{
				Event:     "ERROR",
				ErrorCode: "SERVER_FULL",
				Message:   "maximum concurrent connections reached",
			}))
		_ = conn.Close()
		internal.RBACDenialsTotal.WithLabelValues("ws_connect", "server_full").Inc()
		slog.Warn("ws connection rejected: server full", "user_id", claims.UserID)
		return
	}
	defer appHub.unregister(cl)

	internal.WSConnectionActive.WithLabelValues("global").Inc()
	defer internal.WSConnectionActive.WithLabelValues("global").Dec()

	// Kirim status koneksi (GAP #1 WS event: CONNECTION_STATUS).
	cl.enqueue(mustJSON(models.ConnectionEvent{Event: "CONNECTION_STATUS", Data: map[string]interface{}{
		"status":       "connected",
		"user_id":      claims.UserID,
		"company_code": claims.CompanyCode,
	}}))

	// Log koneksi baru (tenant-aware).
	slog.Info("ws client connected",
		"user_id", claims.UserID,
		"company", claims.CompanyCode,
		"role", role,
		"ip", c.ClientIP(),
	)

	go cl.writePump(appCfg)
	cl.readPump(appCfg)
}

// readPump reads control messages and enforces pong deadlines (FR-5.3).
func (cl *client) readPump(cfg *internal.Config) {
	conn, ok := cl.conn.(*websocket.Conn)
	if !ok {
		return
	}
	conn.SetReadLimit(int64(cfg.WebSocket.MaxMessageSize))
	_ = conn.SetReadDeadline(time.Now().Add(cfg.WebSocket.PongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(cfg.WebSocket.PongWait))
	})
	conn.SetPingHandler(func(string) error {
		_ = conn.WriteControl(websocket.PongMessage, nil, time.Now().Add(cfg.WebSocket.WriteWait))
		return nil
	})

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				slog.Debug("ws read error", "error", err, "user_id", cl.userID)
			}
			return
		}
		var msg models.SubscriptionMessage
		if err := json.Unmarshal(data, &msg); err != nil || msg.Event == "" {
			continue
		}
		switch msg.Event {
		case "SUBSCRIBE":
			if cl.canReceive(msg.Data.VehicleID) {
				cl.subs[msg.Data.VehicleID] = struct{}{}
				_ = cl.enqueueWS(mustJSON(models.ConnectionEvent{
					Event: "SUBSCRIBED",
					Data:  map[string]interface{}{"vehicle_id": msg.Data.VehicleID},
				}))
			} else {
				_ = cl.enqueueWS(mustJSON(models.WsErrorEvent{
					Event:     "ERROR",
					ErrorCode: "UNAUTHORIZED_VEHICLE",
					Message:   "You don't have access to this vehicle",
				}))
				internal.WSMessagesTotal.WithLabelValues("vehicle.update", "denied").Inc()
			}
		case "UNSUBSCRIBE":
			delete(cl.subs, msg.Data.VehicleID)
		}
	}
}

// enqueueWS is a small wrapper returning success (write pump does not need it).
func (cl *client) enqueueWS(payload []byte) error {
	cl.enqueue(payload)
	return nil
}

// writePump writes queued messages + periodic pings (FR-5.3 ping ~30s).
func (cl *client) writePump(cfg *internal.Config) {
	conn, ok := cl.conn.(*websocket.Conn)
	if !ok {
		return
	}
	ticker := time.NewTicker(cfg.WebSocket.HeartbeatInterval)
	defer func() {
		ticker.Stop()
		_ = conn.Close()
	}()

	for {
		select {
		case payload := <-cl.send:
			_ = conn.SetWriteDeadline(time.Now().Add(cfg.WebSocket.WriteWait))
			if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
				return
			}
		case <-ticker.C:
			_ = conn.SetWriteDeadline(time.Now().Add(cfg.WebSocket.WriteWait))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-cl.done:
			return
		}
	}
}

// mustJSON marshals v or returns a minimal JSON fallback.
func mustJSON(v interface{}) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte(`{"event":"ERROR","error_code":"INTERNAL_ERROR"}`)
	}
	return b
}
