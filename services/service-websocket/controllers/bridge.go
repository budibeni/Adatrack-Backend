package controllers

import (
	"encoding/json"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus"

	"adatrack_gps/internal"
	"adatrack_gps/service-websocket/models"
)

// bridgeEvents counts the live updates consumed from NATS (per subject kind).
var bridgeEvents = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "ws_bridge_messages_total",
	Help: "telemetry.live messages consumed by the WebSocket bridge",
}, []string{"result"})

// alertEvents counts the alert-notification and media-event frames consumed by
// the bridge (PRD §8.3: `notify.alert.<vehicle_id>` + `MEDIA_EVENT`).
var alertEvents = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "ws_alert_bridge_messages_total",
	Help: "notify.alert / media.event frames consumed and fanned out by the WebSocket bridge",
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
	reg.MustRegister(bridgeEvents, alertEvents)
}

// Start subscribes the live-state bridge plus the alert-notification and
// media-event bridges with the `websocket` queue group, so several replicas share
// the stream without duplicating work (PRD §4.1 step 7).
func (b *Bridge) Start() ([]*nats.Subscription, error) {
	live, err := b.nats.Subscribe(b.cfg.Subject("live", ">"), "websocket", b.handleMessage)
	if err != nil {
		return nil, err
	}
	subs := []*nats.Subscription{live}

	notify, nerr := b.nats.Subscribe(b.cfg.SubjectPlain("notify", "alert", ">"), "websocket", b.handleAlertNotify)
	if nerr != nil {
		// Roll back the live subscription so a partial bridge is never left
		// running without its siblings.
		b.nats.Unsubscribe(live)
		return nil, nerr
	}
	subs = append(subs, notify)

	media, merr := b.nats.Subscribe(b.cfg.SubjectPlain("media", "event", ">"), "websocket", b.handleMediaEvent)
	if merr != nil {
		for _, sub := range subs {
			b.nats.Unsubscribe(sub)
		}
		return nil, merr
	}
	return append(subs, media), nil
}

// handleAlertNotify fans a `notify.alert.<vehicle_id>` frame out to the clients
// subscribed to that vehicle (PRD §8.3). The event name is the documented
// subject, so a client can match on it directly.
func (b *Bridge) handleAlertNotify(msg *nats.Msg) error {
	var alert models.AlertNotify
	if err := json.Unmarshal(msg.Data, &alert); err != nil {
		alertEvents.WithLabelValues("invalid").Inc()
		slog.Warn("service-websocket: invalid notify.alert payload", "subject", msg.Subject, "error", err)
		return nil
	}
	company := strings.ToUpper(strings.TrimSpace(alert.Company))
	vehicleID := alert.VehicleID
	if vehicleID <= 0 {
		// Fall back to the subject suffix (`notify.alert.<vehicle_id>`).
		vehicleID = trailingID(msg.Subject)
	}
	if company == "" || vehicleID <= 0 {
		alertEvents.WithLabelValues("incomplete").Inc()
		slog.Debug("service-websocket: notify.alert without tenant/vehicle", "subject", msg.Subject)
		return nil
	}

	event := models.EventNotifyAlertPrefix + strconv.FormatInt(vehicleID, 10)
	delivered := b.hub.PublishEvent(company, vehicleID, event, alert)
	alertEvents.WithLabelValues("broadcast").Inc()
	slog.Debug("service-websocket: alert notification fanned out", "company", company,
		"vehicle_id", vehicleID, "type", alert.Type, "severity", alert.Severity, "clients", delivered)
	return nil
}

// handleMediaEvent fans `media.event.<company_code>` out as a MEDIA_EVENT frame
// (FR-8.5) to the clients subscribed to the affected vehicle.
func (b *Bridge) handleMediaEvent(msg *nats.Msg) error {
	var event models.MediaEventData
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		alertEvents.WithLabelValues("invalid").Inc()
		slog.Warn("service-websocket: invalid media.event payload", "subject", msg.Subject, "error", err)
		return nil
	}
	company := strings.ToUpper(strings.TrimSpace(event.CompanyCode))
	if company == "" {
		// The subject carries the tenant (`media.event.<company>`).
		company = strings.ToUpper(strings.TrimSpace(strings.TrimPrefix(msg.Subject, "media.event.")))
	}
	if company == "" || event.VehicleID <= 0 {
		alertEvents.WithLabelValues("incomplete").Inc()
		slog.Debug("service-websocket: media.event without tenant/vehicle", "subject", msg.Subject)
		return nil
	}

	delivered := b.hub.PublishEvent(company, event.VehicleID, models.EventMediaEvent, event)
	alertEvents.WithLabelValues("media").Inc()
	slog.Debug("service-websocket: media event fanned out", "company", company,
		"vehicle_id", event.VehicleID, "media_id", event.ID, "clients", delivered)
	return nil
}

// trailingID parses the last dot-separated segment of a subject as an id.
func trailingID(subject string) int64 {
	idx := strings.LastIndex(subject, ".")
	if idx < 0 || idx == len(subject)-1 {
		return 0
	}
	raw := subject[idx+1:]
	if len(raw) > 18 {
		return 0
	}
	var id int64
	for _, r := range raw {
		if r < '0' || r > '9' {
			return 0
		}
		id = id*10 + int64(r-'0')
	}
	return id
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
