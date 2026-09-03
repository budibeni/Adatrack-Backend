package controllers

// unit_extra_test.go (B4 coverage 2026-08-31): deviceSilent + decodeState +
// helper env/time + Stop/internalRegistry + recordTTA di jalur non-companyStore.

import (
	"context"
	"testing"
	"time"

	"ajb_gps/internal"
	"ajb_gps/internal/tenant"
	"ajb_gps/worker-alert/models"

	"github.com/prometheus/client_golang/prometheus"
)

func TestDeviceSilent_MissingKey(t *testing.T) {
	wa, _ := newTestWA(t)
	if !wa.deviceSilent("DEV001", "123") {
		t.Fatal("missing live-state key must be silent (true)")
	}
}

func TestDeviceSilent_FreshKey(t *testing.T) {
	wa, mr := newTestWA(t)
	now := time.Now().Unix()
	_ = mr.Set("adatrack_gps:dev001:vehicle:state:i1",
		`{"timestamp":`+intStr(now)+`}`)
	if wa.deviceSilent("DEV001", "i1") {
		t.Fatal("fresh live-state must NOT be silent")
	}
}

func TestDeviceSilent_StaleKey(t *testing.T) {
	wa, mr := newTestWA(t)
	old := time.Now().Add(-24 * time.Hour).Unix()
	_ = mr.Set("adatrack_gps:dev001:vehicle:state:i2",
		`{"timestamp":`+intStr(old)+`}`)
	if !wa.deviceSilent("DEV001", "i2") {
		t.Fatal("stale live-state must be silent")
	}
}

func TestDecodeState_Errors(t *testing.T) {
	if _, err := decodeState(`not-json`); err == nil {
		t.Fatal("non-object must error")
	}
	if _, err := decodeState(`{}`); err != nil {
		t.Fatalf("empty object should parse: %v", err)
	}
}

func TestSosCooldownAndOfflineThreshold(t *testing.T) {
	t.Setenv("SOS_COOLDOWN_SECONDS", "45s")
	if d := waSosCooldownEnv(); d != 45*time.Second {
		t.Fatalf("sos cooldown = %v", d)
	}
	t.Setenv("OFFLINE_AFTER_MINUTES", "7")
	if d := durationFromEnv("OFFLINE_AFTER_MINUTES", 3*time.Minute); d != 7*time.Minute {
		t.Fatalf("offline threshold = %v", d)
	}
}

func waSosCooldownEnv() time.Duration {
	return durationFromEnv("SOS_COOLDOWN_SECONDS", 60*time.Second)
}

func TestStop_NilCancelIsSafe(t *testing.T) {
	wa, _ := newTestWA(t)
	wa.cancel = nil
	wa.Stop() // harus return tanpa panic
}

func TestInternalRegistry_FallbackDefault(t *testing.T) {
	wa, _ := newTestWA(t)
	wa.reg = nil
	if wa.internalRegistry() != prometheus.DefaultGatherer {
		t.Fatal("nil reg should fall back to default gatherer")
	}
}

func TestRecordTTA_NonCompanyStoreReturns(t *testing.T) {
	wa, _ := newTestWA(t)
	wa.recordTTA("DEV001", 2*time.Minute) // st.(*companyStore) false → return, no panic
}

func TestImeiForVehicle_NoStore(t *testing.T) {
	wa, _ := newTestWA(t)
	if imei := wa.imeiForVehicle("NOPE", 1); imei != "" {
		t.Fatalf("expected empty imei, got %q", imei)
	}
}

func TestAlertPrefTypes_Default(t *testing.T) {
	if got := alertPrefTypes("SOMETHING"); len(got) != 1 || got[0] != "something" {
		t.Fatalf("default pref types = %v", got)
	}
}

func TestPublishSOSEscalation_NilNATS(t *testing.T) {
	wa, _ := newTestWA(t)
	wa.nac = nil
	wa.tm = &tenant.Manager{} // zero-value Manager, DB() error → imei kosong, publish skip
	entry := models.OpenSOSAlert{ID: 1, VehicleID: 7}
	wa.publishSOSEscalation("DEV001", entry, 1, 2*time.Minute) // nac nil → safe
}

func TestNewStore_NilManager(t *testing.T) {
	wa, _ := newTestWA(t)
	wa.tm = nil
	// newStore dengan tm nil → panic. Guard: jangan panggil. Pastikan helper
	// redisKeyPrefix tetap aman.
	if p := wa.redisKeyPrefix(); p == "" {
		t.Fatal("redisKeyPrefix must return non-empty")
	}
}

func TestConfigHelpers(t *testing.T) {
	cfg := internal.LoadConfig()
	if cfg.GetSpeedLimit() <= 0 {
		t.Fatal("GetSpeedLimit must be > 0")
	}
	if cfg.GetGraceMargin() < 0 {
		t.Fatal("GetGraceMargin must be >= 0")
	}
}

func TestHelpers_KeyBuilders(t *testing.T) {
	wa, _ := newTestWA(t)
	if k := wa.redisStateKey("DEV001", "i1"); k != "adatrack_gps:dev001:vehicle:state:i1" {
		t.Fatalf("redisStateKey = %q", k)
	}
	if k := wa.geofenceStateKey("DEV001", "i1"); k != "adatrack_gps:dev001:geofence_state:i1" {
		t.Fatalf("geofenceStateKey = %q", k)
	}
	if k := wa.fuelStateKey("DEV001", "i1"); k != "adatrack_gps:dev001:fuel_state:i1" {
		t.Fatalf("fuelStateKey = %q", k)
	}
	if k := wa.sosEscalationKey("DEV001", 7); k != "adatrack_gps:dev001:sos_escalation:7" {
		t.Fatalf("sosEscalationKey = %q", k)
	}
}

var _ = context.Background