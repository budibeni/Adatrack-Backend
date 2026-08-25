package controllers

import (
	"encoding/binary"
	"encoding/json"
	"time"

	"ajb_gps/ingestion-tcp/models"
)

// Packet is a decoded GT06 frame. Data holds the raw Information Content
// bytes (after the Protocol Number byte).
type Packet struct {
	Protocol byte
	Data     []byte
	// Serial is the 2-byte Information Serial Number (big-endian). Populated
	// when the frame carries one (login/location/alarm/heartbeat/time).
	Serial uint16
}

// ParseLoginIMEI extracts the 15-byte ASCII IMEI from a GT06 login payload.
// Per v1.8.1 §5.1.1.4 / v3.1 §1.1 the login Information Content is the 15-char
// ASCII IMEI (a trailing model-identification/time-zone block may follow and
// is ignored here).
func ParseLoginIMEI(data []byte) string {
	if len(data) > 15 {
		data = data[:15]
	}
	return string(data)
}

// BcdToInt converts a binary-coded-decimal byte to an integer.
func BcdToInt(b byte) int {
	return int(b>>4)*10 + int(b&0x0f)
}

// IntToBcd encodes a decimal value (0..99) as a single binary-coded-decimal byte.
func IntToBcd(v int) byte {
	h := v / 10
	l := v % 10
	return byte(h<<4 | l)
}

// decodeLatLon converts a raw GT06 latitude/longitude integer to decimal degrees.
//
// Per v1.8.1 §5.2.1.6 the raw integer = (deg*60 + min) * 30000, i.e.
//
//	value/30000 = deg*60 + min  (minutes, float)
//	degrees     = (deg*60 + min)/60
//
// So decimal degrees = value / (30000 * 60) = value / 1_800_000, which matches
// v3.1 §3.1 "Latitude: convert to decimal and divide 1800000".
func decodeLatLon(raw uint32) float64 {
	return float64(raw) / 1_800_000.0
}

// ParseCourseStatus decodes the 2-byte Course & Status field (v1.8.1 §5.2.1.9 /
// v3.1 §3.1-i). Returns (course 0..360, fix bool, eastLon bool, northLat bool).
func ParseCourseStatus(b0, b1 byte) (heading int16, fix, east, north bool) {
	word := uint16(b0)<<8 | uint16(b1)
	heading = int16(word & models.StatBitCourse10Mask)
	fix = word&models.StatBitPositioned != 0
	east = word&models.StatBitEastLon == 0 // bit3: 0 East / 1 West
	north = word&models.StatBitNorthLat != 0
	return
}

// dateBCD toggles GT06 date-time decoding. Per the vendor docs the 6-byte Date
// Time block is encoded as one byte per field whose value equals the decimal
// digit (plain hex: 0x17 = 23, 0x32 = 50) — See v1.8.1 §5.2.1.4 / v3.1 §3.1,
// where both documented packet examples only decode correctly as plain hex.
// Some real-world Concox firmwares instead pack the digits as BCD (0x17 = 17).
// We default to the documented plain-hex encoding and expose a toggle.
var dateBCD bool

// SetDateEncoding selects BCD vs plain-hex date decoding (GT06_DATE_BCD env).
func SetDateEncoding(bcd bool) { dateBCD = bcd }

// EncodeTime6 encodes a UTC time into the 6-byte Date Time block using the
// same encoding selected for decoding (plain-hex by default).
func EncodeTime6(t time.Time) []byte {
	if dateBCD {
		return []byte{
			IntToBcd(t.Year() - 2000), IntToBcd(int(t.Month())), IntToBcd(t.Day()),
			IntToBcd(t.Hour()), IntToBcd(t.Minute()), IntToBcd(t.Second()),
		}
	}
	return []byte{
		byte(t.Year() - 2000), byte(t.Month()), byte(t.Day()),
		byte(t.Hour()), byte(t.Minute()), byte(t.Second()),
	}
}

