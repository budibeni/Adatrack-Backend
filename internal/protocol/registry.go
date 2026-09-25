// Package protocol is the UNIVERSAL protocol/brand registry of ADATRACK
// (PRD Module 1c — "platform menerima SEMUA merek/model GPS tracker tanpa
// terkecuali"). It is the single source of truth shared by:
//
//   - the ingestion listeners (default Traccar ports + env override key), and
//   - api-vehicle device registration, which only accepts a protocol code this
//     registry knows (B11 task "Dukungan protokol universal").
//
// Teltonika is deliberately part of the registry but flagged `reference=own`:
// it is the only protocol documented from its own reference (PRD Module 1c)
// instead of the Traccar catalogue.
//
// Ports follow the Traccar convention (PRD Module 1c table). A port of 0 means
// the listener has no documented default yet and must be enabled via env.
package protocol

import "strings"

// Transport is the wire transport of a listener.
type Transport string

const (
	TCP Transport = "tcp"
	UDP Transport = "udp"
)

// Reference names the document family the decoder was validated against.
type Reference string

const (
	// RefTraccar is the public Traccar protocol catalogue (200+ protocols).
	RefTraccar Reference = "traccar"
	// RefOwn is a vendor/own reference (Teltonika only, PRD Module 1c).
	RefOwn Reference = "own"
)

// Status is the honest implementation depth of a protocol (never guessed).
type Status string

const (
	// Full: position + identity ingested end-to-end (E2E proven).
	Full Status = "full"
	// Partial: position ingested, some vendor frames still open.
	Partial Status = "partial"
	// Framing: framing/identity only, payload decoding still open.
	Framing Status = "framing"
)

// Protocol describes one supported device protocol/brand family.
type Protocol struct {
	// Code is the stable lowercase lookup key stored on the vehicle row.
	Code string
	// Name is the human label shown in the dashboard.
	Name string
	// Brand is the primary device vendor family.
	Brand string
	// Transport is tcp|udp.
	Transport Transport
	// DefaultPort is the Traccar-convention port (0 = no documented default).
	DefaultPort int
	// EnvKey is the environment variable overriding DefaultPort on the
	// ingestion tier ("" for protocols without their own listener).
	EnvKey string
	// Decoder is the ingestion decoder family key.
	Decoder string
	// Reference is the document family the decoder follows.
	Reference Reference
	// Status is the honest implementation depth.
	Status Status
}

// catalogue is the immutable registry. It mirrors the listener table of
// `services/ingestion-tcp/controllers/proto_register.go` plus the GT06 and
// Teltonika listeners that shipped in B0/B5a.
var catalogue = []Protocol{
	{Code: "gt06", Name: "GT06 / Concox", Brand: "Concox", Transport: TCP, DefaultPort: 5001, EnvKey: "TCP_PORT", Decoder: "gt06", Reference: RefTraccar, Status: Full},
	{Code: "teltonika", Name: "Teltonika Codec 8 / 8E", Brand: "Teltonika", Transport: TCP, DefaultPort: 5027, EnvKey: "TELTONIKA_TCP_PORT", Decoder: "teltonika", Reference: RefOwn, Status: Full},
	{Code: "tk103", Name: "TK103 (GT-clone)", Brand: "TK103", Transport: TCP, DefaultPort: 5013, EnvKey: "TK103_TCP_PORT", Decoder: "tk103", Reference: RefTraccar, Status: Partial},
	{Code: "meiligao", Name: "Meiligao GT30i/GT60/VT300", Brand: "Meiligao", Transport: TCP, DefaultPort: 5002, EnvKey: "MEILIGAO_TCP_PORT", Decoder: "meiligao", Reference: RefTraccar, Status: Partial},
	{Code: "xexun", Name: "Xexun GPS103/GPS303", Brand: "Xexun", Transport: TCP, DefaultPort: 5003, EnvKey: "XEXUN_TCP_PORT", Decoder: "xexun", Reference: RefTraccar, Status: Full},
	{Code: "suntech", Name: "Suntech ST215/ST240/ST340", Brand: "Suntech", Transport: TCP, DefaultPort: 5017, EnvKey: "SUNTECH_TCP_PORT", Decoder: "suntech", Reference: RefTraccar, Status: Partial},
	{Code: "h02", Name: "H02 / H08", Brand: "H02", Transport: TCP, DefaultPort: 5010, EnvKey: "H02_TCP_PORT", Decoder: "h02", Reference: RefTraccar, Status: Partial},
	{Code: "totem", Name: "Totem", Brand: "Totem", Transport: TCP, DefaultPort: 5005, EnvKey: "TOTEM_TCP_PORT", Decoder: "totem", Reference: RefTraccar, Status: Partial},
	{Code: "gt02", Name: "GT02", Brand: "GT02", Transport: TCP, DefaultPort: 5006, EnvKey: "GT02_TCP_PORT", Decoder: "gt02", Reference: RefTraccar, Status: Full},
	{Code: "navigil", Name: "Navigil", Brand: "Navigil", Transport: TCP, DefaultPort: 5012, EnvKey: "NAVIGIL_TCP_PORT", Decoder: "navigil", Reference: RefTraccar, Status: Partial},
	{Code: "castel", Name: "Castel", Brand: "Castel", Transport: TCP, DefaultPort: 5019, EnvKey: "CASTEL_TCP_PORT", Decoder: "castel", Reference: RefTraccar, Status: Framing},
}

// All returns a copy of the catalogue (callers can never mutate the registry).
func All() []Protocol {
	out := make([]Protocol, len(catalogue))
	copy(out, catalogue)
	return out
}

// Normalize canonicalises a user supplied protocol code (case + surrounding
// whitespace); protocol codes are stored lowercase.
func Normalize(code string) string {
	return strings.ToLower(strings.TrimSpace(code))
}

// Lookup resolves a protocol code. Unknown brands are rejected by api-vehicle so
// a device can never be registered on a listener that does not exist.
func Lookup(code string) (Protocol, bool) {
	needle := Normalize(code)
	for _, p := range catalogue {
		if p.Code == needle {
			return p, true
		}
	}
	return Protocol{}, false
}

// Supported reports whether a code is part of the universal registry.
func Supported(code string) bool {
	_, ok := Lookup(code)
	return ok
}

// Codes returns every registered protocol code (stable catalogue order).
func Codes() []string {
	out := make([]string, 0, len(catalogue))
	for _, p := range catalogue {
		out = append(out, p.Code)
	}
	return out
}

// ByPort returns every protocol whose default port matches (a default port may
// be shared across brands, e.g. the Concox family).
func ByPort(port int) []Protocol {
	var out []Protocol
	for _, p := range catalogue {
		if p.DefaultPort == port && port != 0 {
			out = append(out, p)
		}
	}
	return out
}

// BrandHint is a small heuristic that pre-selects a protocol from a free-text
// device model, used ONLY to help the dashboard pick a default. The
// authoritative decision always stays the explicit, validated protocol code,
// because a wrong guess could route a device to the wrong decoder.
func BrandHint(deviceModel string) (Protocol, bool) {
	needle := strings.ToLower(strings.TrimSpace(deviceModel))
	if needle == "" {
		return Protocol{}, false
	}
	for _, p := range catalogue {
		if strings.Contains(needle, p.Code) {
			return p, true
		}
		if p.Brand != "" && strings.Contains(needle, strings.ToLower(p.Brand)) {
			return p, true
		}
	}
	return Protocol{}, false
}
