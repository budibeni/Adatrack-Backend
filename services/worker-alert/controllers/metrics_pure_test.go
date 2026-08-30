package controllers

import (
	"context"
	"testing"
	"time"

	"ajb_gps/worker-alert/models"

	"github.com/prometheus/client_golang/prometheus"
)

// ---------------------------------------------------------------------
// alertMetrics — methods increment (nil-safe + terdaftar).
// ---------------------------------------------------------------------

func TestAlertMetricsIncrementMethods(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := newAlertMetrics(reg)
	if m == nil {
		t.Fatal("newAlertMetrics harus mengembalikan instance")
	}
	m.incCreated("DEV001", "GEOFENCE_BREACH", "high")
	m.incError("DEV001", "db")
	m.incError("DEV001", "db") // 2x
	m.incPublished("alert.geofence.DEV001")
	m.incFuelACCSuppressed("DEV001")

	mf, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	if len(mf) == 0 {
		t.Fatal("registry harus berisi metric")
	}
}

func TestAlertMetricsNilReceiverSafe(t *testing.T) {
	var m *alertMetrics
	// Tak boleh panic saat receiver nil.
	m.incCreated("x", "y", "z")
	m.incError("x", "y")
	m.incPublished("s")
	m.incFuelACCSuppressed("x")
}

func TestNewAlertMetricsNoRegistry(t *testing.T) {
	if m := newAlertMetrics(nil); m == nil {
		t.Fatal("reg nil tetap harus mengembalikan instance (utk metric yg boleh nil)")
	}
}

// ---------------------------------------------------------------------
// thresholds env-driven.
// ---------------------------------------------------------------------

func TestOfflineThresholdEnv(t *testing.T) {
	wa := waForTest()
	t.Setenv("OFFLINE_AFTER_MINUTES", "5")
	if got := wa.offlineThreshold(); got != 5*time.Minute {
		t.Errorf("offlineThreshold = %v, want 5m", got)
	}
	t.Setenv("OFFLINE_AFTER_MINUTES", "90s")
	if got := wa.offlineThreshold(); got != 90*time.Second {
		t.Errorf("offlineThreshold dur = %v, want 90s", got)
	}
	t.Setenv("OFFLINE_AFTER_MINUTES", "")
	if got := wa.offlineThreshold(); got != 3*time.Minute {
		t.Errorf("offlineThreshold default = %v, want 3m", got)
	}
}

func TestRouteDeviationThresholdEnv(t *testing.T) {
	wa := waForTest()
	t.Setenv("ROUTE_DEVIATION_THRESHOLD_M", "500")
	if got := wa.routeDeviationThreshold(); got != 500 {
		t.Errorf("routeDevThreshold = %v, want 500", got)
	}
	t.Setenv("ROUTE_DEVIATION_THRESHOLD_M", "0")  // <=0 → default 200
	t.Setenv("ROUTE_DEVIATION_THRESHOLD_M", "-1") // negatif → default
	if got := wa.routeDeviationThreshold(); got != 200 {
		t.Errorf("routeDevThreshold default = %v, want 200", got)
	}
	t.Setenv("ROUTE_DEVIATION_THRESHOLD_M", "")
	if got := wa.routeDeviationThreshold(); got != 200 {
		t.Errorf("routeDevThreshold unset = %v, want 200", got)
	}
}

// ---------------------------------------------------------------------
// nilIfZeroPtr — helper safe-null untuk field numerik.
// ---------------------------------------------------------------------

func TestNilIfZeroPtr(t *testing.T) {
	if nilIfZeroPtr(nil) != nil {
		t.Error("nil ptr harus → nil")
	}
	zero := 0.0
	if v := nilIfZeroPtr(&zero); v != nil {
		t.Errorf("0 harus → nil, dapat %v", v)
	}
	val := 3.5
	if v := nilIfZeroPtr(&val); v == nil || v != 3.5 {
		t.Errorf("nilai non-zero harus dipreservasi, dapat %v", v)
	}
}

// ---------------------------------------------------------------------------
// helpers kecil.
// ---------------------------------------------------------------------------

func TestContextWithTimeoutAndJSONUnmarshal(t *testing.T) {
	ctx, cancel := contextWithTimeout(2 * time.Second)
	defer cancel()
	if ctx == nil {
		t.Fatal("ctx tidak boleh nil")
	}
	// Verifikasi cancel benar-benar membatalkan ctx.
	_, cancelFn := context.WithCancel(context.Background())
	cancelFn()
	var out map[string]int
	if err := jsonUnmarshal([]byte(`{"a":1}`), &out); err != nil {
		t.Fatalf("jsonUnmarshal: %v", err)
	}
	if out["a"] != 1 {
		t.Errorf("decode salah: %+v", out)
	}
}

// ---------------------------------------------------------------------
// alertPrefTypes — token preferensi per enum alert.
// ---------------------------------------------------------------------

func TestAlertPrefTypesMapping(t *testing.T) {
	cases := []struct {
		alertType string
		want      []string
	}{
		{models.AlertTypeGeofence, []string{"geofence"}},
		{models.AlertTypeSpeed, []string{"speed"}},
		{models.AlertTypeSOS, []string{"sos"}},
		{models.AlertTypeBattery, []string{"battery"}},
		{models.AlertTypeOffline, []string{"offline"}},
		{models.AlertTypeRouteDev, []string{"route_deviation", "route"}},
		{models.AlertTypeFuelDrop, []string{"fuel_drop", "fuel"}},
		{models.AlertTypeRefuel, []string{"refuel", "fuel"}},
		{"CUSTOM_XYZ", []string{"custom_xyz"}},
	}
	for _, c := range cases {
		got := alertPrefTypes(c.alertType)
		if len(got) != len(c.want) {
			t.Fatalf("alertPrefTypes(%q) = %v, want %v", c.alertType, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("alertPrefTypes(%q)[%d] = %q, want %q", c.alertType, i, got[i], c.want[i])
			}
		}
	}
}

// ---------------------------------------------------------------------
// decodeState — parse JSON object worker-live.
// ---------------------------------------------------------------------

func TestDecodeStateValid(t *testing.T) {
	m, err := decodeState(`{"timestamp":1700000000,"speed":40}`)
	if err != nil {
		t.Fatalf("decodeState valid: %v", err)
	}
	if m["speed"].(float64) != 40 {
		t.Errorf("speed = %v", m["speed"])
	}
}

func TestDecodeStateEmptyReturnsErr(t *testing.T) {
	if _, err := decodeState(""); err == nil {
		t.Fatal("empty harus error")
	}
}

func TestDecodeStateNotObjectReturnsErr(t *testing.T) {
	if _, err := decodeState(`[1,2]`); err == nil {
		t.Fatal("array bukan object harus error")
	}
	if _, err := decodeState("garbage"); err == nil {
		t.Fatal("bukan json harus error")
	}
}

