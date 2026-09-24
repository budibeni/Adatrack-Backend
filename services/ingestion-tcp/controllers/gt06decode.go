package controllers

import (
	"encoding/binary"
	"math"
	"strconv"
	"strings"
	"time"

	"adatrack_gps/ingestion-tcp/models"
)

// decodeLatLon converts the raw GT06 coordinate integer to decimal degrees.
func decodeLatLon(raw uint32) float64 { return float64(raw) / models.ConvFactor }

// ParseCourseStatus decodes the 2-byte Course & Status field:
// course (low 10 bits), GPS fix, east longitude, north latitude.
func ParseCourseStatus(b0, b1 byte) (heading int16, fix, east, north bool) {
	word := uint16(b0)<<8 | uint16(b1)
	heading = int16(word & models.StatBitCourse10Mask)
	fix = word&models.StatBitPositioned != 0
	east = word&models.StatBitEastLon == 0 // bit3: 0 = East, 1 = West
	north = word&models.StatBitNorthLat != 0
	return
}

// gpsBlock is the decoded 18-byte GPS block shared by position/alarm packets.
type gpsBlock struct {
	Timestamp  int64
	Satellites uint8
	RawLat     uint32
	RawLon     uint32
	Speed      float64
	Heading    int16
	Fix        bool
	East       bool
	North      bool
}

// parseGPSBlock decodes the 18-byte GPS information block:
//
//	[0..5]   Date Time (YY MM DD HH MM SS)
//	[6]      Satellites (low nibble)
//	[7..10]  Latitude (deg*60+min)*30000
//	[11..14] Longitude (deg*60+min)*30000
//	[15]     Speed (knots)
//	[16..17] Course & Status
func parseGPSBlock(data []byte) (gpsBlock, bool) {
	var g gpsBlock
	if len(data) < 18 {
		return g, false
	}
	ts, ok := ParseTime(data[0:6])
	if !ok {
		return g, false
	}
	g.Timestamp = ts.Unix()
	g.Satellites = data[6] & 0x0f
	g.RawLat = binary.BigEndian.Uint32(data[7:11])
	g.RawLon = binary.BigEndian.Uint32(data[11:15])
	g.Speed = float64(data[15]) * 1.852 // knots → km/h
	g.Heading, g.Fix, g.East, g.North = ParseCourseStatus(data[16], data[17])
	return g, true
}

// applyGPSTo fills a TelemetryMessage from the GPS block (hemisphere signs).
func applyGPSTo(t *models.TelemetryMessage, g gpsBlock) {
	t.Timestamp = g.Timestamp
	t.Satellites = g.Satellites
	t.Lat = decodeLatLon(g.RawLat)
	t.Lon = decodeLatLon(g.RawLon)
	if !g.North {
		t.Lat = -t.Lat
	}
	if !g.East {
		t.Lon = -t.Lon
	}
	t.Speed = g.Speed
	t.Heading = g.Heading
	t.Fix = g.Fix
}

// ParsePosition decodes a position packet (protocol 0x22 UTC / 0x12 legacy).
// Beyond the 18-byte GPS block the v3.1 layout continues with:
//
//	[18..19] MCC  [20] MNC  [21..22] LAC  [23..25] CellID
//	[26] ACC  [27] UploadMode  [28] GPSRealTime  [29..32] Mileage
func ParsePosition(data []byte) (models.TelemetryMessage, bool) {
	var t models.TelemetryMessage
	g, ok := parseGPSBlock(data)
	if !ok {
		return t, false
	}
	applyGPSTo(&t, g)
	if len(data) >= 27 {
		// ACC low 0x00 / high 0x01 (v3.1 §3.1). The byte is always present in
		// this frame type, so the value is a real device reading (B6).
		t.ACC = models.BoolPtr(data[26] == 1)
	}
	if len(data) >= 33 {
		t.Mileage = binary.BigEndian.Uint32(data[29:33])
	}
	return t, true
}

// ParseAlarm decodes an alarm packet (0x26 / 0x27 HVT): the GPS block is
// identical to a position packet, followed by LBS + terminal information:
//
//	[18] LBS length  [19..20] MCC  [21] MNC  [22..23] LAC  [24..26] CellID
//	[27] Terminal information (bit1 = ACC)  [28] Voltage  [29] GSM  [30..31] Alarm
func ParseAlarm(data []byte) (models.TelemetryMessage, bool) {
	var t models.TelemetryMessage
	g, ok := parseGPSBlock(data)
	if !ok {
		return t, false
	}
	applyGPSTo(&t, g)

	idx := 18
	if len(data) >= idx+9 { // LBS block: length + MCC + MNC + LAC + CellID
		idx += 9
	}
	if len(data) >= idx+1 {
		// Terminal information bit1: ACC high/low (a real device bit — B6).
		t.ACC = models.BoolPtr(data[idx]&0x02 != 0)
		idx++
	}
	if len(data) >= idx+1 {
		t.Battery = data[idx] // voltage level 0..6
		idx++
	}
	if len(data) >= idx+1 {
		t.GsmSignal = data[idx] // GSM signal 0..4
		idx++
	}
	if len(data) >= idx+2 {
		t.AlarmCode = data[idx] // Alarm/Language byte 1 = alarm reason
	}
	return t, true
}

// ParseLBSAlarm decodes a non-GPS alarm packet (0x19): LBS-only positioning has
// no UTC date and no coordinates, so the timestamp is "now" and lat/lon stay 0.
// AlarmLBS marks the message so worker-alert turns it into a SOS/LBS alert (B3).
func ParseLBSAlarm(_ []byte, imei, company string, vehicleID int64) models.TelemetryMessage {
	return models.TelemetryMessage{
		IMEI:        imei,
		CompanyCode: company,
		VehicleID:   vehicleID,
		AlarmLBS:    true,
		Timestamp:   time.Now().Unix(),
	}
}

// ParseInfoTransmit decodes 0x94 subtype 0x0D: type + time(6) +
// ASCII sensor sentence + serial(2). Other subtypes remain unsupported.
// The v3.1 sensor reports centimetres, NOT percent or litres; keep the raw
// height separate until a calibrated conversion is configured.
func ParseInfoTransmit(data []byte) (models.TelemetryMessage, bool) {
	var t models.TelemetryMessage
	if len(data) < 10 || data[0] != 0x0D {
		return t, false
	}
	ts, ok := ParseTime(data[1:7])
	if !ok {
		return t, false
	}
	fields := strings.Split(string(data[7:len(data)-2]), ",")
	if len(fields) != 10 || (fields[0] != "!AIOIL" && fields[0] != "!AILOIL") {
		return t, false
	}
	if _, err := strconv.ParseUint(fields[1], 10, 8); err != nil {
		return t, false
	}
	height, err := strconv.ParseFloat(fields[2], 64)
	if err != nil || math.IsNaN(height) || math.IsInf(height, 0) || height < 0 {
		return t, false
	}
	temp, err := strconv.ParseFloat(fields[3], 64)
	if err != nil || math.IsNaN(temp) || math.IsInf(temp, 0) {
		return t, false
	}
	t.FuelHeightCM, t.FuelTempC = &height, &temp
	t.Timestamp = ts.Unix()
	return t, true
}
