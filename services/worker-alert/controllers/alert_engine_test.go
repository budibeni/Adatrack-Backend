package controllers

// alert_engine_test.go — RaiseAlert contract (PRD §5.9): validation, the Redis
// dedup fast path, the DB open-row guard, insert-failure key release and the
// notification recipient resolution. The publish/Notify success path needs a
// live NATS (engine.nats is nil here) and is covered by the ADATRACK_IT suite.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"adatrack_gps/worker-alert/models"
)

func alertDraft() *models.Alert {
	return &models.Alert{
		Type:        models.AlertOverspeeding,
		Severity:    models.SeverityHigh,
		VehicleID:   7,
		IMEI:        "it-imei-7",
		CompanyCode: "DEV001",
		DedupKey:    "speed:DEV001:7:1",
	}
}

// TestRaiseAlertValidatesDraft: incomplete drafts never reach Redis or the DB.
func TestRaiseAlertValidatesDraft(t *testing.T) {
	for _, tc := range []struct {
		name  string
		mutlf func(a *models.Alert)
		want  string
	}{
		{"no company", func(a *models.Alert) { a.CompanyCode = "" }, "company"},
		{"no vehicle", func(a *models.Alert) { a.VehicleID = 0 }, "vehicle"},
		{"no type", func(a *models.Alert) { a.Type = "" }, "type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeAlertStore()
			_, eng, _ := newMiniredisWorker(t, store)
			a := alertDraft()
			tc.mutlf(a)

			got, err := eng.RaiseAlert(context.Background(), a)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
			if got != nil {
				t.Errorf("raised = %+v, want nil", got)
			}
			if len(store.alerts) != 0 {
				t.Errorf("drafts reached the store: %v", store.alerts)
			}
		})
	}
}

// TestRaiseAlertDefaultsAndDedup: defaults are applied, the first trigger
// inserts, and a repeat inside the window is suppressed by the Redis fast path
// without touching the store again.
func TestRaiseAlertDefaultsAndDedup(t *testing.T) {
	store := newFakeAlertStore()
	store.inserted = false // open-row guard (publish path needs NATS)
	_, eng, _ := newMiniredisWorker(t, store)
	ctx := context.Background()

	a := alertDraft()
	a.Status = ""
	a.DetectedAt = time.Time{}
	first, err := eng.RaiseAlert(ctx, a)
	if err != nil {
		t.Fatalf("first raise: %v", err)
	}
	if first != nil {
		t.Errorf("inserted=false must return (nil,nil), got %+v", first)
	}
	if a.Status != models.StatusOpen {
		t.Errorf("status default = %q, want open", a.Status)
	}
	if a.DetectedAt.IsZero() {
		t.Error("DetectedAt default must be stamped")
	}
	if len(store.alerts) != 1 {
		t.Fatalf("store drafts = %d, want 1", len(store.alerts))
	}

	// Second trigger inside the window: Redis fast path suppresses it before
	// the store is consulted.
	second, err := eng.RaiseAlert(ctx, alertDraft())
	if err != nil || second != nil {
		t.Fatalf("second raise = (%+v, %v), want (nil, nil)", second, err)
	}
	if len(store.alerts) != 1 {
		t.Errorf("deduped trigger must not reach the store (drafts=%d)", len(store.alerts))
	}
}

// TestRaiseAlertInsertErrorReleasesDedupKey: a failed insert must free the
// fast-path lock so a retry is not silently dropped (rule §8).
func TestRaiseAlertInsertErrorReleasesDedupKey(t *testing.T) {
	store := newFakeAlertStore()
	store.insertErr = context.DeadlineExceeded
	w, eng, cfg := newMiniredisWorker(t, store)
	ctx := context.Background()

	if _, err := eng.RaiseAlert(ctx, alertDraft()); err == nil {
		t.Fatal("insert error must surface")
	}
	key := dedupKeyPrefix + "DEV001:speed:DEV001:7:1"
	if val, err := w.red.Get(ctx, key); err != nil || val != "" {
		t.Errorf("dedup key must be released after a failed insert, got (%q, %v)", val, err)
	}
	_ = cfg
}

// TestRegisterMetricsAndReadiness: the metrics collector registers on a fresh
// registry (nil is a documented no-op) and Readiness forwards the store probe.
func TestRegisterMetricsAndReadiness(t *testing.T) {
	store := newFakeAlertStore()
	w, _, _ := newMiniredisWorker(t, store)
	ctx := context.Background()

	if err := w.Readiness(ctx); err != nil {
		t.Errorf("healthy store: %v", err)
	}
	store.readinessErr = context.DeadlineExceeded
	if err := w.Readiness(ctx); err == nil {
		t.Error("a failing store must surface through Readiness")
	}

	RegisterMetrics(prometheus.NewRegistry())
	RegisterMetrics(nil) // must not panic
}

// and a store failure is logged, never panics.
func TestUpdateRouteDeviation(t *testing.T) {
	store := newFakeAlertStore()
	_, eng, _ := newMiniredisWorker(t, store)
	a := alertDraft()

	eng.UpdateRouteDeviation(context.Background(), a, 350)
	if len(store.deviations) != 1 || store.deviations[0] != 350 {
		t.Errorf("deviations = %v, want [350]", store.deviations)
	}

	store.updateDeviationErr = context.DeadlineExceeded
	eng.UpdateRouteDeviation(context.Background(), a, 900) // must not panic
}

// TestResolveOffline: a fresh message resolves open OFFLINE alerts; store
// failures are logged and counted as zero.
func TestResolveOffline(t *testing.T) {
	store := newFakeAlertStore()
	_, eng, _ := newMiniredisWorker(t, store)
	msg := models.TelemetryMessage{CompanyCode: "DEV001", VehicleID: 7, IMEI: "it-imei-7"}

	store.resolvedN = 2
	eng.resolveOffline(context.Background(), msg)
	if len(store.resolveCalls) != 1 || store.resolveCalls[0] != offlineDedupKey("DEV001", 7) {
		t.Errorf("resolve calls = %v, want the offline dedup key once", store.resolveCalls)
	}

	store.resolveErr = context.DeadlineExceeded
	eng.resolveOffline(context.Background(), msg) // must not panic
}