// ParseTime decodes the 6-byte Date Time block (v1.8.1 §5.2.1.4).
// Year is stored as 2-digit byte (e.g. 0x0A -> 2010). Returns UTC time.Time.
func ParseTime(d []byte) (time.Time, bool) {
	if len(d) < 6 {
		return time.Time{}, false
	}
	var year, month, day, hour, min, sec int
	if dateBCD {
		year = 2000 + BcdToInt(d[0])
		month = BcdToInt(d[1])
		day = BcdToInt(d[2])
		hour = BcdToInt(d[3])
		min = BcdToInt(d[4])
		sec = BcdToInt(d[5])
	} else {
		year = 2000 + int(d[0])
		month = int(d[1])
		day = int(d[2])
		hour = int(d[3])
		min = int(d[4])
		sec = int(d[5])
	}
	// Reject obviously invalid fields to avoid corrupt timestamps.
	if month < 1 || month > 12 || day < 1 || day > 31 || hour > 23 || min > 59 || sec > 59 {
		return time.Time{}, false
	}
	return time.Date(year, time.Month(month), day, hour, min, sec, 0, time.UTC), true
}

// TelemetryGPS is the decoded shared GPS block, plus the network/LBS fields
// that follow in location/alarm packets.
type TelemetryGPS struct {
	Timestamp   int64
	Satellites  uint8
	RawLat      uint32
	RawLon      uint32
	Speed       float64
	Heading     int16
	Fix         bool
	East, North bool
}

// parseGPSBlock decodes the shared GPS block that leads every location/alarm
// packet: Date(6) Sats(1) Lat(4) Lon(4) Speed(1) Course(2).
// The Satellites byte encodes GPS-info-length (high nibble) and satellite count
// (low nibble) — v1.8.1 §5.2.1.5 (0xCB -> 12 len / 11 sats); v3.1 §3.1 uses the
// same nibble layout.
func parseGPSBlock(d []byte) (TelemetryGPS, bool) {
	var g TelemetryGPS
	if len(d) < 18 {
		return g, false
	}
	ts, ok := ParseTime(d[0:6])
	if !ok {
		return g, false
	}
	g.Timestamp = ts.Unix()
	g.Satellites = d[6] & 0x0F // low nibble = sat count (plain hex, not BCD)
	g.RawLat = binary.BigEndian.Uint32(d[7:11])
	g.RawLon = binary.BigEndian.Uint32(d[11:15])
	g.Speed = float64(d[15])
	course, fix, east, north := ParseCourseStatus(d[16], d[17])
	g.Heading = course
	g.Fix = fix
	g.East = east
	g.North = north
	return g, true
}

// ParsePosition decodes a GT06 location packet Information Content (protocol
// 0x12 / 0x22). Returns a TelemetryMessage pre-filled with location fields;
// callers set IMEI/CompanyCode/VehicleID afterwards.
//
// Common prefix (identical in v1.8.1 §5.2.1 and v3.1 §3.1):
//
//	Date(6) Sats(1) Lat(4) Lon(4) Speed(1) Course(2) MCC(2) MNC(1) LAC(2) CellID(3)
//
// v3.1 also appends ACC(1) UploadMode(1) GPSRealTime(1) Mileage(4) after
// CellID. We tolerate both, reading the extended fields defensively when the
// payload is long enough.
func ParsePosition(data []byte) (models.TelemetryMessage, bool) {
	var t models.TelemetryMessage
	g, ok := parseGPSBlock(data)
	if !ok {
		return t, false
	}
	t.Timestamp = g.Timestamp
	t.Satellites = g.Satellites
	t.Lat = decodeLatLon(g.RawLat)
	t.Lon = decodeLatLon(g.RawLon)
	t.Speed = g.Speed
	t.Heading = g.Heading
	t.Fix = g.Fix
	// Apply sign from course/status direction bits (v1.8.1 §5.2.1.9).
	if !g.North {
		t.Lat = -t.Lat
	}
	if !g.East {
		t.Lon = -t.Lon
	}
	// Extended v3.1 tail after CellID (offsets relative to `data`):
	//   [0..17]  GPS block
	//   [18..19] MCC, [20] MNC, [21..22] LAC, [23..25] CellID
	//   [26] ACC, [27] UploadMode, [28] GPSRealTime, [29..32] Mileage
	if len(data) >= 27 {
		t.ACC = data[26] == 1 // ACC low 00 / high 01 (v3.1 §3.1)
	}
	if len(data) >= 33 {
		t.Mileage = binary.BigEndian.Uint32(data[29:33])
	}
	return t, true
}

