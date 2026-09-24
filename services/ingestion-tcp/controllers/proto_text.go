package controllers

// proto_text.go — shared scaffolding for the B9 text/NMEA protocols (Xexun,
// Meiligao, TK103, Totem pattern 1) plus the checksum-strictness switch.
//
// The reference for every field layout is the in-repo Traccar summary
// (docs/docs-device/traccar-reference/) cross-checked against the upstream
// Traccar decoders themselves. Where a family's payload detail is NOT covered by
// either, the decoder reports the gap instead of guessing (no invented numbers
// ever reach the telemetry pipeline).

import (
	"bufio"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"adatrack_gps/ingestion-tcp/models"
)

// checksumStrict rejects a frame whose checksum does not validate. Onboarding a
// new device family occasionally needs a relaxed mode (vendor docs disagree on
// the checksum range), so it is configurable — but strict by default, so
// corrupted data is never silently accepted (PRD §9.6).
var checksumStrict = envBoolDefault("INGESTION_CHECKSUM_STRICT", true)

// SetChecksumStrict overrides the toggle (tests/diagnostics).
func SetChecksumStrict(v bool) { checksumStrict = v }

// ChecksumStrict reports the active mode.
func ChecksumStrict() bool { return checksumStrict }

// envBoolDefault reads a bool env var with a default.
func envBoolDefault(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return b
}

// serveLineProtocol runs a line-oriented serve loop for one connection: idle
// deadline → read one delimiter-bounded frame → hand it to handle. `handle`
// returns an optional reply frame and whether the connection must close (e.g.
// anti-spoofing rejection).
func (s *Server) serveLineProtocol(c net.Conn, protoName string, delim byte, max int,
	handle func(line []byte) (reply []byte, closeConn bool)) {

	r := bufio.NewReader(c)
	for {
		_ = c.SetReadDeadline(time.Now().Add(s.cfg.TCP.IdleTimeout))
		line, err := readLine(r, delim, max)
		if err != nil {
			// A clean disconnect (EOF) is normal device behaviour, not a parse
			// error — only real framing failures are counted (same rule as the
			// GT06/Teltonika handlers).
			if err != io.EOF {
				tcpParseErrors.WithLabelValues(protoName).Inc()
			}
			return
		}
		reply, closeConn := handle(line)
		if len(reply) > 0 && !writeAll(c, reply) {
			return
		}
		if closeConn {
			return
		}
	}
}

// ensureAuth performs the tenant allowlist check (FR-1.4) exactly once per
// connection: the first frame that carries a device identity authenticates it.
func (s *Server) ensureAuth(st *session, imei, protoName, remote string) bool {
	if st.authenticated() || imei == "" {
		return true
	}
	if !isIMEI(imei) {
		rejectedTotal.WithLabelValues("invalid_imei").Inc()
		return false
	}
	return s.login(st, imei, protoName, remote)
}

// parseTK103Sentence decodes the TK103 "tracker" report:
//
//	imei:<15>,tracker,<ddmmyyhhmmss>,,<F|L>,<hhmmss.sss>,<A|V>,<lat>,<N|S>,<lon>,<E|W>,<speed>,<course>,...
//
// Field order follows the upstream Tk103ProtocolDecoder (device id, type, date,
// validity, lat, lon, speed, time, course) restricted to the plain-position
// subset. The full TK103 command matrix (alarms, RFID, BMS/OBD, temperature) is
// NOT covered — an honest gap recorded in docs/B8-B10-VERIFICATION.md §3.
func parseTK103Sentence(sentence string) (models.TelemetryMessage, bool) {
	var t models.TelemetryMessage
	parts := strings.Split(strings.TrimSuffix(strings.TrimSpace(sentence), ";"), ",")
	if len(parts) < 12 || !strings.HasPrefix(parts[1], "tracker") {
		return t, false
	}
	idx := 2 // parts[2] = date/time block (ddmmyyhhmmss)
	dateBlock := strings.TrimSpace(parts[idx])
	if len(dateBlock) < 12 {
		return t, false
	}
	dateStr := dateBlock[0:6] // ddmmyy
	idx += 2                  // skip the date block + the empty field
	if idx+6 >= len(parts) {
		return t, false
	}
	idx++ // signal (F/L)
	timeStr := strings.TrimSpace(parts[idx])
	idx++
	validity := strings.TrimSpace(parts[idx])
	idx++
	if idx+4 >= len(parts) {
		return t, false
	}
	lat, ok := nmeaCoordinate(parts[idx], parts[idx+1])
	if !ok {
		return t, false
	}
	idx += 2
	lon, ok := nmeaCoordinate(parts[idx], parts[idx+1])
	if !ok {
		return t, false
	}
	idx += 2
	ts, ok := nmeaTimestamp(timeStr, dateStr)
	if !ok {
		return t, false
	}
	speedKMH, _ := parseFloatField(parts[idx])
	idx++
	heading := 0.0
	if idx < len(parts) {
		heading, _ = parseFloatField(parts[idx])
	}

	t.Timestamp = ts
	t.Lat, t.Lon = lat, lon
	t.Speed = speedKMH // TK103 reports km/h (upstream: convertSpeed(..., "kmh"))
	t.Heading = int16(heading)
	t.Fix = strings.EqualFold(validity, "A")
	return t, true
}

