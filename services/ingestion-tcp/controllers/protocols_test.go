package controllers

// protocols_test.go — B9 test vectors: sample frame → struct for every protocol
// added in the protocol expansion (Meiligao, Xexun, Suntech, H02, Totem, GT02,
// Navigil, Castel) plus the TK103 validation of the pre-existing provisional
// family. Vectors come from the documented examples in
// docs/docs-device/traccar-reference/ (cross-checked against the upstream
// decoders); the framing builders below mirror the wire layout exactly so the
// reader and the writer are verified against each other.

import (
	"encoding/binary"
	"math"
	"strings"
	"testing"
	"time"

	"adatrack_gps/ingestion-tcp/models"
)

const testIMEI = "123456789012345"

// approx compares two floats with a tolerance suited to coordinates.
func approx(got, want, tol float64) bool { return math.Abs(got-want) <= tol }

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

func TestNMEACoordinateAndTimestamp(t *testing.T) {
	lat, ok := nmeaCoordinate("4807.038", "S")
	if !ok || !approx(lat, -48.1173, 0.0001) {
		t.Fatalf("nmeaCoordinate lat = %v (ok=%v), want -48.1173", lat, ok)
	}
	lon, ok := nmeaCoordinate("01131.000", "E")
	if !ok || !approx(lon, 11.516667, 0.0001) {
		t.Fatalf("nmeaCoordinate lon = %v (ok=%v), want 11.516667", lon, ok)
	}
	if _, ok := nmeaCoordinate("", "N"); ok {
		t.Fatal("nmeaCoordinate accepted an empty field")
	}
	if _, ok := nmeaCoordinate("123456.78", "N"); ok {
		t.Fatal("nmeaCoordinate accepted a malformed field")
	}

	ts, ok := nmeaTimestamp("123519.000", "030926")
	if !ok {
		t.Fatal("nmeaTimestamp rejected a valid NMEA time/date pair")
	}
	// ddmmyy → 03 Sept 2026 (2-digit year offset from 2000, same rule as GT06).
	if want := time.Date(2026, 9, 3, 12, 35, 19, 0, time.UTC).Unix(); ts != want {
		t.Fatalf("nmeaTimestamp = %d, want %d", ts, want)
	}
	if _, ok := nmeaTimestamp("12351", "030926"); ok {
		t.Fatal("nmeaTimestamp accepted a malformed time field")
	}
	if _, ok := nmeaTimestamp("123519", "0309"); ok {
		t.Fatal("nmeaTimestamp accepted a short date field")
	}
	if _, ok := nmeaTimestamp("253519.000", "030926"); ok {
		t.Fatal("nmeaTimestamp accepted hour 25")
	}
}

func TestHexIMEIDropsFamilyMarker(t *testing.T) {
	// 0x01 + "123456789012345" as 8 bytes.
	raw := []byte{0x01, 0x23, 0x45, 0x67, 0x89, 0x01, 0x23, 0x45}
	if got := hexIMEI(raw); got != testIMEI {
		t.Fatalf("hexIMEI = %q, want %q", got, testIMEI)
	}
}

// ---------------------------------------------------------------------------
// Xexun (NMEA basic + full)
// ---------------------------------------------------------------------------

func TestParseXexunGPRMC(t *testing.T) {
	// Basic form documented in docs/docs-device/traccar-reference/02b-xexun.md.
	line := "GPRMC,123519.000,A,4807.038,S,01131.000,E,022.4,084.4,030926,,,A*6C,imei:" + testIMEI + ";"
	tele, ok := parseGPRMCSentence(line)
	if !ok {
		t.Fatal("parseGPRMCSentence rejected the documented Xexun sentence")
	}
	if !approx(tele.Lat, -48.1173, 0.0001) || !approx(tele.Lon, 11.516667, 0.0001) {
		t.Fatalf("xexun position = (%v,%v), want (-48.1173,11.516667)", tele.Lat, tele.Lon)
	}
	if !approx(tele.Speed, 41.4848, 0.01) {
		t.Fatalf("xexun speed = %v km/h, want 41.4848 (knots → km/h)", tele.Speed)
	}
	if tele.Heading != 84 || !tele.Fix {
		t.Fatalf("xexun heading/fix = %d/%v, want 84/true", tele.Heading, tele.Fix)
	}
	if tele.Timestamp != time.Date(2026, 9, 3, 12, 35, 19, 0, time.UTC).Unix() {
		t.Fatalf("xexun timestamp = %d, want 2026-09-03T12:35:19Z", tele.Timestamp)
	}
	if got := parseIMEIPrefix(line); got != testIMEI {
		t.Fatalf("parseIMEIPrefix = %q, want %q", got, testIMEI)
	}
}

func TestApplyXexunStatusIsTristate(t *testing.T) {
	// ACC on/off are literal device statements; an unrelated sentence must not
	// invent an ACC value (B6 tri-state contract).
	on := models.TelemetryMessage{}
	applyXexunStatus(&on, "imei:"+testIMEI+",acc on;")
	if on.ACC == nil || !*on.ACC {
		t.Fatal("acc on did not set ACC=true")
	}
	off := models.TelemetryMessage{}
	applyXexunStatus(&off, "imei:"+testIMEI+",acc off;")
	if off.ACC == nil || *off.ACC {
		t.Fatal("acc off did not set ACC=false")
	}
	none := models.TelemetryMessage{}
	applyXexunStatus(&none, "imei:"+testIMEI+",heartbeat;")
	if none.ACC != nil {
		t.Fatal("a heartbeat sentence invented an ACC value")
	}
	if none.HarshAccel || none.HarshBraking {
		t.Fatal("a heartbeat sentence invented a driver event")
	}
	sos := models.TelemetryMessage{}
	applyXexunStatus(&sos, "imei:"+testIMEI+",help me!;")
	if sos.AlarmCode == 0 {
		t.Fatal("help me! did not raise the SOS alarm code")
	}
}

