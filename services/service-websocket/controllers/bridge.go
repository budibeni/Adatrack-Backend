package controllers

import (
	"encoding/json"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus"

	"ajb_gps/internal"
	"ajb_gps/service-websocket/models"
)

// bridgeEvents counts the live updates consumed from NATS (per subject kind).
var bridgeEvents = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "ws_bridge_messages_total",
	Help: "telemetry.live messages consumed by the WebSocket bridge",
}, []string{"result"})

// Bridge consumes `telemetry.live.<IMEI>` (published by worker-live) and fans the
// updates out to the entitled WebSocket clients — queue group `websocket`
// (PRD §4.1 step 7, .agent/01-global-rules.md §10).
type Bridge struct {
	cfg  *internal.Config
	nats *internal.NATSClient
	hub  *Hub
}

// NewBridge builds the bridge.
func NewBridge(cfg *internal.Config, nats *internal.NATSClient, hub *Hub) *Bridge {
	return &Bridge{cfg: cfg, nats: nats, hub: hub}
}

// RegisterMetrics registers the bridge collectors.
func RegisterBridgeMetrics(reg *prometheus.Registry) {
	if reg == nil {
		return
	}
	reg.MustRegister(bridgeEvents)
}

// Start subscribes with the `websocket` queue group so several replicas share the
// stream without duplicating work.
func (b *Bridge) Start() (*nats.Subscription, error) {
	return b.nats.Subscribe(b.cfg.Subject("live", ">"), "websocket", b.handleMessage)
}

// handleMessage converts one live-state message into a VEHICLE_UPDATE and
// broadcasts it. Malformed payloads are counted and skipped (never panic, never
// block the subscription).
func (b *Bridge) handleMessage(msg *nats.Msg) error {
	var state models.LiveState
	if err := json.Unmarshal(msg.Data, &state); err != nil {
		bridgeEvents.WithLabelValues("invalid").Inc()
		slog.Warn("service-websocket: invalid live-state payload", "subject", msg.Subject, "error", err)
		return nil
	}
	if state.VehicleID <= 0 || state.CompanyCode == "" {
		bridgeEvents.WithLabelValues("incomplete").Inc()
		slog.Debug("service-websocket: live state without vehicle/tenant", "subject", msg.Subject)
		return nil
	}

	deviceTime := time.Unix(state.Timestamp, 0).UTC()
	if state.Timestamp <= 0 {
		deviceTime = time.Now().UTC()
	}
	data := models.VehicleUpdateData{
		IMEI:        state.IMEI,
		CompanyCode: state.CompanyCode,
		VehicleID:   state.VehicleID,
		Lat:         state.Lat,
		Lon:         state.Lon,
		Speed:       state.Speed,
		Heading:     state.Heading,
		ACC:         state.ACC,
		Status:      state.Status,
		Battery:     state.Battery,
		FuelLevel:   state.FuelLevel,
		FuelVolume:  state.FuelVolume,
		FuelTempC:   state.FuelTempC,
		Satellites:  state.Satellites,
		Altitude:    state.Altitude,
		GsmSignal:   state.GsmSignal,
		Timestamp:   deviceTime.Format(time.RFC3339),
		LastSeen:    state.LastSeen,
	}
	if b.hub != nil && b.hub.plates != nil {
		// Plate lookup is served from the in-memory cache; a cold cache returns
		// "" for this message and schedules a refresh (never blocks the
		// subscription on a DB round trip — FR-5.2 payload stays best-effort).
		data.PlateNumber = b.hub.plates.plateCachedOrScheduleRefresh(state.CompanyCode, state.VehicleID)
	}

	b.hub.Broadcast(data)
	bridgeEvents.WithLabelValues("broadcast").Inc()
	return nil
}
