package controllers

import "testing"

// TestFuelDropDecision memverifikasi matriks policy gate ACC FUEL_DROP
// (hybrid FR-7.6): default fail-open semua kondisi; strict hanya menekan
// ACC fresh-OFF; unknown/stale SELALU fail-open (jangan sembunyikan pencurian
// karena data hilang).
func TestFuelDropDecision(t *testing.T) {
	on, off := true, false
	cases := []struct {
		name        string
		require     bool
		acc         *bool
		stale       bool
		wantAllowed bool
		wantCtx     string
	}{
		{"default_nil_acc_unknown_fail_open", false, nil, false, true, "unknown"},
		{"default_on", false, &on, false, true, "on"},
		{"default_off_still_alerts_parked_theft_visible", false, &off, false, true, "off"},
		{"default_stale_off", false, &off, true, true, "stale-off"},
		{"default_stale_on", false, &on, true, true, "stale-on"},
		{"strict_nil_fails_open", true, nil, false, true, "unknown"},
		{"strict_on_allowed", true, &on, false, true, "on"},
		{"strict_fresh_off_suppressed", true, &off, false, false, "off"},
		{"strict_stale_off_fails_open", true, &off, true, true, "stale-off"},
		{"strict_stale_on", true, &on, true, true, "stale-on"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			gotAllowed, gotCtx := fuelDropDecision(tc.require, tc.acc, tc.stale)
			if gotAllowed != tc.wantAllowed || gotCtx != tc.wantCtx {
				t.Fatalf("fuelDropDecision(require=%v, acc=%v, stale=%v) = (%v, %q); want (%v, %q)",
					tc.require, tc.acc, tc.stale, gotAllowed, gotCtx, tc.wantAllowed, tc.wantCtx)
			}
		})
	}
}

func TestBoolWord(t *testing.T) {
	if boolWord(true) != "on" || boolWord(false) != "off" {
		t.Fatalf("boolWord mismatch: %q / %q", boolWord(true), boolWord(false))
	}
}
