package controllers

// proto_navigil.go — Navigil (TG2/UnitReport family) framing (B9 partial).
//
// Reference: docs/docs-device/traccar-reference/03-priority-medium.md §3.3. The
// 20-byte little-endian header IS documented and parsed here (protocol version,
// sequence number, message id, length, device id, timestamp), which lets the
// decoder answer the ACK the family requires and exposes the message mix in
// metrics. Two things are NOT implemented, and both are counted explicitly:
//
//  1. The position/unit/tracking PAYLOAD layouts are not described in-repo.
//  2. Navigil identifies devices by a 4-byte DEVICE ID, while the anti-spoofing
//     allowlist (FR-1.4) is IMEI-based (`master.tm_vehicle_imei_map`). Mapping an
//     id → IMEI needs a registration surface, i.e. a schema/API change that B9's
//     acceptance explicitly excludes ("tanpa perubahan service lain").
//
// So Navigil is a FRAMING-ONLY decoder today: no device is treated as
// authenticated and no telemetry is fabricated.

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net"
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

// Serve reads Navigil frames and reports the gap for every payload it cannot decode.
func (d navigilDecoder) Serve(s *Server, c net.Conn) {
	protoName := models.ProtoNavigil.String()
	defer s.connClose(c, protoName)

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
		if hdr.PayloadLen > 0 {
			if _, err := readFrame(r, int(hdr.PayloadLen), 4096); err != nil {
				tcpParseErrors.WithLabelValues(protoName).Inc()
				return
			}
		}
		framesTotal.WithLabelValues(protoName, navigilKind(hdr.MsgID)).Inc()
		unsupportedFrames.WithLabelValues(protoName).Inc()
		slog.Debug("navigil: frame recognised but payload not decodable yet",
			"msg_id", hdr.MsgID, "device_id", hdr.DeviceID, "payload_len", hdr.PayloadLen)

		// The family expects an acknowledgement carrying the sequence number; it is
		// still sent so the device does not treat the server as dead (keep-alive),
		// even though the payload itself is not decoded yet.
		if !writeAll(c, buildNavigilAck(hdr.Sequence)) {
			return
		}
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

// buildNavigilAck frames the MSG_ACKNOWLEDGEMENT (255) carrying the sequence number.
func buildNavigilAck(sequence uint16) []byte {
	frame := make([]byte, 20)
	frame[0], frame[1] = 1, 1
	binary.LittleEndian.PutUint16(frame[2:4], sequence)
	binary.LittleEndian.PutUint16(frame[4:6], navigilMsgAck)
	// Checksum: CRC-16/CCITT-FALSE over the first 10 bytes (docs §3.3 note 3).
	binary.LittleEndian.PutUint16(frame[10:12], navigilCRC16(frame[0:10]))
	return frame
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
