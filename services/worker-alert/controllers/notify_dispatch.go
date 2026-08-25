package controllers

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"ajb_gps/internal"
	"ajb_gps/worker-alert/models"
)

// dispatch sends one notification through its configured channel:
//   - websocket → NATS notify.alert.<vehicle_id> (service-websocket fan-out RBAC)
//   - email/sms → audit row notifications (migration 010); pengiriman provider
//     hanya bila kredensial env tersedia, selain itu status 'skipped' (no
//     silent drop — setiap keputusan tercatat & di-log).
func (wa *WorkerAlert) dispatch(s store, company string, userID uint64, pref models.NotifPreference,
	rec models.AlertRecord, tm models.TelemetryMessage) {

	subject := fmt.Sprintf("[%s] %s alert: vehicle #%d", company, rec.AlertType, rec.VehicleID)
	body := fmt.Sprintf("%s (alert %d, imei %s)", rec.Description, rec.ID, tm.IMEI)

	switch pref.Channel {
	case "websocket":
		payload := map[string]interface{}{
			"alert_id":     rec.ID,
			"vehicle_id":   rec.VehicleID,
			"imei":         tm.IMEI,
			"company_code": company,
			"alert_type":   rec.AlertType,
			"severity":     rec.Severity,
			"status":       rec.Status,
			"message":      rec.Description,
			"lat":          nilIfZeroPtr(rec.VehicleLat),
			"lon":          nilIfZeroPtr(rec.VehicleLon),
			"speed":        tm.Speed,
			"triggered_at": time.Now().Unix(),
		}
		data, _ := json.Marshal(payload)
		sub := wa.cfg.SubjectPlain("notify", "alert", strconv.FormatUint(rec.VehicleID, 10))
		if err := wa.nac.Publish(sub, data); err != nil {
			slog.Error("ws notification publish failed", "company", company, "subject", sub, "error", err)
			wa.metrics.notifySent.WithLabelValues(company, "websocket", "failed").Inc()
			recordAudit(s, company, userID, rec, "websocket", subject, body, "failed", err.Error())
			return
		}
		wa.metrics.notifySent.WithLabelValues(company, "websocket", "sent").Inc()
		wa.metrics.incPublished(sub)
		recordAudit(s, company, userID, rec, "websocket", subject, body, "sent", "")
		slog.Debug("ws notification dispatched", "company", company, "user_id", userID, "alert_id", rec.ID)

	case "email", "sms", "push":
		status, errMsg := wa.deliverExternal(pref)
		wa.metrics.notifySent.WithLabelValues(company, pref.Channel, status).Inc()
		if err := recordAudit(s, company, userID, rec, pref.Channel, subject, body, status, errMsg); err != nil {
			slog.Error("notification audit insert failed", "company", company, "user_id", userID, "error", err)
			wa.metrics.incError(company, "notify_audit")
		}

	default:
		slog.Warn("unknown notification channel skipped", "company", company, "channel", pref.Channel)
	}
}

// deliverExternal attempts a real send when provider env is configured;
// otherwise records 'skipped' with reason (audit tetap tercatat).
// Pengiriman provider nyata (SMTP/Twilio) adalah integrasi B4 hardening.
func (wa *WorkerAlert) deliverExternal(pref models.NotifPreference) (status, errMsg string) {
	switch pref.Channel {
	case "email":
		if internal.EnvOr("SMTP_HOST", "") == "" {
			return "skipped", "SMTP_HOST not configured (dev)"
		}
		return "pending", ""
	case "sms":
		if internal.EnvOr("SMS_API_KEY", "") == "" {
			return "skipped", "SMS_API_KEY not configured (dev)"
		}
		return "pending", ""
	default:
		return "skipped", "channel push belum didukung (B3)"
	}
}

// recordAudit writes the notifications row; failures are logged + counted.
func recordAudit(s store, company string, userID uint64, rec models.AlertRecord,
	channel, subject, body, status, errMsg string) error {
	if err := s.RecordNotification(userID, rec.ID, channel, lowerCode(rec.AlertType), subject, body, status, errMsg); err != nil {
		slog.Error("notifications insert failed", "company", company, "alert_id", rec.ID,
			"user_id", userID, "channel", channel, "error", err)
		return err
	}
	return nil
}

func nilIfZeroPtr(p *float64) interface{} {
	if p == nil || *p == 0 {
		return nil
	}
	return *p
}
