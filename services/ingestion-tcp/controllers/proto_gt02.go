package controllers

// proto_gt02.go — GT02/GT02A (Concox family, binary) as a pluggable decoder (B9).
//
// Frame (docs/docs-device/traccar-reference/03-priority-medium.md §3.2 + upstream
// Gt02ProtocolDecoder/Gt02FrameDecoder):
//
//	0x68 0x68 | Size(1) | Power(1) | GSM(1) | IMEI(8) | Index(2) | Type(1) |
//	Payload(N) | CRC(2) | [0x0D 0x0A]
//
// Size counts Power..Payload. The trailing stop bytes are OPTIONAL: the upstream
// frame decoder sizes the frame as `size + 5` (no stop bytes) while the in-repo
// summary lists 0x0D 0x0A — the reader therefore consumes the stop bytes only
// when they are actually present, so both firmwares work.
//
// Position scaling is the documented one: `raw / (60 × 30000)` (DDMM.mmmm packed
// as an integer), with flags bit1 = north and bit2 = east.

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

// GT02 command types (upstream Gt02ProtocolDecoder).
const (
	gt02MsgData      = 0x10
	gt02MsgHeartbeat = 0x1A
	gt02MsgResponse  = 0x1C
)

type gt02Decoder struct{}

func (gt02Decoder) Protocol() models.Protocol { return models.ProtoGT02 }

func (gt02Decoder) Port(cfg *internal.Config) string { return cfg.TCP.GT02Port }

// Serve handles one GT02 connection (login is implicit: every frame carries the
// IMEI, so the first frame authenticates the device).
func (d gt02Decoder) Serve(s *Server, c net.Conn) {
	protoName := models.ProtoGT02.String()
	defer s.connClose(c, protoName)

	var st session
	defer s.unregisterConn(&st)

	r := bufio.NewReader(c)
	for {
		_ = c.SetReadDeadline(time.Now().Add(s.cfg.TCP.IdleTimeout))
		frame, err := readGT02Frame(r)
		if err != nil {
			if err != io.EOF {
				tcpParseErrors.WithLabelValues(protoName).Inc()
			}
			return
		}
		if !s.gt02HandleFrame(&st, c, frame, protoName) {
			return
		}
	}
}

// gt02Frame is the decoded GT02 envelope shared by every message type.
type gt02Frame struct {
	Power byte
	GSM   byte
	IMEI  string
	Index uint16
	Type  byte
	Body  []byte
}

// readGT02Frame reads one 0x68 0x68 frame.
func readGT02Frame(r *bufio.Reader) (gt02Frame, error) {
	var f gt02Frame
	var hdr [3]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return f, err
	}
	if hdr[0] != 0x68 || hdr[1] != 0x68 {
		return f, fmt.Errorf("gt02: bad start bytes 0x%02x 0x%02x", hdr[0], hdr[1])
	}
	size := int(hdr[2])
	if size < 13 || size > 512 {
		return f, fmt.Errorf("gt02: invalid size %d", size)
	}
	body, err := readFrame(r, size, 512)
	if err != nil {
		return f, err
	}
	var crc [2]byte
	if _, err := io.ReadFull(r, crc[:]); err != nil {
		return f, err
	}
	// The stop bytes are optional across firmwares — consume them only when present.
	if peek, perr := r.Peek(2); perr == nil && peek[0] == 0x0D && peek[1] == 0x0A {
		_, _ = r.Discard(2)
	}

	f.Power, f.GSM = body[0], body[1]
	f.IMEI = hexIMEI(body[2:10])
	f.Index = binary.BigEndian.Uint16(body[10:12])
	f.Type = body[12]
	f.Body = body[13:]

	// CRC: the in-repo summary specifies an XOR checksum without pinning the range,
	// so both plausible ranges (size||content and header||size||content) are
	// accepted — a superset still catches corruption, while a single-range check
	// would reject a whole firmware family on a documentation ambiguity.
	got := binary.BigEndian.Uint16(crc[:])
	wantA := int(hdr[2])
	for _, b := range body {
		wantA ^= int(b)
	}
	wantB := wantA ^ int(hdr[0]) ^ int(hdr[1])
	if int(got) != wantA && int(got) != wantB && checksumStrict {
		return f, fmt.Errorf("gt02: crc mismatch (got 0x%04x want 0x%02x/0x%02x)", got, wantA, wantB)
	}
	return f, nil
}

