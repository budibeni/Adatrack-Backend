package ws

import (
	"net/http"
	"time"
	"strings"

	"github.com/gorilla/websocket"

	"backend/internal/config"
	"backend/internal/logger"
	"backend/internal/auth"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	maxMessageSize = 512
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for now
	},
}

type Client struct {
	hub    *Hub
	conn   *websocket.Conn
	send   chan []byte
	claims *auth.Claims
}

func (c *Client) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()
	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error { c.conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	for {
		_, _, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				logger.Log.Error("Websocket error", "err", err)
			}
			break
		}
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()
	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)

			// Add queued chat messages to the current websocket message.
			n := len(c.send)
			for i := 0; i < n; i++ {
				w.Write([]byte{'\n'})
				w.Write(<-c.send)
			}

			if err := w.Close(); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func ServeWS(hub *Hub, w http.ResponseWriter, r *http.Request, cfg *config.Config) {
	tokenStr := ""
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		tokenStr = strings.TrimPrefix(authHeader, "Bearer ")
	} else if proto := r.Header.Get("Sec-WebSocket-Protocol"); proto != "" {
		tokenStr = proto
	}
	
	claims, err := auth.ValidateToken(cfg, tokenStr)
	if err != nil {
		logger.Log.Warn("WS connection unauthorized", "err", err)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var responseHeader http.Header
	if proto := r.Header.Get("Sec-WebSocket-Protocol"); proto != "" {
		responseHeader = http.Header{"Sec-WebSocket-Protocol": {proto}}
	}
	conn, err := upgrader.Upgrade(w, r, responseHeader)
	if err != nil {
		logger.Log.Error("WS Upgrade error", "err", err)
		return
	}

	client := &Client{
		hub:    hub,
		conn:   conn,
		send:   make(chan []byte, 256),
		claims: claims,
	}
	
	client.hub.register <- client

	go client.writePump()
	go client.readPump()
}
