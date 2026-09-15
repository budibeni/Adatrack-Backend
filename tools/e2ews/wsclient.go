package main

import (
	"encoding/json"
	"fmt"
	"net/http"
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
func dialWS(opt options, token string, timeout time.Duration) (*websocket.Conn, *http.Response, error) {
	dialer := websocket.Dialer{HandshakeTimeout: timeout}
	return dialer.Dial(opt.wsURL(token), nil)
}

// wsWrite sends one client frame.
func wsWrite(conn *websocket.Conn, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, body)
}

// wsReadEvent waits for the next frame with the given event name, skipping
// other frames (heartbeats/acks can interleave). It returns the envelope plus
// how long the wait took.
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
			return wsEnvelope{}, time.Since(start), fmt.Errorf("decode frame %s: %w", trim(string(raw), 120), uerr)
		}
		if strings.EqualFold(envelope.Event, want) {
			return envelope, time.Since(start), nil
		}
		if time.Now().After(deadline) {
			return wsEnvelope{}, time.Since(start), fmt.Errorf("timed out waiting for %s (last event %q)", want, envelope.Event)
		}
	}
}

// wsSubscribe subscribes to vehicle ids and waits for the ACK.
func wsSubscribe(conn *websocket.Conn, vehicleIDs []int64, timeout time.Duration) (wsEnvelope, error) {
	if err := wsWrite(conn, map[string]any{"action": "subscribe", "vehicle_ids": vehicleIDs}); err != nil {
		return wsEnvelope{}, err
	}
	envelope, _, err := wsReadEvent(conn, "SUBSCRIBED", timeout)
	return envelope, err
}

// wsVehicleUpdate is the decoded FR-5.2 payload.
type wsVehicleUpdate struct {
	IMEI        string   `json:"imei"`
	CompanyCode string   `json:"company_code"`
	VehicleID   int64    `json:"vehicle_id"`
	PlateNumber string   `json:"plate_number"`
	Lat         float64  `json:"lat"`
	Lon         float64  `json:"lon"`
	Speed       float64  `json:"speed"`
	ACC         *bool    `json:"acc"`
	Status      string   `json:"status"`
	FuelLevel   *float64 `json:"fuel_level"`
	Satellites  uint8    `json:"satellites"`
	Altitude    int16    `json:"altitude"`
	GsmSignal   uint8    `json:"gsm_signal"`
	Timestamp   string   `json:"timestamp"`
}

// decodeUpdate decodes a VEHICLE_UPDATE payload.
func decodeUpdate(envelope wsEnvelope) (wsVehicleUpdate, error) {
	var update wsVehicleUpdate
	if len(envelope.Data) == 0 {
		return update, fmt.Errorf("VEHICLE_UPDATE carried no data")
	}
	if err := json.Unmarshal(envelope.Data, &update); err != nil {
		return update, err
	}
	return update, nil
}
