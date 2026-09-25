package controllers

// proto_castel.go — Castel (SC/CC/MPIP) framing, identity and server responses (B9).
//
// Reference: docs/docs-device/traccar-reference/03-priority-medium.md §3.4 plus the
// upstream CastelProtocolDecoder (the v3.0 source is complete enough to pin the two
// rules below, the master version confirms them):
//
//	Header(2B LE, 0x4040) | Length(2B LE) | Version(1B) | ID(20B ASCII) | Type(2B) | Payload | CRC16(2B) | 0x0D 0x0A
//
//  1. `Length` counts the WHOLE frame (header + length + version + id + type +
//     payload + CRC + footer). Verified by the upstream response builder, which
//     writes `response.writeShort(response.capacity())` — 31 bytes for a heartbeat
//     response (2+2+1+20+2+2+2) and 41 for a login response (2+2+1+20+2+4+2+4+2+2).
//     The previous reader treated it as "bytes after the header", so every session
//     drifted by 4 bytes after the first frame (and counted the CRC+footer as
//     payload) — the audit called this out and it is fixed here.
//  2. Login/heartbeat MUST be answered (0x9001/0x9003) or the device retries and
//     never streams positions. The response frame is built here; the byte order of
//     the type field is the one point the reference does not settle (the old
//     upstream writes it big-endian into a little-endian buffer while reading the
//     device side as little-endian), so it stays a documented toggle
//     (CASTEL_RESPONSE_TYPE_BE, default false = the same order this reader uses).
//
// IDENTITY: the 20-character id can carry the IMEI, so identity reuses the normal
// allowlist path: every 15-digit run inside the id is tried (FR-1.4).
//
// GPS PAYLOAD (MSG_SC_GPS 0x4001 and the MPIP position commands): the field order
// and the scales are verified from the upstream source:
//
//	date/time(6, plain bytes) | lat(uint32 LE / 3_600_000) | lon(uint32 LE / 3_600_000) |
//	speed(uint16 LE, cm/s → km/h) | course(uint16 LE / 10) | flags(1)
//
// (the 2026 upstream commit "Use division for decimal scaling" shows the exact lines:
// `double lat = buf.readUnsignedIntLE() / 3600000.0;` … `knotsFromCps(buf.readUnsignedShortLE())`
// … `setCourse(buf.readUnsignedShortLE() / 10.0)` … `int flags = buf.readUnsignedByte();`).
//
// The FLAG BITS are the one point the two upstream revisions do not state identically
// (2015: bit0 = latitude sign, bit1 = longitude sign, bits 2-3 = fix, high nibble =
// satellites; the modern source only shows that bit1 is checked first), so decoding is
// OFF by default and the convention is chosen explicitly:
//
//	CASTEL_GPS_DECODE = off (default) | on (legacy sign bits) | on-swapped (bit1 = lat)
//
// With `off` the frames stay counted in `ingestion_unsupported_frames_total`, exactly
// as before; with `on`/`on-swapped` the operator confirms the convention against one
// real frame (a mirrored position is visible immediately) — the same honesty rule the
// rest of the B9 work follows.

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"regexp"
	"strings"
	"time"

	"adatrack_gps/ingestion-tcp/models"
	"adatrack_gps/internal"
)

// Castel GPS decoding modes (CASTEL_GPS_DECODE).
const (
	castelGPSOff       = "off"
	castelGPSOn        = "on"
	castelGPSOnSwapped = "on-swapped"
)

// castelGPSMode reads the opt-in switch once at boot.
var castelGPSMode = strings.ToLower(strings.TrimSpace(envOrLocal("CASTEL_GPS_DECODE", castelGPSOff)))

// castelGPSEnabled reports whether position frames are decoded.
func castelGPSEnabled() bool {
	return castelGPSMode == castelGPSOn || castelGPSMode == castelGPSOnSwapped
}

// CastelGPSMode reports the active switch (boot log / ops).
func CastelGPSMode() string { return castelGPSMode }

