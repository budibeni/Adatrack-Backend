package controllers

// proto_register.go — B9: protocol-expansion decoders registered into the
// pluggable registry (PRD Module 1c port table).
//
//	| Protocol | Port (Traccar) | Env var              | Status                          |
//	|----------|----------------|----------------------|---------------------------------|
//	| TK103    | 5013           | TK103_TCP_PORT       | position subset (validated)      |
//	| Meiligao | 5002           | MEILIGAO_TCP_PORT    | login/heartbeat/position/alarm   |
//	| Xexun    | 5003           | XEXUN_TCP_PORT       | basic + full NMEA                |
//	| Suntech  | 5017           | SUNTECH_TCP_PORT     | framing+identity (payload gap)   |
//	| H02      | 5010           | H02_TCP_PORT         | text V0/HTBT/V3 (binary gap)     |
//	| Totem    | 5005           | TOTEM_TCP_PORT       | PATTERN_1 (pipe form gap)        |
//	| GT02     | 5006           | GT02_TCP_PORT        | position + heartbeat             |
//	| Navigil  | 5012           | NAVIGIL_TCP_PORT     | framing+ACK (payload/id gap)     |
//	| Castel   | 5019           | CASTEL_TCP_PORT      | framing+identity (GPS scale gap) |
//
// Every listener is disabled until its env var is set (empty or "0"), and the boot
// guard in main.go refuses two protocols sharing a port.
func init() {
	RegisterDecoder(tk103Decoder{})
	RegisterDecoder(meiligaoDecoder{})
	RegisterDecoder(xexunDecoder{})
	RegisterDecoder(suntechDecoder{})
	RegisterDecoder(h02Decoder{})
	RegisterDecoder(totemDecoder{})
	RegisterDecoder(gt02Decoder{})
	RegisterDecoder(navigilDecoder{})
	RegisterDecoder(castelDecoder{})
}
