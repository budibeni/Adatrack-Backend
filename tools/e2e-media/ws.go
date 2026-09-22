package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// wsEnvelope mirrors the server→client frame (PRD §8.3).
type wsEnvelope struct {
	Event     string          `json:"event"`
	Data      json.RawMessage `json:"data,omitempty"`
	ErrorCode string          `json:"error_code,omitempty"`
	Message   string          `json:"message,omitempty"`
	Timestamp string          `json:"timestamp,omitempty"`
}

// dialWS opens an authenticated WebSocket (token via query parameter).
func dialWS(opt options, token string, timeout time.Duration) (*websocket.Conn, error) {
	dialer := websocket.Dialer{HandshakeTimeout: timeout}
	conn, _, err := dialer.Dial(opt.wsEndpoint(token), nil)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", opt.wsEndpoint(token), err)
	}
	return conn, nil
}

// wsWrite sends one client frame.
func wsWrite(conn *websocket.Conn, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, body)
}

// wsReadEvent waits for the next frame with the given event name, skipping the
// others (heartbeats/acks interleave).
func wsReadEvent(conn *websocket.Conn, want string, timeout time.Duration) (wsEnvelope, time.Duration, error) {
	start := time.Now()
	deadline := start.Add(timeout)
	for {
		if err := conn.SetReadDeadline(deadline); err != nil {
			return wsEnvelope{}, 0, err
		}
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return wsEnvelope{}, time.Since(start), fmt.Errorf("read frame: %w", err)
		}
		var envelope wsEnvelope
		if uerr := json.Unmarshal(raw, &envelope); uerr != nil {
			return wsEnvelope{}, time.Since(start), fmt.Errorf("decode frame %s: %w", trim(raw, 120), uerr)
		}
		if strings.EqualFold(envelope.Event, want) {
			return envelope, time.Since(start), nil
		}
		if time.Now().After(deadline) {
			return wsEnvelope{}, time.Since(start),
				fmt.Errorf("timed out waiting for %s (last event %q)", want, envelope.Event)
		}
	}
}

// wsSubscribe subscribes to vehicle ids and waits for the ACK.
func wsSubscribe(conn *websocket.Conn, vehicleIDs []int64, timeout time.Duration) error {
	if err := wsWrite(conn, map[string]any{"action": "subscribe", "vehicle_ids": vehicleIDs}); err != nil {
		return err
	}
	_, _, err := wsReadEvent(conn, "SUBSCRIBED", timeout)
	return err
}
