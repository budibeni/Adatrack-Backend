package controllers

import (
	"encoding/binary"
	"math"
	"testing"
	"time"

	"ajb_gps/ingestion-tcp/models"
)

func TestDecodeLatLon(t *testing.T) {
	// (deg*60 + min) * 30000 integer method (v1.8.1 §5.2.1.6).
	// 22°32.7658' -> (22*60+32.7658)*30000 = 40582974 -> approx 22.545...°
	// degrees = 40582974 / 1800000 = 22.5460967...
	got := decodeLatLon(40582974)
	want := 22.546096666666667
	if math.Abs(got-want) > 1e-6 {
		t.Errorf("decodeLatLon(40582974) = %v, want %v", got, want)
	}
	// Common known value: 106°, 0' -> raw = 106*60*30000 = 190800000 -> 106.0
	if g := decodeLatLon(106 * 60 * 30000); math.Abs(g-106.0) > 1e-9 {
		t.Errorf("decodeLatLon(190800000) = %v, want 106.0", g)
	}
}

func TestParseCourseStatus(t *testing.T) {
	// b0=0x15 b1=0x4C from the spec example (real-time, positioned,
	// east longitude, north latitude, course 332°, GPS tracking on).
	heading, fix, east, north := ParseCourseStatus(0x15, 0x4C)
	if heading != 332 {
		t.Errorf("heading = %d, want 332", heading)
	}
	if !fix {
		t.Error("fix should be true (bit4 set)")
	}
	if !east {
		t.Error("east should be true (bit3 clear)")
	}
	if !north {
		t.Error("north should be true (bit2 set)")
	}
}

func TestParseTime(t *testing.T) {
	// The unique example the vendor doc explicitly decodes (v1.8.1 §5.2.1.4):
	//   0x0A 0x03 0x17 0x0F 0x32 0x17 -> 2010-03-23 15:50:23 UTC
	// (plain-hex encoding: byte value == decimal digit).
	ts, ok := ParseTime([]byte{0x0A, 0x03, 0x17, 0x0F, 0x32, 0x17})
	if !ok {
		t.Fatal("ParseTime returned false for valid data")
	}
	want := time.Date(2010, 3, 23, 15, 50, 23, 0, time.UTC)
	if !ts.Equal(want) {
		t.Errorf("ParseTime = %v, want %v", ts, want)
	}
	// Invalid month must be rejected.
	if _, ok := ParseTime([]byte{0x0A, 0x13, 0x17, 0x0F, 0x32, 0x17}); ok {
		t.Error("ParseTime should reject invalid month 13")
	}
}

// buildGPSBlock builds the 18-byte shared GPS block: Date(6) Sats(1) Lat(4)
// Lon(4) Speed(1) Course(2).
func buildGPSBlock(tm time.Time, sats byte, rawLat, rawLon uint32, speed byte, course uint16, north, east bool) []byte {
	d := make([]byte, 0, 18)
	// Date bytes use the documented plain-hex encoding (byte value == decimal).
	d = append(d, byte(tm.Year()-2000), byte(tm.Month()), byte(tm.Day()),
		byte(tm.Hour()), byte(tm.Minute()), byte(tm.Second()))
	d = append(d, byte(sats)<<4|sats) // info-len high nibble + sat count low nibble
	var latRaw, lonRaw [4]byte
	binary.BigEndian.PutUint32(latRaw[:], rawLat)
	binary.BigEndian.PutUint32(lonRaw[:], rawLon)
	d = append(d, latRaw[:]...)
	d = append(d, lonRaw[:]...)
	d = append(d, speed)
	// Course & status word.
	var cs uint16
	if north {
		cs |= models.StatBitNorthLat
	}
	if !east {
		cs |= models.StatBitEastLon
	}
	cs |= models.StatBitPositioned // fix
	cs |= course & models.StatBitCourse10Mask
	d = append(d, byte(cs>>8), byte(cs))
	return d
}

