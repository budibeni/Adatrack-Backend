package controllers

// alert_harness_test.go — hermetic wiring for the worker-alert suites: a
// miniredis-backed internal.RedisClient (no live Redis) plus a Worker/Engine
// built on the fake store.
//
// The NATS client is NOT constructible without a live server, so the hermetic
// suites keep nats=nil and rely on InsertAlert returning inserted=false (the
// open-alert dedup guard) — RaiseAlert then returns before publish/Notify. The
// live publish/notify paths are covered by store_pg_it_test.go (ADATRACK_IT=1).

import (
	"testing"
	"time"

	"adatrack_gps/internal"

	"github.com/alicebob/miniredis/v2"

	"adatrack_gps/worker-alert/models"
)

// testAlertConfig builds a small, fully in-memory engine configuration.
func testAlertConfig(redAddr string) *internal.Config {
	cfg := &internal.Config{}
	host, port := splitAddr(redAddr)
	cfg.Redis.Host = host
	cfg.Redis.Port = port
	cfg.Redis.KeyPrefix = "adatrack_test:"
	cfg.Redis.TTL = time.Minute
	cfg.NATS.SubjectPrefix = "telemetry"
	cfg.NATS.MaxPendingPercent = 50
	cfg.NATS.JetStreamMaxBytes = 1 << 20
	cfg.Alert.DedupWindow = time.Minute
	cfg.Alert.OfflineAfterMinutes = 3
	cfg.Alert.BatteryLowPercent = 20
	cfg.Alert.RouteDeviationThresholdM = 200
	cfg.Alert.SOSCooldownSeconds = 60
	cfg.Alert.SOSEscalationMinutes = 10
	cfg.Alert.SOSEscalationMax = 3
	cfg.Alert.NotifyRateLimitPerMin = 0
	cfg.Fuel.Severity = models.SeverityHigh
	return cfg
}

// splitAddr splits "host:port".
func splitAddr(addr string) (string, string) {
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			return addr[:i], addr[i+1:]
		}
	}
	return addr, "6379"
}

// newMiniredisWorker wires a Worker + Engine on miniredis + the fake store and
// registers cleanup.
func newMiniredisWorker(t *testing.T, store *fakeAlertStore) (*Worker, *Engine, *internal.Config) {
	t.Helper()
	mr := miniredis.RunT(t)
	cfg := testAlertConfig(mr.Addr())
	red, err := internal.NewRedisClient(cfg)
	if err != nil {
		t.Fatalf("miniredis client: %v", err)
	}
	t.Cleanup(func() { _ = red.Close() })
	w := New(cfg, red, nil, store)
	return w, w.engine, cfg
}
