// Package models holds the ingestion wire-protocol constants and the canonical
// telemetry payload published to NATS (PRD Module 1 / FR-1.2).
package models

import "time"

// Protocol identifies the device wire protocol served by a listener. Each
// protocol gets its OWN port (GT06 default, Teltonika own reference) so no
// fragile header sniffing between framings is needed (PRD Module 1c).
type Protocol int

const (
	ProtoGT06 Protocol = iota + 1
	ProtoTeltonika
)

// String returns the metric/log label of a protocol.
func (p Protocol) String() string {
	if p == ProtoTeltonika {
		return "teltonika"
	}
	return "gt06"
}

// GT06/Concox protocol numbers (docs/docs-device
// GT06_GPS_Tracker_Communication_Protocol_v1.8.1.md §4.3 and
// GPS_Tracker_communication_protocol_v3.1.md §1.1).
const (
	ProtoLogin         = 0x01 // terminal → server login (15-byte ASCII IMEI)
	ProtoPosition      = 0x22 // position, UTC (v3.1)
	ProtoPosition2     = 0x12 // position, legacy non-UTC (v1.8.1)
	ProtoStringInfo    = 0x15 // string information packet (JM01)
	ProtoOnlineReply   = 0x21 // online command reply
	ProtoHeartbeat     = 0x13 // status/heartbeat
	ProtoHeartbeatEG   = 0x23 // status/heartbeat (EG02/EG03)
	ProtoStatusLoc     = 0x20 // location status (compressed)
	ProtoAlarm         = 0x26 // alarm data (UTC)
	ProtoAlarmHVT      = 0x27 // alarm data (HVT001 multi-fence)
	ProtoAlarmLBS      = 0x19 // LBS alarm (no GPS fix)
	ProtoLBSMulti      = 0x28 // LBS multiple-bases extension
	ProtoInfoTransmit  = 0x94 // information transmission (fuel/1-Wire, B5a)
	ProtoOnlineCommand = 0x80 // server online command (outbound only)
	ProtoTimeCheck     = 0x8A // time check (terminal → server)
	ProtoSpeedAlarm    = 0x31 // speed limit alarm
	ProtoPositionAck   = 0x05 // server → terminal position ACK
)

// GT06 framing bytes: 0x78 0x78 uses a 1-byte Packet Length, 0x79 0x79 uses a
// 2-byte length (v3.1 §8.2.1 for large content).
const (
	FrameStartShort = 0x78
	FrameStartLong  = 0x79
	FrameStop0      = 0x0d
	FrameStop1      = 0x0a
)

// Course & Status bit masks of the 2-byte big-endian Course/Status field
// (v1.8.1 §5.2.1.9 / v3.1 §3.1-i).
const (
	StatBitCourse10Mask uint16 = 0x03FF // low 10 bits = course (0..360)
	StatBitPositioned   uint16 = 0x1000 // BYTE_1 bit4: GPS positioned
	StatBitEastLon      uint16 = 0x0800 // BYTE_1 bit3: 0 East / 1 West
	StatBitNorthLat     uint16 = 0x0400 // BYTE_1 bit2: 0 South / 1 North
)

// ConvFactor divides the raw GT06 lat/lon integer: raw = (deg*60+min)*30000, so
// decimal degrees = raw / 1_800_000 (v1.8.1 §5.2.1.6 / v3.1 §3.1).
const ConvFactor = 1_800_000.0

// Device connection policy (FR-1.2 idle timeout, FR-2.2 offline threshold).
const (
	IdleTimeout  = 90 * time.Second
	OfflineAfter = 3 * time.Minute
)

// TelemetryMessage is the canonical payload published to `telemetry.raw.<IMEI>`
// (FR-1.2). CompanyCode + VehicleID are filled from the tenant resolution of
// `master.tm_vehicle_imei_map` before publishing (FR-1.4).
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
	// Altitude (metres, signed) — Teltonika AVL GPS element; GT06 has no
	// altitude field so it stays 0 and is omitted from JSON.
	Altitude  int16  `json:"altitude,omitempty"`
	Battery   uint8  `json:"battery_level,omitempty"`
	GsmSignal uint8  `json:"gsm_signal,omitempty"`
	ACC       bool   `json:"acc,omitempty"`
	Mileage   uint32 `json:"mileage,omitempty"`
	AlarmCode uint8  `json:"alarm_code,omitempty"`
	// AlarmLBS marks a GT06 0x19 LBS alarm packet (no GPS fix, no alarm reason
	// byte) so worker-alert can trigger the SOS life-cycle for it too (B3).
	AlarmLBS  bool   `json:"alarm_lbs,omitempty"`
	Fix       bool   `json:"fix,omitempty"`
	Timestamp int64  `json:"timestamp"`

	// Fuel sensor fields (B5a) — pointers so "absent" is distinguishable from 0.
	FuelLevel  *float64 `json:"fuel_level,omitempty"`
	FuelVolume *float64 `json:"fuel_volume,omitempty"`
	FuelTempC  *float64 `json:"fuel_temp_c,omitempty"`
}
