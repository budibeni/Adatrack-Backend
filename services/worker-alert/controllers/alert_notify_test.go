package controllers

// alert_notify_test.go — the PRD §5.9.8 notification pipeline helpers:
// preference resolution (channelAllowed), recipient union, provider delivery
// (email via the injectable sender, SMS provider matrix), rate limiting and
// the td_notifications audit writer.

import (
	"context"
	"testing"
	"time"

	"adatrack_gps/internal"
	"adatrack_gps/worker-alert/models"
)

func notifyPrefs() []models.PrefRow {
	return []models.PrefRow{
		{UserID: 1, AlertType: "all", Channel: models.ChannelEmail, Enabled: true, MinSeverity: models.SeverityHigh},
		{UserID: 1, AlertType: models.AlertSOS, Channel: models.ChannelSMS, Enabled: true, MinSeverity: models.SeverityLow},
		{UserID: 2, AlertType: "all", Channel: models.ChannelEmail, Enabled: false},
		{UserID: 2, AlertType: "all", Channel: models.ChannelPush, Enabled: true, MinSeverity: models.SeverityLow},
	}
}

// TestChannelAllowedMatrix covers every rule of the preference resolution:
// no row → documented default; explicit disable wins; min_severity filters;
// type mismatch falls back to the default.
func TestChannelAllowedMatrix(t *testing.T) {
	_, eng, _ := newMiniredisWorker(t, newFakeAlertStore())
	prefs := notifyPrefs()

	cases := []struct {
		name    string
		userID  int64
		channel string
		aType   string
		sev     string
		want    bool
	}{
		{"default websocket on", 3, models.ChannelWebsocket, models.AlertOffline, models.SeverityLow, true},
		{"default push off", 3, models.ChannelPush, models.AlertSOS, models.SeverityCritical, false},
		{"email allowed above min severity", 1, models.ChannelEmail, models.AlertOffline, models.SeverityHigh, true},
		{"email filtered below min severity", 1, models.ChannelEmail, models.AlertOffline, models.SeverityMedium, false},
		{"sms type match", 1, models.ChannelSMS, models.AlertSOS, models.SeverityLow, true},
		{"sms type mismatch → default off", 1, models.ChannelSMS, models.AlertOffline, models.SeverityCritical, false},
		{"explicit disable wins", 2, models.ChannelEmail, models.AlertSOS, models.SeverityCritical, false},
		{"push enabled any severity", 2, models.ChannelPush, models.AlertOffline, models.SeverityLow, true},
		{"unknown channel", 1, "pigeon", models.AlertSOS, models.SeverityCritical, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := eng.channelAllowed(prefs, tc.userID, tc.channel, tc.aType, tc.sev)
			if got != tc.want {
				t.Errorf("channelAllowed(%d,%s,%s,%s) = %v, want %v",
					tc.userID, tc.channel, tc.aType, tc.sev, got, tc.want)
			}
		})
	}
}