// ---------------------------------------------------------------------------
// Meiligao (binary framing + ASCII position sentence)
// ---------------------------------------------------------------------------

// buildMeiligaoFrame mirrors the wire layout the reader implements, so reader and
// writer are verified against each other.
func buildMeiligaoFrame(command uint16, payload []byte, serial uint16) []byte {
	content := make([]byte, 0, 4+len(payload))
	content = append(content, byte(command>>8), byte(command))
	content = append(content, payload...)
	content = append(content, byte(serial>>8), byte(serial))

	length := len(content)
	frame := []byte{0x24, 0x24, byte(length >> 8), byte(length)}
	frame = append(frame, content...)
	cks := xorChecksum(append([]byte{byte(length >> 8), byte(length)}, content...))
	frame = append(frame, 0x00, cks, 0x0D, 0x0A)
	return frame
}

func TestMeiligaoLoginAndPosition(t *testing.T) {
	// Login carries the IMEI as 7 BCD bytes (14 digits).
	imeiBCD := []byte{0x12, 0x34, 0x56, 0x78, 0x90, 0x12, 0x34}
	frame := buildMeiligaoFrame(meiligaoMsgLogin, imeiBCD, 1)

	got, err := readMeiligaoFrame(bufReader(frame))
	if err != nil {
		t.Fatalf("readMeiligaoFrame(login): %v", err)
	}
	if got.Command != meiligaoMsgLogin {
		t.Fatalf("login command = 0x%04x, want 0x%04x", got.Command, meiligaoMsgLogin)
	}
	if imei := bcdIMEI(got.Payload); imei != "12345678901234" {
		t.Fatalf("bcdIMEI = %q, want 12345678901234", imei)
	}
	if padded := padIMEI("12345678901234"); padded != "012345678901234" || !isIMEI(padded) {
		t.Fatalf("padIMEI = %q, want the 15-digit allowlist form", padded)
	}

	// Position: the payload is the NMEA body WITHOUT the GPRMC tag.
	sentence := "123519.000,A,4807.038,S,01131.000,E,022.4,084.4,030926,"
	pos := buildMeiligaoFrame(meiligaoMsgPosition, []byte(sentence), 2)
	pf, err := readMeiligaoFrame(bufReader(pos))
	if err != nil {
		t.Fatalf("readMeiligaoFrame(position): %v", err)
	}
	tele, ok := parseMeiligaoSentence(string(pf.Payload))
	if !ok {
		t.Fatal("parseMeiligaoSentence rejected the documented position sentence")
	}
	if !approx(tele.Lat, -48.1173, 0.0001) || !approx(tele.Lon, 11.516667, 0.0001) {
		t.Fatalf("meiligao position = (%v,%v)", tele.Lat, tele.Lon)
	}
	if !tele.Fix {
		t.Fatal("meiligao validity 'A' did not set Fix")
	}

	// An ACK is well formed and echoes the IMEI + serial.
	ack := buildMeiligaoAck(imeiBCD, 7)
	if ack[0] != 0x24 || ack[1] != 0x24 {
		t.Fatalf("ack start bytes = %02x %02x", ack[0], ack[1])
	}
	if ack[len(ack)-2] != 0x0D || ack[len(ack)-1] != 0x0A {
		t.Fatalf("ack stop bytes = %02x %02x", ack[len(ack)-2], ack[len(ack)-1])
	}
}

func TestMeiligaoRejectsCorruptedChecksum(t *testing.T) {
	frame := buildMeiligaoFrame(meiligaoMsgHeartbeat, []byte{0x00}, 1)
	frame[len(frame)-3] ^= 0xFF // corrupt the checksum byte
	if _, err := readMeiligaoFrame(bufReader(frame)); err == nil {
		t.Fatal("readMeiligaoFrame accepted a frame with a corrupted checksum")
	}
}

// ---------------------------------------------------------------------------
// H02 (text V3)
// ---------------------------------------------------------------------------

func TestParseH02V3(t *testing.T) {
	fields := []string{testIMEI, "V3", "20260924120000", "A", "4807.038", "S", "01131.000", "E", "22.4", "84.4", "1025"}
	tele, ok := parseH02V3(fields)
	if !ok {
		t.Fatal("parseH02V3 rejected the documented V3 sentence")
	}
	if !approx(tele.Lat, -48.1173, 0.0001) || !approx(tele.Lon, 11.516667, 0.0001) {
		t.Fatalf("h02 position = (%v,%v)", tele.Lat, tele.Lon)
	}
	if tele.Timestamp != time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC).Unix() {
		t.Fatalf("h02 timestamp = %d, want 2026-09-24T12:00:00Z", tele.Timestamp)
	}
	if tele.ACC == nil || !*tele.ACC {
		t.Fatal("h02 status bit 10 must set ACC=true")
	}
	if got := navigilKind(navigilMsgPosition); got != "position" {
		t.Fatalf("navigilKind = %q", got)
	}
	if _, ok := parseH02Timestamp("2026-09-24 12:00:00"); ok {
		t.Fatal("parseH02Timestamp accepted a non yyyyMMddHHmmss value")
	}
}

// ---------------------------------------------------------------------------
// TK103 (provisional validation, B9)
// ---------------------------------------------------------------------------

