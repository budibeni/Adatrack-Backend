package controllers

import (
	"encoding/binary"
	"time"

	"ajb_gps/ingestion-tcp/models"
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
		t.ACC = data[26] == 1 // ACC low 0x00 / high 0x01 (v3.1 §3.1)
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
		t.ACC = data[idx]&0x02 != 0 // Terminal information bit1: ACC high/low
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
// no UTC date and no coordinates, so the timestamp is "now" and lat/lon stay 0
// (B3 turns these into OFFLINE/LBS alerts).
func ParseLBSAlarm(_ []byte, imei, company string, vehicleID int64) models.TelemetryMessage {
	return models.TelemetryMessage{
		IMEI:        imei,
		CompanyCode: company,
		VehicleID:   vehicleID,
		Timestamp:   time.Now().Unix(),
	}
}

// ParseInfoTransmit decodes the information transmission packet (0x94).
// Information type 0x0D carries 1-Wire/fuel sensor values (v3.1 §10.1, B5a);
// other types are reported as unsupported so the caller can log them.
func ParseInfoTransmit(data []byte) (models.TelemetryMessage, bool) {
	var t models.TelemetryMessage
	if len(data) < 2 {
		return t, false
	}
	if data[0] != 0x0D {
		return t, false
	}
	// Payload: info type (1) + sensor count (1) + value bytes.
	level := float64(binary.BigEndian.Uint16(data[len(data)-2:]))
	t.FuelLevel = &level
	t.Timestamp = time.Now().Unix()
	return t, true
}