func parseIMEIPrefix(line string) string {
	lower := strings.ToLower(line)
	if idx := strings.Index(lower, "imei:"); idx >= 0 {
		tail := strings.TrimLeft(line[idx+len("imei:"):], ":")
		if len(tail) >= 15 {
			return tail[:15]
		}
		return ""
	}
	// Bare (or leading-comma) 15-digit IMEI: `##,123456789012345,A`.
	for _, field := range strings.Split(line, ",") {
		if f := strings.TrimSpace(field); isIMEI(f) {
			return f
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// NMEA sentence parsers (Xexun, Totem pattern 1, Meiligao, TK103)
// ---------------------------------------------------------------------------

// parseGPRMCSentence decodes `GPRMC/GNRMC,hhmmss.sss,A,ddmm.mmmm,N,dddmm.mmmm,E,
// speed,course,ddmmyy,...` — the layout shared by Xexun (basic + full) and Totem
// pattern 1 (docs/docs-device/traccar-reference/02b-xexun.md, 03-priority-medium.md §3.1).
//
// NMEA speed is in KNOTS while this pipeline stores km/h (FR-1.2), so it is
// converted here — the same convention GT06 uses (knots × 1.852).
func parseGPRMCSentence(sentence string) (models.TelemetryMessage, bool) {
	var t models.TelemetryMessage
	body := strings.TrimSpace(sentence)
	// Protocols that embed the sentence inside a larger frame (Totem) prefix it
	// with a '$'; others (Xexun) start straight at the tag.
	if idx := strings.IndexByte(body, '$'); idx >= 0 {
		body = body[idx+1:]
	}
	if !strings.HasPrefix(body, "GPRMC") && !strings.HasPrefix(body, "GNRMC") {
		return t, false
	}
	if star := strings.IndexByte(body, '*'); star >= 0 {
		body = body[:star] // drop the NMEA checksum tail
	}
	f := strings.Split(body, ",")
	if len(f) < 10 {
		return t, false
	}
	ts, ok := nmeaTimestamp(f[1], f[9])
	if !ok {
		return t, false
	}
	lat, ok := nmeaCoordinate(f[3], f[4])
	if !ok {
		return t, false
	}
	lon, ok := nmeaCoordinate(f[5], f[6])
	if !ok {
		return t, false
	}
	speedKnots, _ := parseFloatField(f[7])
	heading, _ := parseFloatField(f[8])

	t.Timestamp = ts
	t.Lat, t.Lon = lat, lon
	t.Speed = speedKnots * 1.852 // knots → km/h (FR-1.2 canonical unit)
	t.Heading = int16(heading)
	t.Fix = strings.EqualFold(f[2], "A")
	return t, true
}

// parseMeiligaoSentence decodes the Meiligao regular position sentence:
// `hhmmss.sss,A,ddmm.mmmm,N,dddmm.mmmm,E,speed,course,ddmmyy,...` — the NMEA body
// WITHOUT the GPRMC tag, which lives in the binary frame header instead
// (upstream MeiligaoProtocolDecoder.decodeRegular).
func parseMeiligaoSentence(sentence string) (models.TelemetryMessage, bool) {
	var t models.TelemetryMessage
	body := strings.TrimSpace(sentence)
	body = strings.TrimPrefix(body, "$")
	if star := strings.IndexByte(body, '*'); star >= 0 {
		body = body[:star]
	}
	f := strings.Split(body, ",")
	if len(f) < 9 || strings.HasPrefix(f[0], "GPRMC") || strings.HasPrefix(f[0], "GNRMC") {
		return t, false
	}
	ts, ok := nmeaTimestamp(f[0], f[8])
	if !ok {
		return t, false
	}
	lat, ok := nmeaCoordinate(f[2], f[3])
	if !ok {
		return t, false
	}
	lon, ok := nmeaCoordinate(f[4], f[5])
	if !ok {
		return t, false
	}
	speedKnots, _ := parseFloatField(f[6])
	heading, _ := parseFloatField(f[7])

	t.Timestamp = ts
	t.Lat, t.Lon = lat, lon
	t.Speed = speedKnots * 1.852
	t.Heading = int16(heading)
	t.Fix = strings.EqualFold(f[1], "A")
	return t, true
}
