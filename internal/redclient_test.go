package internal

import "testing"

// TestLiveStateKeyForLayout pins the ONE live-state key layout that both the
// writer (worker-live, FR-2.1) and the REST overlay reader (api-vehicle, phase
// B6) must agree on; a drift here would silently blank every fuel/ACC overlay.
func TestLiveStateKeyForLayout(t *testing.T) {
	got := LiveStateKeyFor("adatrack_gps:", "DEV001", "864201040512345")
	want := "adatrack_gps:dev001:vehicle:state:864201040512345"
	if got != want {
		t.Errorf("LiveStateKeyFor = %q, want %q", got, want)
	}
}

// TestLiveStateKeyForNormalisation: a casing/whitespace difference in the
// company code must never produce a different key than the writer's (tenant
// isolation, FR-2.1).
func TestLiveStateKeyForNormalisation(t *testing.T) {
	canonical := LiveStateKeyFor("adatrack_gps:", "dev001", "111")
	cases := map[string]string{
		"upper":         "DEV001",
		"mixed":         "Dev001",
		"padded":        " DEV001 ",
		"tabbed":        "DEV\t001",
		"empty→default": "",
	}
	for name, code := range cases {
		if name == "empty→default" {
			if got := LiveStateKeyFor("adatrack_gps:", code, "111"); got != "adatrack_gps:default:vehicle:state:111" {
				t.Errorf("empty company code = %q, want the default namespace", got)
			}
			continue
		}
		if got := LiveStateKeyFor("adatrack_gps:", code, "111"); got != canonical {
			t.Errorf("%s: key = %q, want %q (must match the writer)", name, got, canonical)
		}
	}
}

// TestLiveStateKeyForCustomPrefix: an explicit REDIS_KEY_PREFIX is honoured, and
// an empty prefix falls back to the canonical namespace instead of producing an
// unreadable "dev001:vehicle:state:..." key.
func TestLiveStateKeyForCustomPrefix(t *testing.T) {
	if got := LiveStateKeyFor("adatrack:prod:", "DEV001", "9"); got != "adatrack:prod:dev001:vehicle:state:9" {
		t.Errorf("custom prefix key = %q", got)
	}
	if got := LiveStateKeyFor("", "DEV001", "9"); got != "adatrack_gps:dev001:vehicle:state:9" {
		t.Errorf("empty prefix key = %q, want the adatrack_gps: fallback", got)
	}
}
