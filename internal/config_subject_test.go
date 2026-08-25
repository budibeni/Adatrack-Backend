package internal

import "testing"

func TestConfigSubjectDefaultPrefix(t *testing.T) {
	c := &Config{}
	// Default prefix is "telemetry" (set by LoadConfig). Simulate it directly
	// for the builder semantics:
	c.NATS.SubjectPrefix = "telemetry"

	cases := []struct {
		name string
		got  string
		want string
	}{
		{"raw-imei", c.Subject("raw", "864201040512345"), "telemetry.raw.864201040512345"},
		{"raw-wildcard", c.Subject("raw", ">"), "telemetry.raw.>"},
		{"live-imei", c.Subject("live", "864201040512345"), "telemetry.live.864201040512345"},
		{"error-imei", c.Subject("error", "864201040512345"), "telemetry.error.864201040512345"},
		{"alert-plain", c.SubjectPlain("alert", "geofence", "*"), "alert.geofence.*"},
		{"notify-plain", c.SubjectPlain("notify", "alert", "12"), "notify.alert.12"},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, tc.got, tc.want)
		}
	}
}

func TestConfigSubjectEmptyPrefix(t *testing.T) {
	c := &Config{}
	c.NATS.SubjectPrefix = "" // env override: no prefix
	if got := c.Subject("raw", "123"); got != "raw.123" {
		t.Errorf("empty prefix: got %q want %q", got, "raw.123")
	}
	// empty parts → empty subject (callers guard against this)
	if got := c.Subject(); got != "" {
		t.Errorf("empty parts: got %q want empty", got)
	}
}

func TestConfigSubjectCustomPrefix(t *testing.T) {
	c := &Config{}
	c.NATS.SubjectPrefix = "prod"
	if got := c.Subject("raw", "123"); got != "prod.raw.123" {
		t.Errorf("custom prefix: got %q want %q", got, "prod.raw.123")
	}
}

func TestNATSClientSubject(t *testing.T) {
	c := &Config{}
	c.NATS.SubjectPrefix = "telemetry"
	client := &NATSClient{config: c}
	if got := client.Subject("raw", "123"); got != "telemetry.raw.123" {
		t.Errorf("NATSClient.Subject: got %q", got)
	}
	if got := client.SubjectPlain("alert", "sos", "x"); got != "alert.sos.x" {
		t.Errorf("NATSClient.SubjectPlain: got %q", got)
	}
	// nil config → plain join fallback
	nilCfg := &NATSClient{}
	if got := nilCfg.Subject("raw", "123"); got != "raw.123" {
		t.Errorf("NATSClient.Subject nil config: got %q", got)
	}
}