// TestRecipientsUnion: row-level grants ∪ company admins, deduplicated; no
// targets → nil without a store round trip for users.
func TestRecipientsUnion(t *testing.T) {
	store := newFakeAlertStore()
	_, eng, _ := newMiniredisWorker(t, store)
	ctx := context.Background()

	store.grants = []int64{10, 11}
	store.admins = []int64{11, 12}
	store.users = []models.Recipient{{UserID: 10}, {UserID: 11}, {UserID: 12}}

	got, err := eng.Recipients(ctx, "DEV001", 7)
	if err != nil {
		t.Fatalf("recipients: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("recipients = %+v, want the deduped union of 3", got)
	}

	// Store errors propagate.
	store.adminsErr = context.DeadlineExceeded
	if _, err := eng.Recipients(ctx, "DEV001", 7); err == nil {
		t.Error("admin lookup error must surface")
	}

	// Empty target set → nil, and Users must not be called.
	store2 := newFakeAlertStore()
	_, eng2, _ := newMiniredisWorker(t, store2)
	if got, err := eng2.Recipients(ctx, "DEV001", 7); err != nil || got != nil {
		t.Errorf("no recipients = (%+v, %v), want (nil, nil)", got, err)
	}
	if len(store2.users) != 0 {
		t.Error("Users must not be queried when no ids resolve")
	}
}

// TestSendEmailChannels: unconfigured SMTP or address → skipped; delivery
// failure → failed; success → sent with the address echoed.
func TestSendEmailChannels(t *testing.T) {
	_, eng, cfg := newMiniredisWorker(t, newFakeAlertStore())
	r := models.Recipient{UserID: 1, Email: "ops@example.com"}
	a := alertDraft()

	// Default config has no SMTP host → documented skip.
	st, reason, resp := eng.sendEmail(r, a)
	if st != models.NotifySkipped || reason != "smtp_not_configured" || resp != nil {
		t.Errorf("unconfigured = (%s,%s,%v), want skipped", st, reason, resp)
	}

	// Injected sender success.
	cfg.Alert.SMTP.Host = "smtp.example.com"
	cfg.Alert.SMTP.Port = "25"
	cfg.Alert.SMTP.From = "alerts@example.com"
	restore := emailSender
	emailSender = func(*internal.Config, models.Recipient, *models.Alert) error { return nil }
	st, reason, resp = eng.sendEmail(r, a)
	if st != models.NotifySent || reason != "" || resp["to"] != r.Email {
		t.Errorf("sent = (%s,%s,%v), want sent + address echo", st, reason, resp)
	}

	// Injected sender failure.
	emailSender = func(*internal.Config, models.Recipient, *models.Alert) error {
		return context.DeadlineExceeded
	}
	st, reason, _ = eng.sendEmail(r, a)
	if st != models.NotifyFailed || reason == "" {
		t.Errorf("failed = (%s,%s), want failed + reason", st, reason)
	}
	emailSender = restore

	// Missing recipient address → skip even with SMTP configured.
	st, reason, _ = eng.sendEmail(models.Recipient{UserID: 2}, a)
	if st != models.NotifySkipped || reason != "smtp_not_configured" {
		t.Errorf("no address = (%s,%s), want skipped", st, reason)
	}
}

// TestSendSMSProviders: the provider matrix of §7.3.
func TestSendSMSProviders(t *testing.T) {
	_, eng, cfg := newMiniredisWorker(t, newFakeAlertStore())
	r := models.Recipient{UserID: 1}
	a := alertDraft()

	for provider, wantReason := range map[string]string{
		"":          "sms_provider_none",
		"none":      "sms_provider_none",
		"NONE":      "sms_provider_none",
		"twilio":    "sms_recipient_phone_not_provisioned",
		"aws_sns":   "sms_provider_unknown: aws_sns",
		"vendor999": "sms_provider_unknown: vendor999",
	} {
		cfg.Alert.SMS.Provider = provider
		st, reason, _ := eng.sendSMS(r, a)
		if st != models.NotifySkipped || reason != wantReason {
			t.Errorf("provider %q = (%s,%s), want (skipped,%s)", provider, st, reason, wantReason)
		}
	}
}

// TestMarshalNotificationResponse: nil → nil, value → JSON.
func TestMarshalNotificationResponse(t *testing.T) {
	if got := marshalNotificationResponse(nil); got != nil {
		t.Errorf("nil response = %v, want nil", got)
	}
	if got := marshalNotificationResponse(map[string]any{"ok": true}); got == nil {
		t.Error("a response map must marshal to JSON")
	}
}

// TestNotifyRateLimited: 0 disables; the counter trips beyond the limit and the
// limiter failure never blocks.
func TestNotifyRateLimited(t *testing.T) {
	store := newFakeAlertStore()
	w, eng, cfg := newMiniredisWorker(t, store)
	ctx := context.Background()

	cfg.Alert.NotifyRateLimitPerMin = 0
	if eng.notifyRateLimited(ctx, "DEV001") {
		t.Error("limit 0 must disable the limiter")
	}

	cfg.Alert.NotifyRateLimitPerMin = 2
	if eng.notifyRateLimited(ctx, "DEV001") {
		t.Error("first increment must be under the limit")
	}
	_ = eng.notifyRateLimited(ctx, "DEV001")
	if !eng.notifyRateLimited(ctx, "DEV001") {
		t.Error("third increment within the minute must trip the limiter")
	}

	// A different company has its own bucket.
	if eng.notifyRateLimited(ctx, "OTHER") {
		t.Error("buckets are per company")
	}

	// Transport failure → limiter never blocks notifications.
	_ = w.red.Close()
	if eng.notifyRateLimited(ctx, "DEV001") {
		t.Error("a limiter failure must read as not-limited")
	}
}

// TestRecordNotifications: empty rows are a no-op; a store error surfaces; the
// success path forwards every row.
func TestRecordNotifications(t *testing.T) {
	store := newFakeAlertStore()
	_, eng, _ := newMiniredisWorker(t, store)
	ctx := context.Background()

	if err := eng.recordNotifications(ctx, "DEV001", nil); err != nil {
		t.Errorf("empty rows: %v", err)
	}
	rows := []models.NotificationRow{{AlertID: 1, UserID: 2, Channel: models.ChannelWebsocket, Status: models.NotifySent}}
	if err := eng.recordNotifications(ctx, "DEV001", rows); err != nil {
		t.Fatalf("record: %v", err)
	}
	if len(store.notifRows) != 1 {
		t.Errorf("stored rows = %d, want 1", len(store.notifRows))
	}

	store.notifErr = context.DeadlineExceeded
	if err := eng.recordNotifications(ctx, "DEV001", rows); err == nil {
		t.Error("audit write failure must surface")
	}
}

// TestFreshCadence: zero `every` falls back to 30 s; zero timestamps are never
// fresh; inside the window is fresh.
func TestFreshCadence(t *testing.T) {
	if fresh(time.Time{}, time.Second) {
		t.Error("a zero timestamp must never be fresh")
	}
	if !fresh(time.Now().Add(-time.Second), 30*time.Second) {
		t.Error("a 1s-old timestamp must be fresh within 30s")
	}
	if fresh(time.Now().Add(-31*time.Second), 30*time.Second) {
		t.Error("a 31s-old timestamp must be stale within 30s")
	}
	if !fresh(time.Now().Add(-time.Second), 0) {
		t.Error("a zero cadence must fall back to 30s")
	}
}
