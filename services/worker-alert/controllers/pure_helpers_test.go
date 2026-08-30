package controllers

import (
	"testing"
	"time"

	"ajb_gps/worker-alert/models"
)

func waForTest() *WorkerAlert { return &WorkerAlert{} }

// ---------------------------------------------------------------------
// Key builders (PRD §7 / worker-live alignment)
// ---------------------------------------------------------------------

func TestRedisKeyPrefixDefault(t *testing.T) {
	t.Setenv("REDIS_KEY_PREFIX", "")
	if got := waForTest().redisKeyPrefix(); got != "adatrack_gps:" {
		t.Errorf("prefix = %q", got)
	}
}

func TestRedisKeyPrefixCustom(t *testing.T) {
	t.Setenv("REDIS_KEY_PREFIX", "px:")
	if got := waForTest().redisKeyPrefix(); got != "px:" {
		t.Errorf("prefix = %q", got)
	}
}

func TestRedisStateKeyLowercasesAndDefaults(t *testing.T) {
	t.Setenv("REDIS_KEY_PREFIX", "")
	wa := waForTest()
	cases := []struct{ company, imei, want string }{
		{"DEV001", "1", "adatrack_gps:dev001:vehicle:state:1"},
		{" ABLE01 ", "2", "adatrack_gps:able01:vehicle:state:2"},
		{"", "3", "adatrack_gps:default:vehicle:state:3"},
	}
	for _, c := range cases {
		if got := wa.redisStateKey(c.company, c.imei); got != c.want {
			t.Errorf("stateKey(%q,%q) = %q, want %q", c.company, c.imei, got, c.want)
		}
	}
}

func TestGeofenceFuelEscalationKeys(t *testing.T) {
	t.Setenv("REDIS_KEY_PREFIX", "")
	wa := waForTest()
	if got := wa.geofenceStateKey("DEV001", "9"); got != "adatrack_gps:dev001:geofence_state:9" {
		t.Errorf("geofence key = %q", got)
	}
	if got := wa.fuelStateKey("DEV001", "9"); got != "adatrack_gps:dev001:fuel_state:9" {
		t.Errorf("fuel state key = %q", got)
	}
	if got := wa.sosEscalationKey("DEV001", 42); got != "adatrack_gps:dev001:sos_escalation:42" {
		t.Errorf("sos escalation key = %q", got)
	}
}

func TestUintToString(t *testing.T) {
	cases := map[uint64]string{0: "0", 1: "1", 42: "42", 123456789012345: "123456789012345"}
	for in, want := range cases {
		if got := uintToString(in); got != want {
			t.Errorf("uintToString(%d) = %q, want %q", in, got, want)
		}
	}
}

// ---------------------------------------------------------------------
// Env number helpers
// ---------------------------------------------------------------------

func TestEnvFloatDefaultAndParse(t *testing.T) {
	if got := envFloat("XA_FLOAT_UNSET", 7.5); got != 7.5 {
		t.Errorf("unset = %v, want 7.5", got)
	}
	t.Setenv("XA_FLOAT_SET", "3.25")
	if got := envFloat("XA_FLOAT_SET", 1); got != 3.25 {
		t.Errorf("set = %v, want 3.25", got)
	}
	t.Setenv("XA_FLOAT_BAD", "abc")
	if got := envFloat("XA_FLOAT_BAD", 2.2); got != 2.2 {
		t.Errorf("bad = %v, want default 2.2", got)
	}
}

func TestDurationFromEnvVariants(t *testing.T) {
	t.Setenv("XA_D_U", "")
	if got := durationFromEnv("XA_D_U", 2*time.Minute); got != 2*time.Minute {
		t.Errorf("unset = %v", got)
	}
	t.Setenv("XA_D_DUR", "3m")
	if got := durationFromEnv("XA_D_DUR", time.Minute); got != 3*time.Minute {
		t.Errorf("dur = %v", got)
	}
	t.Setenv("XA_D_INT", "4")
	if got := durationFromEnv("XA_D_INT", time.Minute); got != 4*time.Minute {
		t.Errorf("int = %v", got)
	}
	t.Setenv("XA_D_NEG", "-5")
	if got := durationFromEnv("XA_D_NEG", time.Minute); got != time.Minute {
		t.Errorf("neg = %v, fallback default", got)
	}
}

// ---------------------------------------------------------------------
// Small pure helpers
// ---------------------------------------------------------------------

func TestBreachVerbAndBoolToInt(t *testing.T) {
	if breachVerb(true) != "entered" || breachVerb(false) != "exited" {
		t.Error("breachVerb salah")
	}
	if boolToInt(true) != "1" || boolToInt(false) != "0" {
		t.Error("boolToInt salah")
	}
}

func TestVolumeOf(t *testing.T) {
	if got := volumeOf(models.TelemetryMessage{}); got != 0 {
		t.Errorf("nil volume = %v, want 0", got)
	}
	v := 88.5
	if got := volumeOf(models.TelemetryMessage{FuelVolume: &v}); got != 88.5 {
		t.Errorf("volume = %v, want 88.5", got)
	}
}

func TestAlertTypeSubjectMapping(t *testing.T) {
	cases := map[string]string{
		models.AlertTypeGeofence: "geofence",
		models.AlertTypeSpeed:    "speed",
		models.AlertTypeSOS:      "sos",
		models.AlertTypeBattery:  "battery",
		models.AlertTypeOffline:  "offline",
		models.AlertTypeRouteDev: "route_deviation",
		models.AlertTypeFuelDrop: "fuel",
		models.AlertTypeRefuel:   "fuel",
		"OTHER_CUSTOM":           "other_custom",
	}
	for in, want := range cases {
		if got := alertTypeSubject(in); got != want {
			t.Errorf("alertTypeSubject(%q) = %q, want %q", in, got, want)
		}
	}
}