// envOrLocal reads an env var with a default (kept local so the Castel switch stays
// next to the code that documents it).
func envOrLocal(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// Castel command types (docs §3.4 + upstream).
const (
	castelMsgLogin      = 0x1001
	castelMsgLogout     = 0x1002
	castelMsgHeartbeat  = 0x1003
	castelMsgGPS        = 0x4001
	castelMsgPIDData    = 0x4002
	castelMsgGSensor    = 0x4003
	castelMsgOBD        = 0x4005
	castelMsgAlarm      = 0x4007
	castelMsgCell       = 0x4008
	castelMsgFuel       = 0x400E
	castelMsgComprehen  = 0x401F
	castelHeaderMark    = 0x4040
	castelMaxFrameBytes = 4096

	// Server → device replies (upstream MSG_SC_LOGIN_RESPONSE / MSG_SC_HEARTBEAT_RESPONSE).
	castelMsgLoginResponse     = 0x9001
	castelMsgHeartbeatResponse = 0x9003
)

// Castel frame layout sizes (used for the length convention and the CRC check).
const (
	castelHeaderBytes = 4  // 0x4040 + length
	castelFixedBytes  = 23 // version(1) + id(20) + type(2)
	castelTailBytes   = 4  // CRC(2) + 0x0D 0x0A
	castelMinFrame    = castelHeaderBytes + castelFixedBytes + castelTailBytes
)

// castelIMEIPattern finds a 15-digit IMEI inside the 20-character Castel id.
var castelIMEIPattern = regexp.MustCompile(`\d{15}`)

// castelResponseTypeBigEndian decides the byte order of the type field in the
// server response (see the file header note); default = the reader's order.
var castelResponseTypeBigEndian = envBoolLocal("CASTEL_RESPONSE_TYPE_BE", false)

// CastelResponseTypeBigEndian reports the active reply byte order (boot log).
func CastelResponseTypeBigEndian() bool { return castelResponseTypeBigEndian }

type castelDecoder struct{}

func (castelDecoder) Protocol() models.Protocol { return models.ProtoCastel }

func (castelDecoder) Port(cfg *internal.Config) string { return cfg.TCP.CastelPort }

// Serve reads Castel frames; identity + heartbeats are handled, undecodable
// payloads are counted (see the file header).
func (d castelDecoder) Serve(s *Server, c net.Conn) {
	protoName := models.ProtoCastel.String()
	defer s.connClose(c, protoName)

	var st session
	defer s.unregisterConn(&st)

	r := bufio.NewReader(c)
	for {
		_ = c.SetReadDeadline(time.Now().Add(s.cfg.TCP.IdleTimeout))
		frame, err := readCastelFrame(r)
		if err != nil {
			if err != io.EOF {
				tcpParseErrors.WithLabelValues(protoName).Inc()
			}
			return
		}
		if !s.castelHandle(&st, c, frame, protoName) {
			return
		}
	}
}

// castelFrame is the decoded Castel envelope.
type castelFrame struct {
	Version byte
	ID      string
	Type    uint16
	Body    []byte
}

// readCastelFrame reads one 0x40 0x40 framed packet. `Length` counts the whole
// frame, so the reader consumes exactly that many bytes and splits off the
// CRC + footer tail (see the file header note 1).
func readCastelFrame(r *bufio.Reader) (castelFrame, error) {
	var f castelFrame
	var hdr [castelHeaderBytes]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return f, err
	}
	if binary.LittleEndian.Uint16(hdr[0:2]) != castelHeaderMark {
		return f, fmt.Errorf("castel: bad header 0x%02x%02x", hdr[1], hdr[0])
	}
	total := int(binary.LittleEndian.Uint16(hdr[2:4]))
	if total < castelMinFrame || total > castelMaxFrameBytes {
		return f, fmt.Errorf("castel: invalid frame length %d", total)
	}
	body, err := readFrame(r, total-castelHeaderBytes, castelMaxFrameBytes)
	if err != nil {
		return f, err
	}
	f.Version = body[0]
	f.ID = strings.TrimRight(string(body[1:21]), "\x00 ")
	f.Type = binary.LittleEndian.Uint16(body[21:23])

	// The tail carries CRC-16/CCITT-FALSE over everything before it plus the
	// 0x0D 0x0A footer. A mismatch is counted (never silently accepted as payload)
	// but does not drop the frame: the fields above are still trustworthy.
	f.Body = body[castelFixedBytes : len(body)-castelTailBytes]
	if len(body) >= castelTailBytes && body[len(body)-2] == 0x0D && body[len(body)-1] == 0x0A {
		want := binary.BigEndian.Uint16(body[len(body)-castelTailBytes : len(body)-2])
		if got := navigilCRC16(body[:len(body)-castelTailBytes]); got != want {
			castelCRCErrors.Inc()
			slog.Debug("castel: checksum mismatch", "want", fmt.Sprintf("0x%04x", want),
				"got", fmt.Sprintf("0x%04x", got))
		}
	}
	return f, nil
}

