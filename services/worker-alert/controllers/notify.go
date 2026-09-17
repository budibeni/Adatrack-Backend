package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/smtp"
	"strings"
	"time"

	"adatrack_gps/internal"
	"adatrack_gps/worker-alert/models"
)

// defaultChannels is applied when a user has NO preference row for a channel
// (documented default: websocket ON, external channels OFF).
var defaultChannels = map[string]bool{
	models.ChannelWebsocket: true,
	models.ChannelEmail:     false,
	models.ChannelSMS:       false,
	models.ChannelPush:      false,
}

// Recipients resolves the notification targets for one vehicle:
// users with a row-level grant (tm_user_vehicles) ∪ company Admin/Manager
// (PRD §5.9.8). Returns master tm_users rows so the email channel has an address.
func (e *Engine) Recipients(ctx context.Context, company string, vehicleID int64) ([]models.Recipient, error) {
	ids, err := e.store.VehicleGrants(ctx, company, vehicleID)
	if err != nil {
		return nil, err
	}
	admins, err := e.store.TenantAdminUserIDs(ctx, company)
	if err != nil {
		return nil, err
	}
	seen := make(map[int64]bool, len(ids)+len(admins))
	for _, id := range ids {
		seen[id] = true
	}
	for _, id := range admins {
		if !seen[id] {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	return e.store.Users(ctx, ids)
}

// Notify fans one alert out according to tm_notification_preferences:
// websocket → `notify.alert.<vehicle_id>` (RBAC fan-out by service-websocket),
// email/SMS/push → td_notifications delivery audit rows (PRD §5.9.8).
func (e *Engine) Notify(ctx context.Context, a *models.Alert) error {
	recipients, err := e.Recipients(ctx, a.CompanyCode, a.VehicleID)
	if err != nil {
		return fmt.Errorf("recipients: %w", err)
	}
	if len(recipients) == 0 {
		return nil
	}

	ids := make([]int64, len(recipients))
	for i, r := range recipients {
		ids[i] = r.UserID
	}
	prefs, err := e.store.Preferences(ctx, a.CompanyCode, ids)
	if err != nil {
		return fmt.Errorf("preferences: %w", err)
	}

	// Company-level notification rate limit (PRD §5.9.8) — one bucket/minute.
	if limited := e.notifyRateLimited(ctx, a.CompanyCode); limited {
		rows := []models.NotificationRow{}
		for _, r := range recipients {
			for _, ch := range notifyChannels {
				if !e.channelAllowed(prefs, r.UserID, ch, a.Type, a.Severity) {
					continue
				}
				rows = append(rows, models.NotificationRow{
					AlertID: a.ID, UserID: r.UserID, Channel: ch,
					Status: models.NotifySkipped, Reason: "rate_limited",
				})
			}
		}
		return e.recordNotifications(ctx, a.CompanyCode, rows)
	}

	rows := []models.NotificationRow{}
	for _, r := range recipients {
		for _, ch := range notifyChannels {
			if !e.channelAllowed(prefs, r.UserID, ch, a.Type, a.Severity) {
				continue
			}
			row := models.NotificationRow{AlertID: a.ID, UserID: r.UserID, Channel: ch}
			switch ch {
			case models.ChannelWebsocket:
				if err := e.publishNotify(ctx, a); err != nil {
					row.Status, row.Reason = models.NotifyFailed, "publish_failed: "+err.Error()
				} else {
					row.Status = models.NotifySent
				}
			case models.ChannelEmail:
				st, rs, resp := e.sendEmail(r, a)
				row.Status = st
				row.Reason = rs
				row.ResponseJSON = marshalNotificationResponse(resp)
			case models.ChannelSMS:
				st, rs, resp := e.sendSMS(r, a)
				row.Status = st
				row.Reason = rs
				row.ResponseJSON = marshalNotificationResponse(resp)
			case models.ChannelPush:
				row.Status, row.Reason = models.NotifySkipped, "push_provider_not_configured"
			}
			rows = append(rows, row)
		}
	}
	return e.recordNotifications(ctx, a.CompanyCode, rows)
}

// notifyChannels is the evaluation order of the delivery pipeline.
var notifyChannels = []string{models.ChannelWebsocket, models.ChannelEmail, models.ChannelSMS, models.ChannelPush}

// notifyRateLimited reports whether the company exceeded
// NOTIFY_RATE_LIMIT_PER_MIN (0/absent disables limiting).
func (e *Engine) notifyRateLimited(ctx context.Context, company string) bool {
	limit := e.cfg.Alert.NotifyRateLimitPerMin
	if limit <= 0 {
		return false
	}
	bucket := time.Now().UTC().Format("200601021504")
	key := "alert:notify:rate:" + company + ":" + bucket
	n, err := e.red.Incr(ctx, key)
	if err != nil {
		return false // limiter failure must not swallow notifications
	}
	if n == 1 {
		_ = e.red.Expire(ctx, key, 2*time.Minute)
	}
	return n > int64(limit)
}

// channelAllowed applies one user's preferences: a matching row wins; the
// documented default applies otherwise. `all` matches every alert type and
// min_severity filters low-severity noise.
func (e *Engine) channelAllowed(prefs []models.PrefRow, userID int64, channel, alertType, severity string) bool {
	allowed, ok := defaultChannels[channel]
	if !ok {
		allowed = false
	}
	for _, p := range prefs {
		if p.UserID != userID || p.Channel != channel {
			continue
		}
		if p.AlertType != "all" && p.AlertType != alertType {
			continue
		}
		if !p.Enabled {
			return false // explicit disable always wins
		}
		if !models.AtLeastSeverity(severity, p.MinSeverity) {
			return false
		}
		return true
	}
	return allowed
}

// marshalNotificationResponse renders a provider response for the audit row.
func marshalNotificationResponse(resp map[string]any) []byte {
	if resp == nil {
		return nil
	}
	b, err := json.Marshal(resp)
	if err != nil {
		return nil
	}
	return b
}

// recordNotifications appends the delivery audit rows (never blocks alert flow).
func (e *Engine) recordNotifications(ctx context.Context, company string, rows []models.NotificationRow) error {
	if len(rows) == 0 {
		return nil
	}
	if err := e.store.InsertNotifications(ctx, company, rows); err != nil {
		slog.Error("alert engine: notification audit write failed", "company", company, "error", err)
		return err
	}
	for _, r := range rows {
		notificationsSent.WithLabelValues(r.Channel, r.Status).Inc()
	}
	return nil
}

// sendEmail delivers one alert email; a missing SMTP host is a documented skip.
func (e *Engine) sendEmail(r models.Recipient, a *models.Alert) (status, reason string, response map[string]any) {
	if e.cfg.Alert.SMTP.Host == "" || r.Email == "" {
		return models.NotifySkipped, "smtp_not_configured", nil
	}
	if err := emailSender(e.cfg, r, a); err != nil {
		return models.NotifyFailed, "smtp_error: " + err.Error(), nil
	}
	return models.NotifySent, "", map[string]any{"to": r.Email}
}

// sendSMS delivers one alert SMS via the configured provider; provider "none"
// (default) is a documented skip.
func (e *Engine) sendSMS(r models.Recipient, a *models.Alert) (status, reason string, response map[string]any) {
	switch strings.ToLower(e.cfg.Alert.SMS.Provider) {
	case "", "none":
		return models.NotifySkipped, "sms_provider_none", nil
	case "twilio":
		// B3 ships the provider scaffold; recipient phone numbers land with the
		// B12 users schema extension — recorded as skipped until then.
		return models.NotifySkipped, "sms_recipient_phone_not_provisioned", nil
	default:
		return models.NotifySkipped, "sms_provider_unknown: " + e.cfg.Alert.SMS.Provider, nil
	}
}

// emailSender is injectable for tests.
var emailSender = func(cfg *internal.Config, r models.Recipient, a *models.Alert) error {
	addr := cfg.Alert.SMTP.Host + ":" + cfg.Alert.SMTP.Port
	from := cfg.Alert.SMTP.From
	subject := fmt.Sprintf("[ADATRACK][%s] %s — vehicle %d", strings.ToUpper(a.Severity), a.Type, a.VehicleID)
	body := fmt.Sprintf("Alert %s (%s) on vehicle %d at %s.\nMetadata: %v\n",
		a.Type, a.Severity, a.VehicleID, a.DetectedAt.Format(time.RFC3339), a.Metadata)

	msg := bytes.Join([][]byte{
		[]byte("From: " + from),
		[]byte("To: " + r.Email),
		[]byte("Subject: " + subject),
		[]byte("MIME-Version: 1.0"),
		[]byte("Content-Type: text/plain; charset=UTF-8"),
		[]byte(""),
		[]byte(body),
	}, []byte("\r\n"))

	var auth smtp.Auth
	if cfg.Alert.SMTP.Username != "" {
		host := strings.Split(addr, ":")[0]
		auth = smtp.PlainAuth("", cfg.Alert.SMTP.Username, cfg.Alert.SMTP.Password, host)
	}
	return smtp.SendMail(addr, auth, from, []string{r.Email}, msg)
}
