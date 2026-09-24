package controllers

// proto_navigil.go — Navigil (TG2/UnitReport family) as a pluggable decoder (B9).
//
// Reference: docs/docs-device/traccar-reference/03-priority-medium.md §3.3 plus the
// upstream NavigilProtocolDecoder, which pins the header AND the payload layouts
// (the audit gap "payload tidak terdokumentasi" is therefore closed for the two
// message types below):
//
//	header(20, all LE): ver(1) versionId(1) seq(2) msgId(2) length(2) flags(2)
//	                    checksum(2) deviceId(4) timestamp(4)
//
//	MSG_UNIT_REPORT (8):    trigger(2) flags(2) lat(int32/1e7) lon(int32/1e7)
//	                        altitude(2) satellites(2) …
//	MSG_TRACKING_DATA (18): mode(1) flags(1, bit0=valid) duration(2) lat lon
//	                        speed(1, km/h) course(1, ×2) satellites(1)
//	                        battery(2, /1000) odometer(4)
//
// Timestamps are Unix seconds minus the family's 25 leap seconds (upstream
// `convertTimestamp`), and the ACK is a 24-byte MSG_ACKNOWLEDGEMENT whose checksum
// covers the 4 data bytes only.
//
// IDENTITY: Navigil identifies devices by a 4-byte numeric DEVICE ID instead of an
// IMEI, while the anti-spoofing allowlist (FR-1.4) is IMEI-based. The mapping is
// therefore explicit and operational (NAVIGIL_DEVICE_MAP, e.g.
// "1234567=864201040512345,7654321=864201040512999"); an unmapped device is counted
// + logged, never silently trusted. Message types 13/15 keep leading fields that
// upstream does not pin, so they stay counted as unsupported instead of guessed.

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"adatrack_gps/ingestion-tcp/models"
	"adatrack_gps/internal"
)

// Navigil message ids (docs §3.3).
const (
	navigilMsgConnOpen   = 5
	navigilMsgConnClose  = 6
	navigilMsgUnitReport = 8
	navigilMsgPosition   = 13
	navigilMsgPosition2  = 15
	navigilMsgTracking   = 18
	navigilMsgAck        = 255
)

type navigilDecoder struct{}

func (navigilDecoder) Protocol() models.Protocol { return models.ProtoNavigil }

func (navigilDecoder) Port(cfg *internal.Config) string { return cfg.TCP.NavigilPort }

// Serve reads Navigil frames: it acknowledges every frame that asks for it and
// decodes the two payload types whose layout is pinned by the reference.
func (d navigilDecoder) Serve(s *Server, c net.Conn) {
	protoName := models.ProtoNavigil.String()
	defer s.connClose(c, protoName)

	var st session
	defer s.unregisterConn(&st)

	r := bufio.NewReader(c)
	for {
		_ = c.SetReadDeadline(time.Now().Add(s.cfg.TCP.IdleTimeout))
		hdr, err := readNavigilHeader(r)
		if err != nil {
			if err != io.EOF {
				tcpParseErrors.WithLabelValues(protoName).Inc()
			}
			return
		}
		var payload []byte
		if hdr.PayloadLen > 0 {
			if payload, err = readFrame(r, int(hdr.PayloadLen), 4096); err != nil {
				tcpParseErrors.WithLabelValues(protoName).Inc()
				return
			}
		}
		framesTotal.WithLabelValues(protoName, navigilKind(hdr.MsgID)).Inc()

		// The family expects an acknowledgement (its keep-alive: without it the device
		// retransmits and eventually reboots). Upstream only ACKs when the header
		// carries no "no-ack" flag.
		if hdr.Flags&0x1 == 0 {
			if !writeAll(c, buildNavigilAck(hdr.Sequence)) {
				return
			}
		}

		tele, ok := parseNavigilPayload(hdr.MsgID, hdr.Timestamp, payload)
		if !ok {
			unsupportedFrames.WithLabelValues(protoName).Inc()
			slog.Debug("navigil: frame recognised but payload not decodable yet",
				"msg_id", hdr.MsgID, "device_id", hdr.DeviceID, "payload_len", hdr.PayloadLen)
			continue
		}

		imei, ok := navigilIMEI(hdr.DeviceID)
		if !ok {
			unmappedDevices.WithLabelValues(protoName).Inc()
			slog.Warn("navigil: device id has no IMEI mapping (set NAVIGIL_DEVICE_MAP)",
				"device_id", hdr.DeviceID, "remote", c.RemoteAddr().String())
			continue
		}
		if !s.ensureAuth(&st, imei, protoName, c.RemoteAddr().String()) {
			return
		}
		s.registerConn(&st, c, models.ProtoNavigil)
		s.publish(&st, tele, protoName)
	}
}