func TestParseTK103Tracker(t *testing.T) {
	// Documented TK103 "tracker" report (date/time + NMEA position + km/h speed).
	line := "imei:" + testIMEI + ",tracker,080923192922,,F,172503.000,A,5052.9669,N,00633.2109,E,10.5,120.0,;"
	tele, ok := parseTK103Sentence(line)
	if !ok {
		t.Fatal("parseTK103Sentence rejected the documented tracker report")
	}
	if !approx(tele.Lat, 50.882782, 0.0001) || !approx(tele.Lon, 6.553515, 0.0001) {
		t.Fatalf("tk103 position = (%v,%v), want (50.882782,6.553515)", tele.Lat, tele.Lon)
	}
	if !tele.Fix {
		t.Fatal("tk103 validity 'A' did not set Fix")
	}
	if !approx(tele.Speed, 10.5, 0.001) {
		t.Fatalf("tk103 speed = %v, want 10.5 km/h (no knots conversion)", tele.Speed)
	}
	if got := parseIMEIPrefix(line); got != testIMEI {
		t.Fatalf("parseIMEIPrefix = %q, want %q", got, testIMEI)
	}
	// Non-position frames of the (large) TK103 command matrix are rejected, not
	// misinterpreted — the documented B9 gap.
	alarm := "imei:" + testIMEI + ",ALARM,1,080923192922,A,5052.9669,N,00633.2109,E,10.5,120.0,;"
	if _, ok := parseTK103Sentence(alarm); ok {
		t.Fatal("parseTK103Sentence accepted an alarm frame it does not implement")
	}
}

// ---------------------------------------------------------------------------
// GT02 (binary, 0x68 0x68)
// ---------------------------------------------------------------------------

// buildGT02Frame mirrors the wire layout the reader implements.
func buildGT02Frame(imeiRaw []byte, msgType byte, body []byte) []byte {
	inner := []byte{0x00, 0x0F} // power, gsm
	inner = append(inner, imeiRaw...)
	inner = append(inner, 0x00, 0x01) // index
	inner = append(inner, msgType)
	inner = append(inner, body...)

	frame := []byte{0x68, 0x68, byte(len(inner))}
	frame = append(frame, inner...)
	cks := int(frame[2])
	for _, b := range inner {
		cks ^= int(b)
	}
	frame = append(frame, 0x00, byte(cks), 0x0D, 0x0A)
	return frame
}