// gt02HandleFrame dispatches one frame; it reports whether the connection may
// continue (false = the caller must close it).
func (s *Server) gt02HandleFrame(st *session, c net.Conn, f gt02Frame, protoName string) bool {
	if !s.ensureAuth(st, f.IMEI, protoName, c.RemoteAddr().String()) {
		return false
	}
	s.registerConn(st, c, models.ProtoGT02)

	switch f.Type {
	case gt02MsgData:
		framesTotal.WithLabelValues(protoName, "position").Inc()
		tele, ok := parseGT02Position(f.Body)
		if !ok {
			tcpParseErrors.WithLabelValues(protoName).Inc()
			rejectedTotal.WithLabelValues("parse").Inc()
			return true
		}
		s.publish(st, tele, protoName)
	case gt02MsgHeartbeat:
		framesTotal.WithLabelValues(protoName, "heartbeat").Inc()
		s.publish(st, models.TelemetryMessage{Timestamp: time.Now().Unix()}, protoName)
		// Documented heartbeat reply (upstream Gt02ProtocolDecoder).
		if !writeAll(c, []byte{0x54, 0x68, 0x1A, 0x0D, 0x0A}) {
			return false
		}
	case gt02MsgResponse:
		// B8: server-command reply content (upstream KEY_RESULT).
		framesTotal.WithLabelValues(protoName, "command_reply").Inc()
		if content := extractCommandReply(f.Body); content != "" && s.gateway != nil {
			s.gateway.Ack(st.imei, content)
		}
	default:
		framesTotal.WithLabelValues(protoName, "other").Inc()
		unsupportedFrames.WithLabelValues(protoName).Inc()
		slog.Debug("gt02: unhandled message type", "type", f.Type, "imei", st.imei)
	}
	return true
}

// parseGT02Position decodes the MSG_DATA payload:
//
//	Date(YY,MM,DD) | Time(HH,MM,SS) | Lat(4B) | Lon(4B) | Speed(1B km/h) |
//	Course(2B) | Reserved(3B) | Flags(4B)
//
// Flags: bit0 = valid fix, bit1 = NORTH, bit2 = EAST.
func parseGT02Position(body []byte) (models.TelemetryMessage, bool) {
	var t models.TelemetryMessage
	if len(body) < 18 {
		return t, false
	}
	year, month, day := int(body[0]), int(body[1]), int(body[2])
	hour, minute, second := int(body[3]), int(body[4]), int(body[5])
	if month < 1 || month > 12 || day < 1 || day > 31 || hour > 23 || minute > 59 || second > 59 {
		return t, false
	}
	if year < 100 {
		year += 2000 // 2-digit year offset from 2000 (same convention as GT06)
	}
	t.Timestamp = time.Date(year, time.Month(month), day, hour, minute, second, 0, time.UTC).Unix()

	rawLat := binary.BigEndian.Uint32(body[6:10])
	rawLon := binary.BigEndian.Uint32(body[10:14])
	t.Speed = float64(body[14]) // already km/h (FR-1.2 canonical unit)
	t.Heading = int16(binary.BigEndian.Uint16(body[15:17]))

	flags := binary.BigEndian.Uint32(body[len(body)-4:])
	t.Fix = flags&0x01 != 0
	lat := float64(rawLat) / (60.0 * 30000.0)
	lon := float64(rawLon) / (60.0 * 30000.0)
	if flags&0x02 == 0 {
		lat = -lat
	}
	if flags&0x04 == 0 {
		lon = -lon
	}
	t.Lat, t.Lon = lat, lon
	return t, true
}