// ParseLBSAlarm decodes a non-GPS alarm packet (protocol 0x19) — LBS-only
// position derived from tower signals. Produce a timestamp from "now" (LBS
// packets carry no UTC date) and leave lat/lon at 0 (approximate location).
func ParseLBSAlarm(data []byte, imei string, company string, vehicleID int64) (models.TelemetryMessage, bool) {
	var t models.TelemetryMessage
	t.IMEI = imei
	t.CompanyCode = company
	t.VehicleID = vehicleID
	t.Timestamp = time.Now().Unix()
	return t, true
}

// ParseAlarm decodes a GT06 alarm packet Information Content (protocol
// 0x26 / 0x27). The GPS block is identical to location packets; trailing
// network + status fields carry battery, GSM signal and the alarm code
// (v3.1 §6 Alarm Packet / §6-i Terminal Information).
func ParseAlarm(data []byte) (models.TelemetryMessage, bool) {
	var t models.TelemetryMessage
	g, ok := parseGPSBlock(data)
	if !ok {
		return t, false
	}
	t.Timestamp = g.Timestamp
	t.Satellites = g.Satellites
	t.Lat = decodeLatLon(g.RawLat)
	t.Lon = decodeLatLon(g.RawLon)
	t.Speed = g.Speed
	t.Heading = g.Heading
	t.Fix = g.Fix
	if !g.North {
		t.Lat = -t.Lat
	}
	if !g.East {
		t.Lon = -t.Lon
	}
	// Alarm payload after the 18-byte GPS prefix (v3.1 §6):
	//   LBSLen(1) MCC(2) MNC(1) LAC(2) CellID(3)  => 9 bytes
	//   TerminalInfo(1) Voltage(1) GSM(1) Alarm/Language(2)
	idx := 18
	if len(data) >= idx+9 { // LBS block: LBSLen+MCC+MNC+LAC+CellID
		idx += 9
	}
	// Terminal Information byte (ACC bit1).
	if len(data) >= idx+1 {
		ti := data[idx]
		t.ACC = ti&0x02 != 0 // Bit1: ACC high/low
		idx++
	}
	if len(data) >= idx+1 {
		t.Battery = data[idx] // Voltage Level 0..6
		idx++
	}
	if len(data) >= idx+1 {
		t.GsmSignal = data[idx] // GSM 0..4
		idx++
	}
	// Alarm/Language 2 bytes: byte0 = alarm code.
	if len(data) >= idx+2 {
		t.AlarmCode = data[idx]
	}
	return t, true
}

// publishTelemetry publishes a parsed message to telemetry.raw.<IMEI>.
// Records nats_publish_duration_ms / nats_publish_errors_total per company.
func publishTelemetry(t models.TelemetryMessage) error {
	data, err := json.Marshal(t)
	if err != nil {
		return err
	}
	company := t.CompanyCode
	if company == "" {
		company = "default"
	}
	subject := natsCli.Subject("raw", t.IMEI)
	start := time.Now()
	err = natsCli.Publish(subject, data)
	natsPublishDuration.WithLabelValues(company, natsCli.Subject("raw")).Observe(float64(time.Since(start).Microseconds()) / 1000.0)
	if err != nil {
		natsPublishErrors.WithLabelValues(company).Inc()
		return err
	}
	return nil
}