// navigilHeader is the fixed 20-byte little-endian header (docs §3.3).
type navigilHeader struct {
	ProtocolVer byte
	VersionID   byte
	Sequence    uint16
	MsgID       uint16
	PayloadLen  uint16
	Flags       uint16
	Checksum    uint16
	DeviceID    uint32
	Timestamp   uint32
}

// readNavigilHeader reads and validates one 20-byte header.
func readNavigilHeader(r *bufio.Reader) (navigilHeader, error) {
	var h navigilHeader
	var raw [20]byte
	if _, err := io.ReadFull(r, raw[:]); err != nil {
		return h, err
	}
	h.ProtocolVer = raw[0]
	h.VersionID = raw[1]
	h.Sequence = binary.LittleEndian.Uint16(raw[2:4])
	h.MsgID = binary.LittleEndian.Uint16(raw[4:6])
	h.PayloadLen = binary.LittleEndian.Uint16(raw[6:8])
	h.Flags = binary.LittleEndian.Uint16(raw[8:10])
	h.Checksum = binary.LittleEndian.Uint16(raw[10:12])
	h.DeviceID = binary.LittleEndian.Uint32(raw[12:16])
	h.Timestamp = binary.LittleEndian.Uint32(raw[16:20])
	if h.ProtocolVer == 0 || h.PayloadLen > 4096 {
		return h, fmt.Errorf("navigil: implausible header (ver=%d len=%d)", h.ProtocolVer, h.PayloadLen)
	}
	return h, nil
}

// buildNavigilAck frames the MSG_ACKNOWLEDGEMENT (255) reply. Upstream layout
// (NavigilProtocolDecoder.sendAcknowledgment) is a 24-byte frame:
//
//	header(20): ver=1, versionId=0, seq(2 LE), msgId=255(2 LE), length(2 LE)=24,
//	            flags(2 LE)=0, checksum(2 LE), deviceId(4 LE)=0, timestamp(4 LE)
//	data(4)  : sequenceNumber(2 LE) + status(2 LE, 0 = OK)
//
// The checksum is CRC-16/CCITT-FALSE over the 4 DATA bytes only (not the header)
// — the previous 20-byte/no-data ACK was rejected by devices, which is exactly
// the kind of bug the audit turned up.
func buildNavigilAck(sequence uint16) []byte {
	data := make([]byte, 4)
	binary.LittleEndian.PutUint16(data[0:2], sequence)
	binary.LittleEndian.PutUint16(data[2:4], 0) // status OK

	frame := make([]byte, 20+len(data))
	frame[0], frame[1] = 1, 0
	binary.LittleEndian.PutUint16(frame[2:4], navigilSenderSequence())
	binary.LittleEndian.PutUint16(frame[4:6], navigilMsgAck)
	binary.LittleEndian.PutUint16(frame[6:8], uint16(len(frame)))
	binary.LittleEndian.PutUint16(frame[8:10], 0) // flags
	binary.LittleEndian.PutUint16(frame[10:12], navigilCRC16(data))
	binary.LittleEndian.PutUint32(frame[12:16], 0) // device id (server side)
	binary.LittleEndian.PutUint32(frame[16:20], uint32(time.Now().Unix()+navigilLeapSecondsDelta))
	copy(frame[20:], data)
	return frame
}

// navigilLeapSecondsDelta is the epoch-leap correction the family applies on top
// of Unix time (upstream: LEAP_SECONDS_DELTA = 25).
const navigilLeapSecondsDelta = 25

// navigilSenderSequence is the server-side sequence counter of the ACK frames.
var navigilSenderSequenceCounter atomic.Uint32

// navigilSenderSequence returns the next server sequence number.
func navigilSenderSequence() uint16 {
	n := navigilSenderSequenceCounter.Add(1)
	return uint16(n % 0xFFFF)
}

