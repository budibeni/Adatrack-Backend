package controllers

import (
	"encoding/json"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"ajb_gps/internal"
	"ajb_gps/service-websocket/models"

	"github.com/nats-io/nats.go"
)

// bridgeHandle is the NATS subscriber (queue group "websocket", GAP #9)
// for telemetry.raw.<IMEI>. It resolves (company, imei) → vehicle via the
// registry (company-scoped, anti-leak) and fans VEHICLE_UPDATE out via the
// hub — only clients of the same company & authorised (RBAC) receive it.
func bridgeHandle(msg *nats.Msg) error {
	var tm models.TelemetryMessage
	if err := json.Unmarshal(msg.Data, &tm); err != nil {
		slog.Error("bridge: unmarshal telemetry failed", "error", err, "subject", msg.Subject)
		return nil
	}
	if tm.IMEI == "" {
		return nil
	}
	if tm.Timestamp <= 0 {
		tm.Timestamp = time.Now().Unix()
	}

	start := time.Now()
	veh, ok := vehReg.lookup(tm.CompanyCode, tm.IMEI)
	if !ok {
		// IMEI tidak dikenal / company tidak valid — tidak di-push (anti-leak).
		return nil
	}

	status := "IDLE"
	if tm.Speed > 0 {
		status = "MOVING"
	}
	ts := time.Unix(tm.Timestamp, 0).UTC().Format(time.RFC3339)

	event := models.VehicleUpdateEvent{
		Event: "VEHICLE_UPDATE",
		Data: models.VehicleUpdateData{
			VehicleID:   veh.ID,
			IMEI:        tm.IMEI,
			CompanyCode: tm.CompanyCode,
			PlateNumber: veh.Plate,
			DeviceModel: veh.Model,
			Lat:         tm.Lat,
			Lon:         tm.Lon,
			Speed:       tm.Speed,
			Heading:     tm.Heading,
			Acc:         tm.Speed > 0,
			Status:      status,
			Battery:     tm.Battery,
			Timestamp:   ts,
		},
	}
	payload, err := json.Marshal(event)
	if err != nil {
		slog.Error("bridge: marshal event failed", "error", err)
		return nil
	}

	appHub.broadcast(tm.CompanyCode, veh.ID, payload)
	internal.WSMessagesTotal.WithLabelValues("vehicle.update", "send").Inc()
	internal.WSMessageDuration.WithLabelValues("vehicle_update").Observe(time.Since(start).Seconds())
	return nil
}

// notifyHandle consumes subject notify.alert.<vehicle_id> (queue group "websocket")
// published by worker-alert (B3 notifikasi). It wraps the AlertNotification as an
// ALERT_NOTIFICATION event and fans it out via the hub — RBAC + tenant-filtered
// by canReceive (same companyCode + vehicle access).
func notifyHandle(msg *nats.Msg) error {
	start := time.Now()

	var notif models.AlertNotification
	if err := json.Unmarshal(msg.Data, &notif); err != nil {
		slog.Error("notify: unmarshal failed", "subject", msg.Subject, "error", err)
		return nil
	}

	// subject: notify.alert.<vehicle_id>;
	parts := strings.Split(msg.Subject, ".")
	vid, err := strconv.ParseUint(parts[len(parts)-1], 10, 64)
	if err != nil {
		slog.Warn("notify: invalid vehicle_id in subject", "subject", msg.Subject)
		return nil
	}

	// If the payload's vehicle_id disagrees with the subject, trust the subject
	// (it is the authoritative broadcast target for RBAC).
	notif.VehicleID = vid

	event := models.AlertNotificationEvent{
		Event: "ALERT_NOTIFICATION",
		Data:  notif,
	}
	payload, err := json.Marshal(event)
	if err != nil {
		slog.Error("notify: marshal failed", "error", err)
		return nil
	}

	appHub.broadcast(notif.CompanyCode, vid, payload)
	internal.WSMessagesTotal.WithLabelValues("alert.notification", "send").Inc()
	internal.WSMessageDuration.WithLabelValues("alert_notification").Observe(time.Since(start).Seconds())
	return nil
}

// mediaHandle consumes subject media.event.<company> (queue group "websocket")
// published by service-media (B5b, FR-8.5). It wraps the media payload as a
// MEDIA_EVENT WS event and fans it out via the hub — RBAC/tenant-filtered to
// clients of the same company with access to the vehicle (hub.broadcast).
func mediaHandle(msg *nats.Msg) error {
	start := time.Now()

	// NATS payload mirrors MediaEventData fields at top level (service-media
	// publishes MediaEventsEvent; json tags match). "event" field is ignored.
	var mev models.MediaEventData
	if err := json.Unmarshal(msg.Data, &mev); err != nil {
		slog.Error("media: unmarshal failed", "subject", msg.Subject, "error", err)
		return nil
	}
	if mev.CompanyCode == "" {
		slog.Warn("media: missing company_code in event", "subject", msg.Subject)
		return nil
	}

	event := models.MediaEventWS{
		Event: "MEDIA_EVENT",
		Data:  mev,
	}
	payload, err := json.Marshal(event)
	if err != nil {
		slog.Error("media: marshal failed", "error", err)
		return nil
	}

	appHub.broadcast(mev.CompanyCode, mev.VehicleID, payload)
	internal.WSMessagesTotal.WithLabelValues("media.event", "send").Inc()
	internal.WSMessageDuration.WithLabelValues("media_event").Observe(time.Since(start).Seconds())
	return nil
}