// buildCastelResponse frames a server→device reply: version(1) + id(20) + type(2)
// + optional payload, with `Length` = the whole frame, CRC-16/CCITT-FALSE over
// everything before it and a 0x0D 0x0A footer (upstream sendResponse).
func buildCastelResponse(version byte, id string, msgType uint16, payload []byte) []byte {
	fixed := make([]byte, 0, castelFixedBytes)
	fixed = append(fixed, version)
	fixed = append(fixed, []byte(fmt.Sprintf("%-20s", id))[:20]...)
	if castelResponseTypeBigEndian {
		fixed = binary.BigEndian.AppendUint16(fixed, msgType)
	} else {
		fixed = binary.LittleEndian.AppendUint16(fixed, msgType)
	}

	body := append(fixed, payload...)
	frame := make([]byte, 0, castelHeaderBytes+len(body)+castelTailBytes)
	frame = binary.LittleEndian.AppendUint16(frame, castelHeaderMark)
	// Length = whole frame, known up front (header + body + CRC + footer).
	frame = binary.LittleEndian.AppendUint16(frame, uint16(castelHeaderBytes+len(body)+castelTailBytes))
	frame = append(frame, body...)
	frame = binary.BigEndian.AppendUint16(frame, navigilCRC16(frame))
	return append(frame, 0x0D, 0x0A)
}

// castelLoginResponsePayload is the login reply body: 0xFFFFFFFF + 0x0000 + the
// current Unix time (upstream writes exactly these three fields).
func castelLoginResponsePayload() []byte {
	out := make([]byte, 0, 10)
	out = append(out, 0xFF, 0xFF, 0xFF, 0xFF)
	out = binary.BigEndian.AppendUint16(out, 0)
	return binary.BigEndian.AppendUint32(out, uint32(time.Now().Unix()))
}

// castelPositionPayloadBytes is the exact length of the position block
// (date/time 6 + lat 4 + lon 4 + speed 2 + course 2 + flags 1). Frames with fewer
// bytes are never parsed into a half-position.
const castelPositionPayloadBytes = 19

// parseCastelPosition decodes the verified position block (see the file header).
//
//	date/time(6) | lat(uint32 LE / 3.6e6) | lon(uint32 LE / 3.6e6) |
//	speed(uint16 LE, cm/s) | course(uint16 LE /10) | flags(1)
//
// It returns false when decoding is switched off (default), when the block is too
// short, when the date/time is implausible or when the coordinates are out of range
// — the caller then counts the frame as unsupported instead of publishing it.
func parseCastelPosition(body []byte) (models.TelemetryMessage, bool) {
	var t models.TelemetryMessage
	if !castelGPSEnabled() || len(body) < castelPositionPayloadBytes {
		return t, false
	}
	ts, ok := castelDateTime(body[0:6])
	if !ok {
		return t, false
	}
	lat := float64(binary.LittleEndian.Uint32(body[6:10])) / 3600000.0
	lon := float64(binary.LittleEndian.Uint32(body[10:14])) / 3600000.0
	// Speed travels in cm/s (upstream `knotsFromCps`); 1 cm/s = 0.036 km/h.
	speedKMH := float64(binary.LittleEndian.Uint16(body[14:16])) * 0.036
	course := float64(binary.LittleEndian.Uint16(body[16:18])) / 10.0
	flags := body[18]

	// Sign convention (see the file header): `on` = 2015 bit assignment, `on-swapped`
	// = latitude takes bit1.
	latBit, lonBit := byte(0x01), byte(0x02)
	if castelGPSMode == castelGPSOnSwapped {
		latBit, lonBit = 0x02, 0x01
	}
	if flags&latBit == 0 {
		lat = -lat
	}
	if flags&lonBit == 0 {
		lon = -lon
	}
	if lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return t, false
	}

	t.Timestamp = ts
	t.Lat, t.Lon = lat, lon
	t.Speed = speedKMH
	t.Heading = int16(course)
	t.Fix = flags&0x0C > 0 // bits 2-3 = fix (2015 reference)
	t.Satellites = flags >> 4
	return t, true
}