// buildLocationContent builds the Information Content of a GT06 location packet
// using the documented layout: GPS(18) MCC(2) MNC(1) LAC(2) CellID(3)
// [ACC(1) UploadMode(1) GPSRealTime(1) Mileage(4)].
func buildLocationContent(tm time.Time, sats byte, rawLat, rawLon uint32, speed byte, course uint16, north, east bool, withExt bool, acc bool, mileage uint32) []byte {
	d := buildGPSBlock(tm, sats, rawLat, rawLon, speed, course, north, east)
	// LBS tail: MCC(2) MNC(1) LAC(2) CellID(3) = 8 bytes -> ACC at offset 26.
	d = append(d, 0x01, 0x00, 0x00, 0x28, 0x7D, 0x00, 0x1F, 0xB8)
	if withExt {
		accB := byte(0)
		if acc {
			accB = 1
		}
		d = append(d, accB, 0x00, 0x00) // ACC, UploadMode, GPSRealTime
		var m [4]byte
		binary.BigEndian.PutUint32(m[:], mileage)
		d = append(d, m[:]...)
	}
	return d
}

func TestParsePosition(t *testing.T) {
	tm := time.Date(2026, 8, 16, 10, 30, 0, 0, time.UTC)
	// lat = -6.2088, lon = 106.8456
	rawLat := uint32(math.Round(6.2088 * 1_800_000))
	rawLon := uint32(math.Round(106.8456 * 1_800_000))
	data := buildLocationContent(tm, 11, rawLat, rawLon, 45, 332, false, true, true, true, 123456)

	msg, ok := ParsePosition(data)
	if !ok {
		t.Fatal("ParsePosition returned false for valid data")
	}
	if math.Abs(msg.Lat-(-6.2088)) > 1e-5 {
		t.Errorf("Lat = %v, want ~-6.2088", msg.Lat)
	}
	if math.Abs(msg.Lon-106.8456) > 1e-5 {
		t.Errorf("Lon = %v, want ~106.8456", msg.Lon)
	}
	if msg.Speed != 45 {
		t.Errorf("Speed = %v, want 45", msg.Speed)
	}
	if msg.Heading != 332 {
		t.Errorf("Heading = %d, want 332", msg.Heading)
	}
	if msg.Satellites != 11 {
		t.Errorf("Satellites = %d, want 11", msg.Satellites)
	}
	if msg.Timestamp != tm.Unix() {
		t.Errorf("Timestamp = %d, want %d", msg.Timestamp, tm.Unix())
	}
	if !msg.ACC {
		t.Error("ACC should be true")
	}
	if msg.Mileage != 123456 {
		t.Errorf("Mileage = %d, want 123456", msg.Mileage)
	}
}

func TestParsePositionShortData(t *testing.T) {
	if _, ok := ParsePosition(make([]byte, 15)); ok {
		t.Error("ParsePosition should return false for short data (<18)")
	}
}

func TestParseAlarm(t *testing.T) {
	tm := time.Date(2026, 8, 16, 10, 30, 0, 0, time.UTC)
	rawLat := uint32(math.Round(6.2088 * 1_800_000))
	rawLon := uint32(math.Round(106.8456 * 1_800_000))
	// Alarm packet: GPS block(18) + LBSLen(1)+MCC(2)+MNC(1)+LAC(2)+CellID(3) +
	// TerminalInfo(1)+Voltage(1)+GSM(1)+Alarm/Language(2). (v3.1 §6)
	data := buildGPSBlock(tm, 8, rawLat, rawLon, 30, 180, false, true)
	data = append(data,
		0x09,       // LBS length
		0x01, 0xCC, // MCC 460
		0x00,       // MNC
		0x28, 0x7D, // LAC
		0x00, 0x1F, 0xB8, // CellID
		0x02,       // TerminalInfo: ACC high (bit1)
		0x05,       // Voltage level 5
		0x03,       // GSM signal 3
		0x00, 0x02) // Alarm/Language: alarm=normal, lang=English

	msg, ok := ParseAlarm(data)
	if !ok {
		t.Fatal("ParseAlarm returned false for valid data")
	}
	if math.Abs(msg.Lat-(-6.2088)) > 1e-5 {
		t.Errorf("Lat = %v, want ~-6.2088", msg.Lat)
	}
	if !msg.ACC {
		t.Error("ACC should be true (TerminalInfo bit1)")
	}
	if msg.Battery != 5 {
		t.Errorf("Battery = %d, want 5", msg.Battery)
	}
	if msg.GsmSignal != 3 {
		t.Errorf("GsmSignal = %d, want 3", msg.GsmSignal)
	}
	if msg.AlarmCode != 0 {
		t.Errorf("AlarmCode = %d, want 0", msg.AlarmCode)
	}
}
