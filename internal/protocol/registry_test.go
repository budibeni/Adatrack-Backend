package protocol

import "testing"

// TestCatalogueIsWellFormed locks the universal registry invariants: every code
// is lowercase/unique, every listener carries a port + env key, and Teltonika is
// the only protocol on its own reference (PRD Module 1c).
func TestCatalogueIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	teltonika := 0
	for _, p := range All() {
		if p.Code == "" || p.Code != Normalize(p.Code) {
			t.Errorf("protocol code %q is not canonical lowercase", p.Code)
		}
		if seen[p.Code] {
			t.Errorf("duplicate protocol code %q", p.Code)
		}
		seen[p.Code] = true
		if p.Name == "" || p.Brand == "" || p.Decoder == "" {
			t.Errorf("%s: name/brand/decoder must not be empty", p.Code)
		}
		if p.Transport != TCP && p.Transport != UDP {
			t.Errorf("%s: unsupported transport %q", p.Code, p.Transport)
		}
		if p.DefaultPort <= 0 || p.DefaultPort > 65535 {
			t.Errorf("%s: invalid default port %d", p.Code, p.DefaultPort)
		}
		if p.EnvKey == "" {
			t.Errorf("%s: listener protocol must document its env override key", p.Code)
		}
		if p.Reference != RefTraccar && p.Reference != RefOwn {
			t.Errorf("%s: invalid reference %q", p.Code, p.Reference)
		}
		if p.Reference == RefOwn {
			teltonika++
			if p.Code != "teltonika" {
				t.Errorf("only Teltonika may use the own reference, got %s", p.Code)
			}
		}
		switch p.Status {
		case Full, Partial, Framing:
		default:
			t.Errorf("%s: invalid status %q (never guess a protocol depth)", p.Code, p.Status)
		}
	}
	if teltonika != 1 {
		t.Errorf("expected exactly 1 own-reference protocol (Teltonika), got %d", teltonika)
	}
}

// TestCatalogueMirrorsIngestionListenerTable pins the registry to the PRD
// Module 1c port table so a listener change cannot silently drift the registry.
func TestCatalogueMirrorsIngestionListenerTable(t *testing.T) {
	want := map[string]struct {
		port int
		env  string
	}{
		"gt06":      {5001, "TCP_PORT"},
		"teltonika": {5027, "TELTONIKA_TCP_PORT"},
		"tk103":     {5013, "TK103_TCP_PORT"},
		"meiligao":  {5002, "MEILIGAO_TCP_PORT"},
		"xexun":     {5003, "XEXUN_TCP_PORT"},
		"suntech":   {5017, "SUNTECH_TCP_PORT"},
		"h02":       {5010, "H02_TCP_PORT"},
		"totem":     {5005, "TOTEM_TCP_PORT"},
		"gt02":      {5006, "GT02_TCP_PORT"},
		"navigil":   {5012, "NAVIGIL_TCP_PORT"},
		"castel":    {5019, "CASTEL_TCP_PORT"},
	}
	got := map[string]string{}
	for _, p := range All() {
		expect, ok := want[p.Code]
		if !ok {
			t.Errorf("unexpected protocol %q in registry", p.Code)
			continue
		}
		if p.DefaultPort != expect.port {
			t.Errorf("%s: port = %d, want %d", p.Code, p.DefaultPort, expect.port)
		}
		if p.EnvKey != expect.env {
			t.Errorf("%s: env key = %q, want %q", p.Code, p.EnvKey, expect.env)
		}
		got[p.Code] = p.EnvKey
	}
	for code := range want {
		if _, ok := got[code]; !ok {
			t.Errorf("protocol %q missing from the registry", code)
		}
	}
}

// TestLookupAndNormalize verifies case/whitespace tolerant resolution and the
// rejection of unknown brands.
func TestLookupAndNormalize(t *testing.T) {
	for _, raw := range []string{"gt06", "  GT06 ", "Gt06"} {
		p, ok := Lookup(raw)
		if !ok || p.Code != "gt06" {
			t.Errorf("Lookup(%q) = %+v, %v; want gt06", raw, p, ok)
		}
	}
	if _, ok := Lookup("unknown-brand"); ok {
		t.Error("unknown brand must not resolve")
	}
	if Supported("") || Supported("meitrack") {
		t.Error("empty/unknown protocol must not be reported supported")
	}
	if len(Codes()) != len(All()) {
		t.Error("Codes() and All() disagree on catalogue size")
	}
}

// TestByPortAndBrandHint covers the dashboard helpers.
func TestByPortAndBrandHint(t *testing.T) {
	byPort := ByPort(5001)
	if len(byPort) != 1 || byPort[0].Code != "gt06" {
		t.Errorf("ByPort(5001) = %+v, want [gt06]", byPort)
	}
	if got := ByPort(0); len(got) != 0 {
		t.Errorf("ByPort(0) must be empty, got %+v", got)
	}

	hint, ok := BrandHint("Concox GT06N")
	if !ok || hint.Code != "gt06" {
		t.Errorf("BrandHint(Concox GT06N) = %+v, %v; want gt06", hint, ok)
	}
	if _, ok := BrandHint(""); ok {
		t.Error("empty model must not produce a hint")
	}
}

// TestAllReturnsCopy proves callers cannot mutate the shared registry.
func TestAllReturnsCopy(t *testing.T) {
	first := All()
	first[0].Code = "tampered"
	if got := All()[0].Code; got == "tampered" {
		t.Error("All() must return a copy, not the backing slice")
	}
}
