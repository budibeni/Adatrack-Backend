package models

import "time"

// Protocol identifies the device wire protocol a listener serves. Each
// protocol has its own TCP port (GT06 default :9000; Teltonika & TK103 via
// their own env ports) — avoids fragile header-sniffing between framings.
type Protocol int

const (
	ProtoGT06 Protocol = iota + 1
	ProtoTeltonika
	ProtoTK103
)

func (p Protocol) String() string {
	switch p {
	case ProtoTeltonika:
		return "teltonika"
	case ProtoTK103:
		return "tk103"
	default:
		return "gt06"
	}
}

// GT06/Concox protocol numbers (from docs/docs-device
// GT06_GPS_Tracker_Communication_Protocol_v1.8.1.md §4.3 and
// GPS_Tracker_communication_protocol_v3.1.md §1.1 — digital protocol family).
const (
	// Login message (terminal -> server).
	ProtoLogin = 0x01
	// Position (UTC) 0x22 / legacy non-UTC 0x12 (v1.8.1 §4.3 uses 0x12).
	ProtoPosition  = 0x22
	ProtoPosition2 = 0x12
	// String information packet (server online-command reply, JM01).
	ProtoStringInfo = 0x15
	// Online command reply (general models: JV200/GT300/GT800/MT200).
	ProtoOnlineReply = 0x21
	// Status/heartbeat 0x13 (EG02/EG03 use 0x23).
	ProtoHeartbeat   = 0x13
	ProtoHeartbeatEG = 0x23
	// Location status (compressed/status-only) packet.
	ProtoStatusLoc = 0x20
	// Alarm data (UTC) 0x26 / 0x27 (HVT001 multi-fence) / LBS alarm 0x19.
	ProtoAlarm    = 0x26
	ProtoAlarmHVT = 0x27
	ProtoAlarmLBS = 0x19
	// LBS multiple-bases extension packet.
	ProtoLBSMulti = 0x28
	// WiFi information protocol (Q2/HVT001).
	ProtoWifi = 0x2C
	// Information transmission packet (config/status).
	ProtoInfoTransmit = 0x94
	// Server online command (outbound only).
	ProtoOnlineCommand = 0x80
	// Time check packet (terminal -> server).
	ProtoTimeCheck = 0x8A
	// Speed limit alarm (mobile app query).
	ProtoSpeedAlarm = 0x31
)

// Frame offsets: GT06 framing. v3.1 §1.1 also defines a 2-byte length variant
// starting with 0x79 0x79 (used for online-command replies / large content).
const (
	FrameStartShort = 0x78 // +0x78  => 1-byte Packet Length
	FrameStartLong  = 0x79 // +0x79  => 2-byte Packet Length
	FrameStop0      = 0x0d
	FrameStop1      = 0x0a
)

// GT06 connection/idle policy (FR-1.3, PRD §…).
const (
	IdleTimeout  = 90 * time.Second // idle timeout (PRD FR-1.3)
	OfflineAfter = 3 * time.Minute  // device offline if silent this long (FR-2.2)
)

// Course & Status bit masks. The 2-byte "Course, Status" field is big-endian:
// BYTE_1 is the first (high) byte, BYTE_2 the second (low) byte (v1.8.1
// §5.2.1.9 / v3.1 §3.1-i). The running direction occupies the low 10 bits of
// the 16-bit word; the hemisphere/fix flags sit in BYTE_1's upper bits.
const (
	StatBitCourse10Mask uint16 = 0x03FF // low 10 bits = course (0..360)
	StatBitPositioned   uint16 = 0x1000 // BYTE_1 bit4: GPS has been positioned
	StatBitEastLon      uint16 = 0x0800 // BYTE_1 bit3: 0 East / 1 West
	StatBitNorthLat     uint16 = 0x0400 // BYTE_1 bit2: 0 South / 1 North
)

// ConvFactor is the divisor for decoding latitude/longitude. Per v1.8.1
// §5.2.1.6 the raw integer = (deg*60 + min_decimal) * 30000, so we divide by
// 30000 then MM=deg*60+min. v3.1 §3.1 states "divide 1800000" which yields the
// same degree result (see decodeLatLon in parser.go for derivation).
const ConvFactor = 30000.0

// TelemetryMessage is the canonical payload published to telemetry.raw.<IMEI>.
// CompanyCode & VehicleID come from tenant resolution (master.vehicle_imei_map)
// and are added before publish (FR-1.4, PRD multi-tenant).
//
// Additional fields beyond the base GT06 location block:
//   - Battery      : terminal built-in battery voltage level 0x00..0x06 (v3.1 §6-i)
//   - GsmSignal    : GSM signal strength 0x00..0x04 (v3.1 §6-i)
//   - ACC          : ignition state (0 off / 1 on), v3.1 location packet "ACC"
//   - Mileage      : accumulated odometer (m), when present
//   - AlarmCode    : alarm reason byte (v3.1 alarm "Alarm/Language" byte 1)
//   - Fix          : GPS fix valid flag (course/status bit4)
type TelemetryMessage struct {
	IMEI        string  `json:"imei"`
	CompanyCode string  `json:"company_code,omitempty"`
	VehicleID   int64   `json:"vehicle_id,omitempty"`
	Lat         float64 `json:"lat"`
	Lon         float64 `json:"lon"`
	Speed       float64 `json:"speed"`
	Heading     int16   `json:"heading,omitempty"`
	Satellites  uint8   `json:"satellites,omitempty"`
	HDOP        float64 `json:"hdop,omitempty"`
	Battery     uint8   `json:"battery_level,omitempty"`
	GsmSignal   uint8   `json:"gsm_signal,omitempty"`
	ACC         bool    `json:"acc,omitempty"`
	Mileage     uint32  `json:"mileage,omitempty"`
	AlarmCode   uint8   `json:"alarm_code,omitempty"`
	Fix         bool    `json:"fix,omitempty"`
	Timestamp   int64   `json:"timestamp"`

	// --- B5a: Fuel sensor (PRD v1.3.0 Module 7) ---
	// Pointer + omitempty: field yang tidak hadir (absen ≠ nol) tidak muncul di JSON.
	// FuelLevel = liquid level measurement value (cm) dari sensor AIOIL.
	// FuelVolume = fuel volume (L) — butuh kalibrasi tangki (fuel_configs / tank_profile).
	// FuelTempC = fuel temperature (°C).
	FuelLevel  *float64 `json:"fuel_level,omitempty"`
	FuelVolume *float64 `json:"fuel_volume,omitempty"`
	FuelTempC  *float64 `json:"fuel_temp_c,omitempty"`
}