// navigilCRC16 is CRC-16/CCITT-FALSE (poly 0x1021, init 0xFFFF) — the algorithm
// the reference lists for Navigil (docs/docs-device/traccar-reference/07-appendix.md).
func navigilCRC16(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc ^= uint16(b) << 8
		for i := 0; i < 8; i++ {
			if crc&0x8000 != 0 {
				crc = (crc << 1) ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

// parseNavigilPayload decodes the payload of a message whose layout the reference
// pins. It reads ONLY the documented fields and ignores trailing ones, so a
// firmware with extra data still decodes correctly.
func parseNavigilPayload(msgID uint16, headerTS uint32, payload []byte) (models.TelemetryMessage, bool) {
	var t models.TelemetryMessage
	switch msgID {
	case navigilMsgUnitReport:
		// trigger(2) flags(2) | lat(4) lon(4) | altitude(2) satellites(2) | …
		if len(payload) < 16 {
			return t, false
		}
		t.Lat = float64(int32(binary.LittleEndian.Uint32(payload[4:8]))) / 1e7
		t.Lon = float64(int32(binary.LittleEndian.Uint32(payload[8:12]))) / 1e7
		t.Altitude = int16(binary.LittleEndian.Uint16(payload[12:14]))
		t.Satellites = uint8(binary.LittleEndian.Uint16(payload[14:16]))
		t.Fix = true // upstream marks a unit report valid
		t.Timestamp = navigilTime(headerTS)
		return t, true

	case navigilMsgTracking:
		// mode(1) flags(1) duration(2) | lat(4) lon(4) | speed(1) course(1)
		// satellites(1) battery(2) odometer(4)
		if len(payload) < 18 {
			return t, false
		}
		t.Fix = payload[1]&0x01 == 0x01
		t.Lat = float64(int32(binary.LittleEndian.Uint32(payload[4:8]))) / 1e7
		t.Lon = float64(int32(binary.LittleEndian.Uint32(payload[8:12]))) / 1e7
		t.Speed = float64(payload[12]) // already km/h (FR-1.2 canonical unit)
		t.Heading = int16(payload[13]) * 2
		t.Satellites = payload[14]
		t.Timestamp = navigilTime(headerTS)
		return t, true

	default:
		return t, false
	}
}

// navigilTime converts the family timestamp (Unix seconds + 25 leap seconds) to
// Unix seconds; a zero header timestamp stays 0 so the publish path stamps "now".
func navigilTime(headerTS uint32) int64 {
	if headerTS == 0 {
		return 0
	}
	return int64(headerTS) - navigilLeapSecondsDelta
}

// navigilDeviceMap maps a Navigil device id (decimal, exactly as upstream renders
// the 4-byte LE value) to the IMEI the tenant allowlist knows.
var navigilDeviceMap = parseNavigilDeviceMap(os.Getenv("NAVIGIL_DEVICE_MAP"))

// parseNavigilDeviceMap parses `id=imei,id=imei`; invalid pairs are skipped so a
// typo can never map a device to a foreign IMEI.
func parseNavigilDeviceMap(raw string) map[uint32]string {
	out := map[uint32]string{}
	for _, pair := range strings.Split(raw, ",") {
		parts := strings.SplitN(strings.TrimSpace(pair), "=", 2)
		if len(parts) != 2 {
			continue
		}
		id, err := strconv.ParseUint(strings.TrimSpace(parts[0]), 10, 32)
		if err != nil {
			continue
		}
		imei := strings.TrimSpace(parts[1])
		if !isIMEI(imei) {
			continue
		}
		out[uint32(id)] = imei
	}
	return out
}

// navigilIMEI resolves a device id to its mapped IMEI.
func navigilIMEI(deviceID uint32) (string, bool) {
	imei, ok := navigilDeviceMap[deviceID]
	return imei, ok
}

// NavigilDeviceMapSize reports how many device ids are mapped (boot log / ops).
func NavigilDeviceMapSize() int { return len(navigilDeviceMap) }

// navigilKind maps a message id to its metric label.
func navigilKind(msgID uint16) string {
	switch msgID {
	case navigilMsgConnOpen:
		return "conn_open"
	case navigilMsgConnClose:
		return "conn_close"
	case navigilMsgUnitReport:
		return "unit_report"
	case navigilMsgPosition, navigilMsgPosition2:
		return "position"
	case navigilMsgTracking:
		return "tracking"
	case navigilMsgAck:
		return "ack"
	default:
		return "other"
	}
}
