package controllers

// protocols_test.go — B9 test vectors: sample frame → struct for every protocol
// added in the protocol expansion (Meiligao, Xexun, Suntech, H02, Totem, GT02,
// Navigil, Castel) plus the TK103 validation of the pre-existing provisional
// family. Vectors come from the documented examples in
// docs/docs-device/traccar-reference/ (cross-checked against the upstream
// decoders); the framing builders below mirror the wire layout exactly so the
// reader and the writer are verified against each other.

import (
	"math"
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
	ack := buildNavigilAck(got.Sequence)
	if len(ack) != 20 || ack[4] != byte(navigilMsgAck) {
		t.Fatalf("navigil ack is malformed: % x", ack)
	}
	if _, err := readNavigilHeader(bufReader(make([]byte, 20))); err == nil {
		t.Fatal("readNavigilHeader accepted a zeroed (implausible) header")
	}
}

func TestCastelFrameAndIdentity(t *testing.T) {
	id := testIMEI + "ABCDE" // 20-char ASCII id carrying the IMEI
	body := append([]byte{0x01}, []byte(id)...)
	body = append(body, byte(castelMsgLogin&0xFF), byte(castelMsgLogin>>8))
	body = append(body, 0x00, 0x01)

	frame := []byte{0x40, 0x40, byte(len(body)), byte(len(body) >> 8)}
	frame = append(frame, body...)

	f, err := readCastelFrame(bufReader(frame))
	if err != nil {
		t.Fatalf("readCastelFrame: %v", err)
	}
	if f.Type != castelMsgLogin || f.ID != id {
		t.Fatalf("castel frame = %+v", f)
	}
	if got := castelIMEIPattern.FindString(f.ID); got != testIMEI {
		t.Fatalf("castel IMEI extraction = %q, want %q", got, testIMEI)
	}
	bad := append([]byte{}, frame...)
	bad[0] = 0x41
	if _, err := readCastelFrame(bufReader(bad)); err == nil {
		t.Fatal("readCastelFrame accepted a frame with a bad header")
	}
}
