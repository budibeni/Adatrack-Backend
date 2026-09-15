package controllers

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// handleWS upgrades an authenticated request to a WebSocket connection
// (PRD §8.3: `ws://<host>/ws/v1/adatrack?token=<JWT>`).
//
// The handshake reuses the HTTP auth chain (JWT → revocation → user → RBAC), so
// an unauthenticated or revoked client is rejected with the normal 401/403 JSON
// envelope BEFORE any upgrade happens.
func (s *Service) handleWS(c *gin.Context) {
	identity, ok := currentIdentity(c)
	if !ok {
		respondError(c, errUnauthorized("unauthenticated"))
		return
	}
	if !s.settings.OriginAllowed(c.GetHeader("Origin")) {
		// FR-5.4: browser origins are validated; non-browser clients send none.
		wsConnectionsRejected.WithLabelValues("origin").Inc()
		respondError(c, errForbidden(CodeForbidden, "origin not allowed"))
		return
	}
	if !s.hub.canAccept() {
		// Capacity is checked BEFORE the upgrade so the client gets a clean 503.
		wsConnectionsRejected.WithLabelValues("capacity").Inc()
		respondError(c, errUnavailable("websocket capacity reached"))
		return
	}

	upgrader := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: s.writeBufferSize(),
		// CheckOrigin is already decided above; keep it explicit for defence in
		// depth (a proxy that strips the header must not open the hole).
		CheckOrigin: func(r *http.Request) bool {
			return s.settings.OriginAllowed(r.Header.Get("Origin"))
		},
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		// Upgrade already wrote the HTTP error response.
		wsConnectionsRejected.WithLabelValues("upgrade").Inc()
		slog.Debug("service-websocket: upgrade failed", "error", err, "request_id", requestID(c))
		return
	}

	client := newClient(s.hub, conn, c.ClientIP(), identity)
	if !s.hub.register(client) {
		// Unreachable in practice (canAccept ran before the upgrade) but kept as a
		// race-safe net: the response is already hijacked, so close the socket.
		_ = conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "capacity reached"))
		_ = conn.Close()
		wsConnectionsRejected.WithLabelValues("capacity").Inc()
		return
	}

	slog.Info("websocket client connected",
		"user_id", identity.user.ID, "company", identity.user.CompanyCode,
		"role", identity.user.Role, "ip", c.ClientIP(), "request_id", requestID(c))

	go client.writePump()
	client.readPump() // blocks until the client disconnects

	slog.Info("websocket client disconnected",
		"user_id", identity.user.ID, "company", identity.user.CompanyCode)
}

// writeBufferSize clamps the configured send buffer (FR-5.4: 256 KB).
func (s *Service) writeBufferSize() int {
	if s.settings.WSSendBufferSize > 0 {
		return s.settings.WSSendBufferSize
	}
	return 256 * 1024
}