// castelDateTime converts the 6 plain date/time bytes (day, month, year-2000, hour,
// minute, second) into a Unix timestamp, rejecting implausible values.
func castelDateTime(b []byte) (int64, bool) {
	day, month, year := int(b[0]), int(b[1]), 2000+int(b[2])
	hour, minute, second := int(b[3]), int(b[4]), int(b[5])
	if month < 1 || month > 12 || day < 1 || day > 31 ||
		hour > 23 || minute > 59 || second > 60 || year < 2000 || year > 2100 {
		return 0, false
	}
	return time.Date(year, time.Month(month), day, hour, minute, second, 0, time.UTC).Unix(), true
}

// castelHandle dispatches one frame; false = the connection must close.
func (s *Server) castelHandle(st *session, c net.Conn, f castelFrame, protoName string) bool {
	// The device id carries the IMEI for SC/CC trackers; a frame without a
	// resolvable 15-digit IMEI cannot be authenticated, so it is rejected.
	if m := castelIMEIPattern.FindString(f.ID); m != "" {
		if !s.ensureAuth(st, m, protoName, c.RemoteAddr().String()) {
			return false
		}
		s.registerConn(st, c, models.ProtoCastel)
	}

	switch f.Type {
	case castelMsgLogin:
		framesTotal.WithLabelValues(protoName, "login").Inc()
		// The device waits for the login reply before it streams anything, so the
		// response is mandatory (see the file header note 2).
		if !writeAll(c, buildCastelResponse(f.Version, f.ID, castelMsgLoginResponse, castelLoginResponsePayload())) {
			return false
		}
		if st.authenticated() {
			s.publish(st, models.TelemetryMessage{Timestamp: time.Now().Unix()}, protoName)
		}
	case castelMsgHeartbeat:
		framesTotal.WithLabelValues(protoName, "heartbeat").Inc()
		if !writeAll(c, buildCastelResponse(f.Version, f.ID, castelMsgHeartbeatResponse, nil)) {
			return false
		}
		if st.authenticated() {
			s.publish(st, models.TelemetryMessage{Timestamp: time.Now().Unix()}, protoName)
		}
	case castelMsgLogout:
		framesTotal.WithLabelValues(protoName, "logout").Inc()
		return false
	case castelMsgGPS:
		framesTotal.WithLabelValues(protoName, "position").Inc()
		tele, ok := parseCastelPosition(f.Body)
		if !ok {
			unsupportedFrames.WithLabelValues(protoName).Inc()
			slog.Debug("castel: position payload not decoded",
				"id", f.ID, "bytes", len(f.Body), "mode", castelGPSMode)
			break
		}
		if st.authenticated() {
			s.publish(st, tele, protoName)
		}
	default:
		framesTotal.WithLabelValues(protoName, "unsupported_payload").Inc()
		unsupportedFrames.WithLabelValues(protoName).Inc()
		slog.Debug("castel: payload not decodable yet",
			"type", fmt.Sprintf("0x%04x", f.Type), "id", f.ID, "bytes", len(f.Body))
	}
	return true
}
