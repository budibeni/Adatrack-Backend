package controllers

import (
	"testing"
	"time"
)

// TestParseLoginIMEI checks the 15-character ASCII IMEI extraction (trailing
// model/timezone bytes are ignored).
func TestParseLoginIMEI(t *testing.T) {
	data := []byte("864201040512345\x00\x01")
	if got := ParseLoginIMEI(data); got != "864201040512345" {
		t.Errorf("ParseLoginIMEI = %q, want 864201040512345", got)
	}
}

// TestParseTimePlainHexAndBCD verifies both date encodings: the default plain-hex
// (vendor examples) and the BCD toggle used by some firmwares.
func TestParseTimePlainHexAndBCD(t *testing.T) {
	defer SetDateEncoding(false)

	SetDateEncoding(false)
	// Plain hex: 0x17 = 23 (year 2023), 0x0A = October.
	ts, ok := ParseTime([]byte{0x17, 0x0A, 0x05, 0x0E, 0x1E, 0x28})
	if !ok {
		t.Fatal("plain-hex ParseTime returned ok=false")
	}
	want := time.Date(2023, 10, 5, 14, 30, 40, 0, time.UTC)
	if !ts.Equal(want) {
		t.Errorf("plain-hex ParseTime = %s, want %s", ts, want)
	}

	SetDateEncoding(true)
	// BCD: 0x23 = 23, 0x10 = 10, 0x05 = 5, 0x14 = 14, 0x30 = 30, 0x40 = 40.
	ts, ok = ParseTime([]byte{0x23, 0x10, 0x05, 0x14, 0x30, 0x40})
	if !ok {
		t.Fatal("BCD ParseTime returned ok=false")
	}
	if !ts.Equal(want) {
		t.Errorf("BCD ParseTime = %s, want %s", ts, want)
	}

	// Invalid values are rejected instead of producing a nonsense timestamp.
	if _, ok := ParseTime([]byte{0x17, 0x13, 0x05, 0x0E, 0x1E, 0x28}); ok {
		t.Error("ParseTime accepted month 0x13 (19) — must reject")
	}
}

// TestParseCourseStatus decodes the hemisphere/fix bits and the 10-bit course.
func TestParseCourseStatus(t *testing.T) {
	heading, fix, east, north := ParseCourseStatus(0x04, 0x28)
	if !north {
		t.Error("expected north=true for bit2 (0x0400)")
	}
	if !east {
		t.Error("expected east=true when the west bit (0x0800) is clear")
	}
	if fix {
		t.Error("expected fix=false when the positioned bit (0x1000) is clear")
	}
	if heading != 40 { // 0x0428 & 0x03FF = 0x28
		t.Errorf("heading = %d, want 40", heading)
	}

	// 0x18 in the high byte = positioned (0x1000) + west (0x0800).
	_, fix, east, _ = ParseCourseStatus(0x18, 0x01)
	if !fix {
		t.Error("expected fix=true for bit4 (0x1000)")
	}
	if east {
		t.Error("expected east=false when the west bit (0x0800) is set")
	}
}

// TestParsePositionRoundTrip decodes a synthesized position payload including the
// southern/western hemispheres (negative coordinates).
func TestParsePositionRoundTrip(t *testing.T) {
	when := time.Date(2026, 9, 15, 3, 30, 0, 0, time.UTC)
	block := buildGPSBlock(when, 0x0A, -6.2088, 106.8456, 23, false, true)

	payload := append([]byte{}, block...)
	payload = append(payload, 0x01, 0xF4, 0x01, 0x00, 0x01, 0x00, 0x01, 0x23) // LBS tail
	payload = append(payload, 0x01)                                           // ACC ON
	payload = append(payload, 0x00, 0x00, 0x00, 0x00, 0x00, 0x64)             // upload/gps/mileage

	msg, ok := ParsePosition(payload)
	if !ok {
		t.Fatal("ParsePosition returned ok=false")
	}
	if diff := absFloat(msg.Lat - (-6.2088)); diff > 0.0001 {
		t.Errorf("lat = %.6f, want -6.2088", msg.Lat)
	}
	if diff := absFloat(msg.Lon - 106.8456); diff > 0.0001 {
		t.Errorf("lon = %.6f, want 106.8456", msg.Lon)
	}
	if diff := absFloat(msg.Speed - 23*1.852); diff > 0.001 {
		t.Errorf("speed = %.3f km/h, want %.3f (knots → km/h)", msg.Speed, 23*1.852)
	}
	if msg.Satellites != 0x0A {
		t.Errorf("satellites = %d, want 10", msg.Satellites)
	}
	if msg.Timestamp != when.Unix() {
		t.Errorf("timestamp = %d, want %d", msg.Timestamp, when.Unix())
	}
	if !msg.Fix {
		t.Error("expected fix=true when the positioned bit is set")
	}
	if msg.ACC == nil || !*msg.ACC {
		t.Error("expected ACC=true from the v3.1 tail byte")
	}
	if msg.Mileage != 0x64 {
		t.Errorf("mileage = %d, want 100", msg.Mileage)
	}
}

// TestParsePositionShortPayload rejects truncated payloads.
func TestParsePositionShortPayload(t *testing.T) {
	if _, ok := ParsePosition([]byte{0x17, 0x0A, 0x05}); ok {
		t.Error("ParsePosition accepted a truncated payload")
	}
}

// TestEncodeTime6RoundTrip verifies the time-check reply encoding.
func TestEncodeTime6RoundTrip(t *testing.T) {
	defer SetDateEncoding(false)
	SetDateEncoding(false)

	now := time.Date(2026, 9, 15, 3, 30, 40, 0, time.UTC)
	decoded, ok := ParseTime(EncodeTime6(now))
	if !ok {
		t.Fatal("ParseTime(EncodeTime6(now)) returned ok=false")
	}
	if !decoded.Equal(now) {
		t.Errorf("round trip = %s, want %s", decoded, now)
	}
}
