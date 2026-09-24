// Package models holds the ingestion wire-protocol constants and the canonical
// telemetry payload published to NATS (PRD Module 1 / FR-1.2).
package models

import (
	"fmt"
	"time"
)

// Protocol identifies the device wire protocol served by a listener. Each
// protocol gets its OWN port (GT06 default, Teltonika own reference) so no
// fragile header sniffing between framings is needed (PRD Module 1c).
//
// B9 adds the Traccar-convention families (TK103, Meiligao, Xexun, Suntech, H02,
// Totem, GT02, Navigil, Castel) as first-class members: the protocol is chosen by
// LISTENER (one port per protocol), never by sniffing the first bytes, so a
// malformed stream can never be mis-routed into another decoder.
type Protocol int

const (
	ProtoGT06 Protocol = iota + 1
	ProtoTeltonika
	ProtoTK103
	ProtoMeiligao
	ProtoXexun
	ProtoSuntech
	ProtoH02
	ProtoTotem
	ProtoGT02
	ProtoNavigil
	ProtoCastel
)

// protoNames maps every protocol to its metric/log label. Keeping the mapping in
// one place means adding a decoder cannot silently reuse another label.
var protoNames = map[Protocol]string{
	ProtoGT06:      "gt06",
	ProtoTeltonika: "teltonika",
	ProtoTK103:     "tk103",
	ProtoMeiligao:  "meiligao",
	ProtoXexun:     "xexun",
	ProtoSuntech:   "suntech",
	ProtoH02:       "h02",
	ProtoTotem:     "totem",
	ProtoGT02:      "gt02",
	ProtoNavigil:   "navigil",
	ProtoCastel:    "castel",
}

// String returns the metric/log label of a protocol.
func (p Protocol) String() string {
	if name, ok := protoNames[p]; ok {
		return name
	}
	return fmt.Sprintf("proto_%d", int(p))
}

// IMEI identifies a device on the allowlist (`master.tm_vehicle_imei_map`).
// Protocols without an IMEI field (Navigil device id, Castel 20-char id) resolve
// their own identity first and reuse the same resolution path.
type IMEI = string

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
	Altitude  int16 `json:"altitude,omitempty"`
	Battery   uint8 `json:"battery_level,omitempty"`
	GsmSignal uint8 `json:"gsm_signal,omitempty"`
	// ACC is the DEVICE ignition line (B6). It is TRI-STATE on purpose: a
	// non-nil pointer means the frame reported ACC (true = ON, false = OFF),
	// while `nil` means the protocol/packet did not carry ACC at all (fuel-only
	// sentences, LBS frames, Teltonika devices without an ignition IO). The
	// audit finding behind B6 was that a missing ACC used to be published as
	// `false`, i.e. an inference — with `omitempty` the field now disappears
	// from the payload instead, so downstream services can keep it NULL/absent.
	ACC       *bool  `json:"acc,omitempty"`
	Mileage   uint32 `json:"mileage,omitempty"`
	AlarmCode uint8  `json:"alarm_code,omitempty"`
	// AlarmLBS marks a GT06 0x19 LBS alarm packet (no GPS fix, no alarm reason
	// byte) so worker-alert can trigger the SOS life-cycle for it too (B3).
	AlarmLBS  bool  `json:"alarm_lbs,omitempty"`
	Fix       bool  `json:"fix,omitempty"`
	Timestamp int64 `json:"timestamp"`

	// --- B8 driver behaviour -------------------------------------------------
	// The flags are set only when the FRAME ITSELF carries the event (GT06 alarm
	// reason 0x29/0x30, Teltonika IO 253/254/240); they are never inferred from
	// speed differences here. worker-alert turns them into driver events + score
	// inputs (FR-2.7) so the raw telemetry stays a pure device statement.
	HarshAccel     bool `json:"harsh_accel,omitempty"`
	HarshBraking   bool `json:"harsh_braking,omitempty"`
	HarshCornering bool `json:"harsh_cornering,omitempty"`

	// Fuel sensor fields (B5a) — pointers so "absent" is distinguishable from 0.
	FuelLevel  *float64 `json:"fuel_level,omitempty"`
	FuelVolume *float64 `json:"fuel_volume,omitempty"`
	FuelTempC  *float64 `json:"fuel_temp_c,omitempty"`
	// FuelHeightCM is the RAW GT06 0x0D sensor height in centimetres (v3.1
	// §8.2 "0D Fuel sensor data": `!AIOIL,<count>,<height_cm>,...`). It is kept
	// separate from FuelVolume because the sentence reports a height, not litres;
	// calibration (volt/cm → litres) is out of core scope (FR-7.8), so the raw
	// height is published as-is and consumers derive their own scale.
	FuelHeightCM *float64 `json:"fuel_height_cm,omitempty"`
}

// BoolPtr returns a pointer to v. It exists because the ACC flag is tri-state
// (B6): `BoolPtr(false)` means "the device reported ACC off", while `nil` means
// "this frame carried no ACC information". Keeping the distinction explicit at
// the call sites is what makes the audit fix visible in the code.
func BoolPtr(v bool) *bool { return &v }