// appendTestUint32BE appends a big-endian uint32 (test helper).
func appendTestUint32BE(b []byte, v uint32) []byte {
	return append(b, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

func TestGT02PositionAndHeartbeat(t *testing.T) {
	imeiRaw := []byte{0x01, 0x23, 0x45, 0x67, 0x89, 0x01, 0x23, 0x45}
	body := make([]byte, 0, 24)
	body = append(body, 26, 9, 24, 12, 0, 0) // YY,MM,DD,HH,MM,SS
	latRaw := 48.1173 * 1800000.0
	lonRaw := 11.516667 * 1800000.0
	body = appendTestUint32BE(body, uint32(latRaw))
	body = appendTestUint32BE(body, uint32(lonRaw))
	body = append(body, 42)       // speed km/h
	body = append(body, 0x00, 90) // course
	body = append(body, 0, 0, 0)  // reserved
	body = appendTestUint32BE(body, 0x00000007)

	frame := buildGT02Frame(imeiRaw, gt02MsgData, body)
	f, err := readGT02Frame(bufReader(frame))
	if err != nil {
		t.Fatalf("readGT02Frame: %v", err)
	}
	if f.IMEI != testIMEI {
		t.Fatalf("gt02 imei = %q, want %q", f.IMEI, testIMEI)
	}
	if f.Type != gt02MsgData {
		t.Fatalf("gt02 type = 0x%02x, want 0x10", f.Type)
	}
	tele, ok := parseGT02Position(f.Body)
	if !ok {
		t.Fatal("parseGT02Position rejected a valid MSG_DATA payload")
	}
	if !approx(tele.Lat, 48.1173, 0.0001) || !approx(tele.Lon, 11.516667, 0.0001) {
		t.Fatalf("gt02 position = (%v,%v)", tele.Lat, tele.Lon)
	}
	if tele.Speed != 42 || tele.Heading != 90 || !tele.Fix {
		t.Fatalf("gt02 speed/heading/fix = %v/%v/%v", tele.Speed, tele.Heading, tele.Fix)
	}
	if tele.Timestamp != time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC).Unix() {
		t.Fatalf("gt02 timestamp = %d", tele.Timestamp)
	}

	// Southern/western hemispheres flip the sign (flags bit1/bit2 clear).
	bodyWest := append([]byte{}, body[:15]...)
	bodyWest = append(bodyWest, 0, 0, 0)
	bodyWest = appendTestUint32BE(bodyWest, 0x00000001)
	west, ok := parseGT02Position(bodyWest)
	if !ok {
		t.Fatal("parseGT02Position rejected the southern/western payload")
	}
	if west.Lat >= 0 || west.Lon >= 0 {
		t.Fatalf("gt02 hemisphere sign ignored: (%v,%v)", west.Lat, west.Lon)
	}

	// Bad start bytes must not be silently accepted.
	bad := append([]byte{}, frame...)
	bad[1] = 0x67
	if _, err := readGT02Frame(bufReader(bad)); err == nil {
		t.Fatal("readGT02Frame accepted a frame with bad start bytes")
	}
}

// ---------------------------------------------------------------------------
// Totem (PATTERN_1), Navigil (header) and Castel (framing + IMEI in the id)
// ---------------------------------------------------------------------------

// TestMeiligaoPositionCommandVariants covers the interop fix: the upstream
// revisions disagree on the command ids, so the decoder accepts the union and the
// strict sentence parser decides (plus an optional leading alarm byte / 6-byte
// logged header).
func TestMeiligaoPositionCommandVariants(t *testing.T) {
	sentence := "063519.000,A,0612.0000,S,10650.0000,E,022.4,084.4,240926,"

	// The real device frame from the upstream test suite carries a position under
	// 0x9999 with NO leading alarm byte (the in-repo doc calls 0x9999 the login ACK).
	for _, cmd := range []uint16{meiligaoMsgPositionLatest, meiligaoMsgPosition,
		meiligaoMsgPositionLegacy} {
		tele, alarm, ok := parseMeiligaoPositionPayload([]byte(sentence))
		if !ok {
			t.Fatalf("command 0x%04x: payload rejected", cmd)
		}
		if alarm != 0 {
			t.Fatalf("command 0x%04x: alarm = %d, want 0", cmd, alarm)
		}
		if !approx(tele.Lat, -6.2, 1e-6) || !approx(tele.Lon, 106.833333, 1e-6) {
			t.Fatalf("command 0x%04x: position = (%v,%v)", cmd, tele.Lat, tele.Lon)
		}
		if !isMeiligaoPositionCommand(cmd) {
			t.Fatalf("command 0x%04x must be routed to the position path", cmd)
		}
	}

	// Alarm-style payload: one alarm byte in front of the sentence.
	withAlarm := append([]byte{0x05}, []byte(sentence)...)
	tele, alarm, ok := parseMeiligaoPositionPayload(withAlarm)
	if !ok || alarm != 0x05 {
		t.Fatalf("alarm payload: ok=%v alarm=%d, want true/5", ok, alarm)
	}
	if !approx(tele.Lat, -6.2, 1e-6) {
		t.Fatalf("alarm payload position = (%v,%v)", tele.Lat, tele.Lon)
	}

	// Logged-position style payload: six skipped bytes.
	withSkip := append([]byte{0, 0, 0, 0, 0, 0}, []byte(sentence)...)
	if _, _, ok := parseMeiligaoPositionPayload(withSkip); !ok {
		t.Fatal("logged-position payload (6-byte header) was rejected")
	}

	// Garbage must never be decoded into a position.
	if _, _, ok := parseMeiligaoPositionPayload([]byte("not a sentence")); ok {
		t.Fatal("garbage payload was accepted")
	}
}

// TestMeiligaoLuhnIMEI: a real terminal reports a 14-digit id, and upstream
// completes it with the IMEI Luhn check digit (not a leading zero) — the allowlist
// stores the 15-digit form, so the candidate order matters.
func TestMeiligaoLuhnIMEI(t *testing.T) {
	// 14 digits whose Luhn check digit is 4 (hand-computed): 86420104051234 → …344
	if got := luhnIMEI("86420104051234"); got != "864201040512344" {
		t.Fatalf("luhnIMEI = %q, want 864201040512344", got)
	}
	if !isIMEI(luhnIMEI("86420104051234")) {
		t.Fatal("the Luhn-completed id is not a valid 15-digit IMEI")
	}
	// Unknown length / non-digits are rejected instead of padded.
	if got := luhnIMEI("1234"); got != "" {
		t.Fatalf("luhnIMEI(short) = %q, want empty", got)
	}
	if got := luhnIMEI("1234567890123x"); got != "" {
		t.Fatalf("luhnIMEI(non-digits) = %q, want empty", got)
	}
	// The legacy padded candidate is still produced by padIMEI for older firmwares.
	if got := padIMEI("86420104051234"); got != "086420104051234" {
		t.Fatalf("padIMEI = %q, want 086420104051234", got)
	}
}

// TestTK103HandshakeAndOdometer covers the handshake login and the optional
// `L<hex>` odometer of the upstream pattern.
func TestTK103HandshakeAndOdometer(t *testing.T) {
	frame := "(123456789012BP0012345678)"
	id, kind, ok := parseTK103Handshake(frame)
	if !ok || id != "123456789012" || kind != "BP00" {
		t.Fatalf("handshake parse = %q/%q/%v", id, kind, ok)
	}
	if got := tk103Trailer(frame); got != "678" {
		t.Fatalf("handshake trailer = %q, want 678", got)
	}
	if _, kind, ok := parseTK103Handshake("(123456789012BP05)"); !ok || kind != "BP05" {
		t.Fatalf("BP05 handshake not recognised (kind=%q ok=%v)", kind, ok)
	}
	// A position frame must never be mistaken for a handshake.
	if _, _, ok := parseTK103Handshake("imei:864201040512345,tracker,240926063519,"); ok {
		t.Fatal("a tracker sentence was parsed as a handshake")
	}

	line := "imei:864201040512345,tracker,240926063519,,F,063519.000,A,0612.0000,S," +
		"10650.0000,E,022.4,084.4,00000001,L1B4F)"
	tele, ok := parseTK103Sentence(line)
	if !ok {
		t.Fatalf("parseTK103Sentence rejected %q", line)
	}
	if tele.Mileage != 0x1B4F {
		t.Fatalf("odometer = %d, want 0x1B4F (%d)", tele.Mileage, 0x1B4F)
	}
	if !approx(tele.Lat, -6.2, 1e-6) || !approx(tele.Lon, 106.833333, 1e-6) {
		t.Fatalf("position = (%v,%v)", tele.Lat, tele.Lon)
	}
	// A sentence without the odometer keeps it zero (never coerced from the state).
	noOdo := "imei:864201040512345,tracker,240926063519,,F,063519.000,A,0612.0000,S," +
		"10650.0000,E,022.4,084.4)"
	if tele, ok := parseTK103Sentence(noOdo); !ok || tele.Mileage != 0 {
		t.Fatalf("odometer without L-field = %d (ok=%v), want 0", tele.Mileage, ok)
	}
}

// TestParseTotemPattern2 covers the pipe-delimited PATTERN_2 sentence. The layout
// follows the upstream regex: `$$<len><IMEI>|<alarm><DDMMYY><HHMMSS>|<A/V>|…`.
func TestParseTotemPattern2(t *testing.T) {
	line := "$$0A" + testIMEI + "|AB240926063519|A|0612.0000|S|10650.0000|E|12.3|84" +
		"|1.0|1|100|12.5|1|0|25|1234|1"
	tele, ok := parseTotemPattern2(line)
	if !ok {
		t.Fatalf("parseTotemPattern2 rejected %q", line)
	}
	if !approx(tele.Lat, -6.2, 1e-6) || !approx(tele.Lon, 106.833333, 1e-6) {
		t.Fatalf("totem p2 position = (%v,%v), want (-6.2,106.833333)", tele.Lat, tele.Lon)
	}
	if !approx(tele.Speed, 12.3*1.852, 1e-6) {
		t.Fatalf("totem p2 speed = %v km/h, want %v", tele.Speed, 12.3*1.852)
	}
	if tele.Heading != 84 || !tele.Fix {
		t.Fatalf("totem p2 heading/fix = %d/%v, want 84/true", tele.Heading, tele.Fix)
	}
	want := time.Date(2026, 9, 24, 6, 35, 19, 0, time.UTC).Unix()
	if tele.Timestamp != want {
		t.Fatalf("totem p2 timestamp = %d, want %d", tele.Timestamp, want)
	}

	// The doc layout (a pipe after the length) is tolerated as well.
	doc := "$$0A|" + testIMEI + "|AB240926063519|A|0612.0000|S|10650.0000|E|12.3|84|x"
	if _, ok := parseTotemPattern2(doc); !ok {
		t.Fatalf("parseTotemPattern2 rejected the documented layout %q", doc)
	}

	// PATTERN_1 frames must not be swallowed by the pattern-2 parser.
	p1 := "$$0A" + testIMEI + "|AB$GPRMC,123519.000,A,4807.038,S,01131.000,E,022.4,084.4,030926,,,A*6C|1.0|1.0|1.0|1|100|12.5|1|0|25|1234|1"
	if _, ok := parseTotemPattern2(p1); ok {
		t.Fatal("a PATTERN_1 frame was parsed as PATTERN_2")
	}
	// The GPRMC body is found regardless of its pipe index (identity + body search).
	if body, idx := totemGPRMCField(strings.Split(p1, "|")); body == "" || idx != 1 {
		t.Fatalf("gprmc field = %q idx=%d, want the body at index 1", body, idx)
	}
	// Identity: upstream layout (`$$<len><IMEI>|`) and doc layout (`$$<len>|<IMEI>|`)
	// must both resolve — otherwise a real Totem device never authenticates.
	if got := totemIMEI(line); got != testIMEI {
		t.Fatalf("totemIMEI(upstream layout) = %q, want %q", got, testIMEI)
	}
	if got := totemIMEI(doc); got != testIMEI {
		t.Fatalf("totemIMEI(doc layout) = %q, want %q", got, testIMEI)
	}
	if got := totemIMEI("$$0A|no-id-here|AB"); got != "" {
		t.Fatalf("totemIMEI(no id) = %q, want empty", got)
	}
}

// TestCastelPositionPayload covers the opt-in Castel position decoder (scales
// verified upstream; sign bits chosen explicitly — see proto_castel.go).
func TestCastelPositionPayload(t *testing.T) {
	saved := castelGPSMode
	defer func() { castelGPSMode = saved }()

	body := make([]byte, castelPositionPayloadBytes)
	body[0], body[1], body[2] = 24, 9, 26                             // day, month, year-2000
	body[3], body[4], body[5] = 6, 35, 19                             // hour, minute, second
	binary.LittleEndian.PutUint32(body[6:10], uint32(6.2*3600000))    // |lat| * 3.6e6
	binary.LittleEndian.PutUint32(body[10:14], uint32(106.8*3600000)) // |lon| * 3.6e6
	binary.LittleEndian.PutUint16(body[14:16], 1000)                  // 1000 cm/s = 36 km/h
	binary.LittleEndian.PutUint16(body[16:18], 840)                   // 84.0 degrees
	body[18] = 0x0A | 0x20                                            // lat sign bit clear (south), sats

	// Default: decoding is OFF → the frame stays counted as unsupported.
	castelGPSMode = castelGPSOff
	if _, ok := parseCastelPosition(body); ok {
		t.Fatal("position decoded while CASTEL_GPS_DECODE=off")
	}

	// Legacy sign bits: bit0 = latitude sign (0 → south), bit1 = longitude sign (1 → east).
	castelGPSMode = castelGPSOn
	tele, ok := parseCastelPosition(body)
	if !ok {
		t.Fatal("legacy mode rejected a valid position block")
	}
	if !approx(tele.Lat, -6.2, 1e-6) || !approx(tele.Lon, 106.8, 1e-6) {
		t.Fatalf("legacy position = (%v,%v), want (-6.2,106.8)", tele.Lat, tele.Lon)
	}
	if !approx(tele.Speed, 36, 1e-6) || tele.Heading != 84 {
		t.Fatalf("speed/heading = %v/%d, want 36/84", tele.Speed, tele.Heading)
	}
	if tele.Satellites != 2 {
		t.Fatalf("satellites = %d, want 2 (high nibble of 0x20)", tele.Satellites)
	}
	want := time.Date(2026, 9, 24, 6, 35, 19, 0, time.UTC).Unix()
	if tele.Timestamp != want || !tele.Fix {
		t.Fatalf("timestamp/fix = %d/%v, want %d/true", tele.Timestamp, tele.Fix, want)
	}

	// Swapped mode: bit1 = latitude sign, bit0 = longitude sign → different result.
	castelGPSMode = castelGPSOnSwapped
	swapped, ok := parseCastelPosition(body)
	if !ok {
		t.Fatal("swapped mode rejected a valid position block")
	}
	if !approx(swapped.Lat, 6.2, 1e-6) {
		t.Fatalf("swapped latitude = %v, want 6.2 (bit1 clear → south in legacy reads north here)", swapped.Lat)
	}

	// A short or implausible block never produces a position.
	if _, ok := parseCastelPosition(body[:10]); ok {
		t.Fatal("a truncated position block was accepted")
	}
	bad := append([]byte{}, body...)
	bad[1] = 13 // month 13
	if _, ok := parseCastelPosition(bad); ok {
		t.Fatal("an impossible month was accepted")
	}
}

func TestParseTotemPattern1(t *testing.T) {
	line := "$$0123|" + testIMEI + "|help me!$GPRMC,123519.000,A,4807.038,S,01131.000,E,022.4,084.4,030926,,,A*6C" +
		"|1|2|3|4|80|12.3|5|0|0|25|98765|1234"
	fields := splitPipeFields(line)
	tele, ok := parseGPRMCSentence(fields[2])
	if !ok {
		t.Fatal("totem PATTERN_1 GPRMC body was rejected")
	}
	applyTotemExtras(&tele, fields)
	if tele.Battery != 80 {
		t.Fatalf("totem battery = %d, want 80", tele.Battery)
	}
	if tele.Mileage != 98765 {
		t.Fatalf("totem odometer = %d, want 98765", tele.Mileage)
	}
}

// splitPipeFields splits a Totem frame on '|' (test helper).
func splitPipeFields(line string) []string {
	var out []string
	cur := ""
	for _, r := range line {
		if r == '|' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	return append(out, cur)
}

func TestNavigilHeaderAndAck(t *testing.T) {
	hdr := make([]byte, 20)
	hdr[0], hdr[1] = 1, 1
	hdr[2], hdr[3] = 0x34, 0x12 // sequence 0x1234 (little-endian)
	hdr[4], hdr[5] = byte(navigilMsgPosition), 0
	hdr[6], hdr[7] = 0x10, 0x00 // payload length 16
	hdr[12], hdr[13], hdr[14], hdr[15] = 0x78, 0x56, 0x34, 0x12

	got, err := readNavigilHeader(bufReader(hdr))
	if err != nil {
		t.Fatalf("readNavigilHeader: %v", err)
	}
	if got.Sequence != 0x1234 || got.MsgID != navigilMsgPosition || got.PayloadLen != 16 {
		t.Fatalf("navigil header = %+v", got)
	}
	if _, err := readNavigilHeader(bufReader(make([]byte, 20))); err == nil {
		t.Fatal("readNavigilHeader accepted a zeroed (implausible) header")
	}

	// The ACK is 24 bytes: 20-byte header + 4-byte data (seq + status OK), and the
	// CRC-16/CCITT-FALSE covers the DATA only (upstream sendAcknowledgment).
	ack := buildNavigilAck(got.Sequence)
	if len(ack) != 24 {
		t.Fatalf("navigil ack length = %d, want 24", len(ack))
	}
	if binary.LittleEndian.Uint16(ack[4:6]) != navigilMsgAck {
		t.Fatalf("ack msg id = %d, want %d", binary.LittleEndian.Uint16(ack[4:6]), navigilMsgAck)
	}
	if binary.LittleEndian.Uint16(ack[6:8]) != 24 {
		t.Fatalf("ack length field = %d, want 24", binary.LittleEndian.Uint16(ack[6:8]))
	}
	if binary.LittleEndian.Uint16(ack[20:22]) != got.Sequence {
		t.Fatalf("ack does not echo the sequence number")
	}
	if binary.LittleEndian.Uint16(ack[10:12]) != navigilCRC16(ack[20:24]) {
		t.Fatal("ack checksum is not the CRC-16/CCITT-FALSE of the data bytes")
	}
}

// TestParseNavigilPayload covers the two layouts the reference pins: the unit
// report (MSG 8) and the tracking data (MSG 18).
func TestParseNavigilPayload(t *testing.T) {
	// MSG_UNIT_REPORT: trigger(2) flags(2) lat(4) lon(4) altitude(2) satellites(2)
	latRaw, lonRaw := int32(-62000000), int32(1068000000)
	unit := make([]byte, 20)
	binary.LittleEndian.PutUint16(unit[0:2], 1)    // trigger
	binary.LittleEndian.PutUint16(unit[2:4], 0x10) // flags
	binary.LittleEndian.PutUint32(unit[4:8], uint32(latRaw))
	binary.LittleEndian.PutUint32(unit[8:12], uint32(lonRaw))
	binary.LittleEndian.PutUint16(unit[12:14], 45)
	binary.LittleEndian.PutUint16(unit[14:16], 9)

	tele, ok := parseNavigilPayload(navigilMsgUnitReport, 1790274600, unit)
	if !ok {
		t.Fatal("unit report payload was rejected")
	}
	if !approx(tele.Lat, -6.2, 1e-6) || !approx(tele.Lon, 106.8, 1e-6) {
		t.Fatalf("unit report position = (%v,%v), want (-6.2,106.8)", tele.Lat, tele.Lon)
	}
	if tele.Altitude != 45 || tele.Satellites != 9 || !tele.Fix {
		t.Fatalf("unit report altitude/sats/fix = %d/%d/%v", tele.Altitude, tele.Satellites, tele.Fix)
	}
	if tele.Timestamp != 1790274600-navigilLeapSecondsDelta {
		t.Fatalf("unit report timestamp = %d, want header ts - 25", tele.Timestamp)
	}

	// MSG_TRACKING_DATA: mode(1) flags(1) duration(2) lat(4) lon(4) speed(1)
	// course(1) satellites(1) battery(2) odometer(4)
	track := make([]byte, 22)
	track[0] = 2 // mode
	track[1] = 0x01
	binary.LittleEndian.PutUint32(track[4:8], uint32(latRaw))
	binary.LittleEndian.PutUint32(track[8:12], uint32(lonRaw))
	track[12] = 55 // km/h
	track[13] = 30 // course 60°
	track[14] = 7
	binary.LittleEndian.PutUint16(track[15:17], 12400) // 12.4 V
	binary.LittleEndian.PutUint32(track[17:21], 98765)

	tele, ok = parseNavigilPayload(navigilMsgTracking, 1790274600, track)
	if !ok {
		t.Fatal("tracking payload was rejected")
	}
	if tele.Speed != 55 || tele.Heading != 60 || !tele.Fix || tele.Satellites != 7 {
		t.Fatalf("tracking speed/heading/fix/sats = %v/%v/%v/%v", tele.Speed, tele.Heading, tele.Fix, tele.Satellites)
	}
	if !approx(tele.Lat, -6.2, 1e-6) {
		t.Fatalf("tracking position = (%v,%v)", tele.Lat, tele.Lon)
	}

	// A truncated payload must be rejected, and an unknown message id reported as
	// unsupported (the documented gap for types 13/15).
	if _, ok := parseNavigilPayload(navigilMsgUnitReport, 1, unit[:10]); ok {
		t.Fatal("a truncated unit report was accepted")
	}
	if _, ok := parseNavigilPayload(navigilMsgPosition, 1, unit); ok {
		t.Fatal("message id 13 must stay unsupported (leading fields not pinned upstream)")
	}
}

func TestNavigilDeviceMap(t *testing.T) {
	m := parseNavigilDeviceMap("1234567=864201040512345, 7654321=864201040512999,bad=864201040512000,42=short,88=864201040512888")
	if len(m) != 3 {
		t.Fatalf("device map size = %d, want 3 (invalid pairs skipped)", len(m))
	}
	if imei, ok := m[1234567]; !ok || imei != "864201040512345" {
		t.Fatalf("device 1234567 → %q/%v", imei, ok)
	}
	if _, ok := m[42]; ok {
		t.Fatal("a non-15-digit IMEI was mapped (anti-spoofing must stay strict)")
	}
	// The live lookup reads the process-wide map, which is empty unless
	// NAVIGIL_DEVICE_MAP is configured — an unmapped device must never resolve.
	if _, ok := navigilIMEI(1234567); ok && len(navigilDeviceMap) == 0 {
		t.Fatal("navigilIMEI resolved a device without a configured mapping")
	}
}

func TestParseSuntechTextLine(t *testing.T) {
	// Classic universal sentence (upstream `universal` pattern). Note the sign on
	// BOTH coordinates: the reference requires `[-+]` for latitude and longitude.
	line := "ST300STT;" + testIMEI + ";1;20260924;06:35:19;ABCD;-06.20;+106.80;041.000;084.00;0000;1"

	tele, ok := parseSuntechTextLine(line)
	if !ok {
		t.Fatalf("parseSuntechTextLine rejected %q", line)
	}
	if !approx(tele.Lat, -6.2, 1e-9) || !approx(tele.Lon, 106.8, 1e-9) {
		t.Fatalf("suntech position = (%v,%v), want (-6.2,106.8)", tele.Lat, tele.Lon)
	}
	if !approx(tele.Speed, 41, 1e-9) {
		t.Fatalf("suntech speed = %v km/h, want 41 (wire unit is already km/h)", tele.Speed)
	}
	if tele.Heading != 84 || !tele.Fix {
		t.Fatalf("suntech heading/fix = %d/%v, want 84/true", tele.Heading, tele.Fix)
	}
	want := time.Date(2026, 9, 24, 6, 35, 19, 0, time.UTC).Unix()
	if tele.Timestamp != want {
		t.Fatalf("suntech timestamp = %d, want %d", tele.Timestamp, want)
	}

	// The version field is REQUIRED by the reference pattern (only the extra field
	// before it is optional), so a sentence without it stays unsupported rather than
	// being matched with shifted groups.
	noVersion := "ST215;" + testIMEI + ";20260924;06:35:19;-06.20;+106.80;041.000;084.00;"
	if _, ok := parseSuntechTextLine(noVersion); ok {
		t.Fatalf("parseSuntechTextLine accepted a sentence without the version field: %q", noVersion)
	}

	// A legacy 6-digit device id cannot be authenticated against the IMEI allowlist
	// (FR-1.4) → counted as unsupported instead of attributed to a device.
	legacy := "ST215;123456;1;20260924;06:35:19;-06.20;+106.80;041.000;084.00;"
	if _, ok := parseSuntechTextLine(legacy); ok {
		t.Fatal("a 6-digit legacy id was accepted as an IMEI")
	}
	// Out-of-range coordinates and malformed values are rejected, never published.
	for _, bad := range []string{
		"ST215;" + testIMEI + ";1;20260924;06:35:19;-99.20;+106.80;041.000;084.00;",
		"ST215;" + testIMEI + ";1;20260924;06:35:19;-06.20;+999.80;041.000;084.00;",
		"ST215;" + testIMEI + ";1;20261324;06:35:19;-06.20;+106.80;041.000;084.00;",
		"ST215;" + testIMEI + ";1;20260924;25:35:19;-06.20;+106.80;041.000;084.00;",
		"ST215;" + testIMEI + ";1;20260924;06:35:19;-6.2;+106.8;41.0;84.0;",
		"ST215;" + testIMEI + ";1;20260924;06:35:19;-06.20;106.80;041.000;084.00;", // unsigned longitude
	} {
		if _, ok := parseSuntechTextLine(bad); ok {
			t.Fatalf("parseSuntechTextLine accepted an invalid sentence: %q", bad)
		}
	}
}

func TestCastelFrameAndIdentity(t *testing.T) {
	id := testIMEI + "ABCDE" // 20-char ASCII id carrying the IMEI

	// A device frame: header + Length(=WHOLE frame) + version + id + type + payload
	// + CRC(2 BE) + 0x0D 0x0A (see the file header note 1).
	buildDeviceFrame := func(msgType uint16, payload []byte) []byte {
		body := append([]byte{4}, []byte(id)...)
		body = binary.LittleEndian.AppendUint16(body, msgType)
		body = append(body, payload...)

		frame := binary.LittleEndian.AppendUint16(nil, castelHeaderMark)
		frame = binary.LittleEndian.AppendUint16(frame, uint16(castelHeaderBytes+len(body)+castelTailBytes))
		frame = append(frame, body...)
		frame = binary.BigEndian.AppendUint16(frame, navigilCRC16(frame))
		return append(frame, 0x0D, 0x0A)
	}

	// Two frames back to back: the reader must consume exactly the first one, which
	// is what the old "length = bytes after the header" convention broke (desync).
	frame := buildDeviceFrame(castelMsgLogin, nil)
	second := buildDeviceFrame(castelMsgHeartbeat, nil)
	r := bufReader(append(append([]byte{}, frame...), second...))

	f, err := readCastelFrame(r)
	if err != nil {
		t.Fatalf("readCastelFrame: %v", err)
	}
	if f.Type != castelMsgLogin || f.ID != id || f.Version != 4 {
		t.Fatalf("castel frame = %+v", f)
	}
	if len(f.Body) != 0 {
		t.Fatalf("castel body = % x, want empty (CRC+footer must not be payload)", f.Body)
	}
	if got := castelIMEIPattern.FindString(f.ID); got != testIMEI {
		t.Fatalf("castel IMEI extraction = %q, want %q", got, testIMEI)
	}

	next, err := readCastelFrame(r)
	if err != nil {
		t.Fatalf("second readCastelFrame: %v", err)
	}
	if next.Type != castelMsgHeartbeat {
		t.Fatalf("second frame type = 0x%04x, want 0x%04x", next.Type, castelMsgHeartbeat)
	}

	// A payload-bearing frame keeps its payload and drops the 4-byte tail.
	withBody := buildDeviceFrame(castelMsgGPS, []byte{0xAA, 0xBB, 0xCC})
	f, err = readCastelFrame(bufReader(withBody))
	if err != nil {
		t.Fatalf("readCastelFrame(gps): %v", err)
	}
	if string(f.Body) != "\xaa\xbb\xcc" {
		t.Fatalf("castel gps body = % x, want aa bb cc", f.Body)
	}

	bad := append([]byte{}, frame...)
	bad[0] = 0x41
	if _, err := readCastelFrame(bufReader(bad)); err == nil {
		t.Fatal("readCastelFrame accepted a frame with a bad header")
	}
	// A length that cannot hold the fixed fields is rejected, not parsed into garbage.
	short := append([]byte{}, frame...)
	binary.LittleEndian.PutUint16(short[2:4], 8)
	if _, err := readCastelFrame(bufReader(short)); err == nil {
		t.Fatal("readCastelFrame accepted an undersized frame length")
	}
}

// TestCastelResponseFrames covers the mandatory login/heartbeat replies: the frame
// must be parseable by the same reader (round-trip) and carry `Length` = whole frame.
func TestCastelResponseFrames(t *testing.T) {
	id := testIMEI + "ABCDE"

	login := buildCastelResponse(4, id, castelMsgLoginResponse, castelLoginResponsePayload())
	if len(login) != 41 { // 4 header + 1 version + 20 id + 2 type + 10 payload + 2 CRC + 2 footer
		t.Fatalf("login response length = %d, want 41", len(login))
	}
	if got := int(binary.LittleEndian.Uint16(login[2:4])); got != len(login) {
		t.Fatalf("login response Length field = %d, want %d (whole frame)", got, len(login))
	}
	if login[len(login)-2] != 0x0D || login[len(login)-1] != 0x0A {
		t.Fatalf("login response footer = % x, want 0d 0a", login[len(login)-2:])
	}
	if want := binary.BigEndian.Uint16(login[len(login)-4 : len(login)-2]); navigilCRC16(login[:len(login)-4]) != want {
		t.Fatal("login response CRC does not match the frame content")
	}

	f, err := readCastelFrame(bufReader(login))
	if err != nil {
		t.Fatalf("readCastelFrame(login response): %v", err)
	}
	if f.Type != castelMsgLoginResponse || f.ID != id || len(f.Body) != 10 {
		t.Fatalf("login response round-trip = %+v body=% x", f, f.Body)
	}
	if got := binary.BigEndian.Uint32(f.Body[6:10]); got == 0 {
		t.Fatal("login response carries no server time")
	}

	hb := buildCastelResponse(4, id, castelMsgHeartbeatResponse, nil)
	if len(hb) != 31 {
		t.Fatalf("heartbeat response length = %d, want 31", len(hb))
	}
	f, err = readCastelFrame(bufReader(hb))
	if err != nil {
		t.Fatalf("readCastelFrame(heartbeat response): %v", err)
	}
	if f.Type != castelMsgHeartbeatResponse || len(f.Body) != 0 {
		t.Fatalf("heartbeat response round-trip = %+v", f)
	}
}
