package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"adatrack_gps/internal"
	"adatrack_gps/worker-alert/models"
)

// Alert subject categories (PRD §4.1 / .agent/01-global-rules.md §10).
var alertCategory = map[string]string{
	models.AlertGeofenceBreach: "geofence",
	models.AlertOverspeeding:   "speed",
	models.AlertBatteryLow:     "battery",
	models.AlertOffline:        "offline",
	models.AlertSOS:            "sos",
	models.AlertRouteDeviation: "route",
	models.AlertFuelDrop:       "fuel",
	models.AlertRefuel:         "fuel",
}

// Engine raises alerts: dedup (Redis fast path + DB open-row guard) → persist
// `th_alerts` → publish `alert.<category>.<company>` → fan out notifications
// (PRD §5.9, acceptance B3: trigger → alert + persist + publish → notification).
type Engine struct {
	cfg   *internal.Config
	red   *internal.RedisClient
	nats  *internal.NATSClient
	store Store

	// nowFunc is overridable in tests.
	nowFunc func() time.Time
}

// NewEngine wires the engine.
func NewEngine(cfg *internal.Config, red *internal.RedisClient, nats *internal.NATSClient, store Store) *Engine {
	return &Engine{cfg: cfg, red: red, nats: nats, store: store, nowFunc: time.Now}
}

// dedupKeyPrefix is the Redis namespace of the dedup window fast path.
const dedupKeyPrefix = "alert:dedup:"

// RaiseAlert persists + publishes + notifies. It returns the stored alert or
// nil when the dedup window suppressed the trigger.
func (e *Engine) RaiseAlert(ctx context.Context, a *models.Alert) (*models.Alert, error) {
	if a.CompanyCode == "" || a.VehicleID <= 0 || a.Type == "" {
		return nil, fmt.Errorf("alert engine: incomplete alert draft (company/vehicle/type required)")
	}
	if a.Status == "" {
		a.Status = models.StatusOpen
	}
	if a.DetectedAt.IsZero() {
		a.DetectedAt = e.nowFunc().UTC()
	}

	// Fast path: Redis SETNX with the dedup window as TTL. When the key exists
	// the same identity already fired inside the window — skip DB work.
	window := e.cfg.Alert.DedupWindow
	if window <= 0 {
		window = 5 * time.Minute
	}
	key := dedupKeyPrefix + a.CompanyCode + ":" + a.DedupKey
	ok, err := e.red.SetNX(ctx, key, "1", window)
	if err != nil {
		// Redis failure never blocks detection: the DB open-row guard remains.
		slog.Warn("alert engine: dedup fast path unavailable", "key", key, "error", err)
	} else if !ok {
		alertsDeduped.WithLabelValues(a.Type, a.CompanyCode).Inc()
		return nil, nil
	}

	inserted, err := e.store.InsertAlert(ctx, a.CompanyCode, a)
	if err != nil {
		alertPersistErrors.Inc()
		// Release the fast-path lock so a retry can insert (no silent drop).
		if derr := e.red.Del(ctx, key); derr != nil {
			slog.Warn("alert engine: dedup key release failed", "key", key, "error", derr)
		}
		return nil, fmt.Errorf("alert engine: insert alert: %w", err)
	}
	if !inserted {
		// An open alert with the same identity exists — dedup guard.
		alertsDeduped.WithLabelValues(a.Type, a.CompanyCode).Inc()
		return nil, nil
	}

	alertsRaised.WithLabelValues(a.Type, a.Severity, a.CompanyCode).Inc()
	if err := e.publish(ctx, a); err != nil {
		slog.Error("alert engine: publish failed", "type", a.Type, "company", a.CompanyCode,
			"vehicle_id", a.VehicleID, "error", err)
	}
	if err := e.Notify(ctx, a); err != nil {
		slog.Error("alert engine: notification pipeline failed", "alert_id", a.ID,
			"company", a.CompanyCode, "error", err)
	}
	return a, nil
}

// UpdateRouteDeviation refreshes the max deviation on an open alert when the
// same trigger repeats with a larger distance (PRD §5.9.2).
func (e *Engine) UpdateRouteDeviation(ctx context.Context, a *models.Alert, meters float64) {
	if err := e.store.UpdateRouteDeviation(ctx, a.CompanyCode, a.DedupKey, meters); err != nil {
		slog.Warn("alert engine: route deviation update failed", "error", err)
	}
}

// publish fans one alert out on `alert.<category>.<company>` (JetStream stream
// `alert`, PRD §4.1).
func (e *Engine) publish(ctx context.Context, a *models.Alert) error {
	category, ok := alertCategory[a.Type]
	if !ok {
		category = a.Type
	}
	subject := e.cfg.SubjectPlain("alert", category, a.CompanyCode)
	payload, err := json.Marshal(a)
	if err != nil {
		return err
	}
	if err := e.nats.PublishJetStream(subject, payload); err != nil {
		return err
	}
	slog.Info("alert published", "subject", subject, "alert_id", a.ID, "type", a.Type,
		"severity", a.Severity, "vehicle_id", a.VehicleID)
	return nil
}

// publishNotify sends the websocket-channel fan-out frame for one alert
// (`notify.alert.<vehicle_id>`, consumed by service-websocket with RBAC).
func (e *Engine) publishNotify(ctx context.Context, a *models.Alert) error {
	subject := e.cfg.SubjectPlain("notify", "alert", fmt.Sprintf("%d", a.VehicleID))
	payload, err := json.Marshal(map[string]any{
		"alert_id":    a.ID,
		"type":        a.Type,
		"severity":    a.Severity,
		"company":     a.CompanyCode,
		"vehicle_id":  a.VehicleID,
		"imei":        a.IMEI,
		"lat":         a.Lat,
		"lon":         a.Lon,
		"metadata":    a.Metadata,
		"detected_at": a.DetectedAt.Format(time.RFC3339),
	})
	if err != nil {
		return err
	}
	return e.nats.Publish(subject, payload)
}

// resolveOffline marks a vehicle's open OFFLINE alert as resolved when a fresh
// message arrives (PRD §5.9.7: status OK → resolve).
func (e *Engine) resolveOffline(ctx context.Context, t models.TelemetryMessage) {
	key := offlineDedupKey(t.CompanyCode, t.VehicleID)
	n, err := e.store.ResolveOpenAlerts(ctx, t.CompanyCode, key)
	if err != nil {
		slog.Warn("alert engine: offline resolve failed", "company", t.CompanyCode,
			"vehicle_id", t.VehicleID, "error", err)
		return
	}
	if n > 0 {
		alertsResolved.WithLabelValues(models.AlertOffline, t.CompanyCode).Add(float64(n))
		slog.Info("offline alert resolved", "company", t.CompanyCode, "vehicle_id", t.VehicleID)
	}
}
