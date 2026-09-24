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
// GPS PAYLOAD: the field list is documented (timestamp 6B, lat 4B, lon 4B,
// speed 1B, course 2B, status 4B) but without the lat/lon scale, and a wrong scale
// silently reports a wrong position. The v3.0 source shows `/ 3600000.0` for a
// different (older) message variant, which is NOT enough to claim the current
// layout, so GPS frames stay counted in
// `ingestion_unsupported_frames_total{protocol="castel"}` instead of guessed.

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net"
	"regexp"
	"strings"
	"time"

	"adatrack_gps/ingestion-tcp/models"
	"adatrack_gps/internal"
)

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
	default:
		framesTotal.WithLabelValues(protoName, "unsupported_payload").Inc()
		unsupportedFrames.WithLabelValues(protoName).Inc()
		slog.Debug("castel: payload not decodable yet",
			"type", fmt.Sprintf("0x%04x", f.Type), "id", f.ID, "bytes", len(f.Body))
	}
	return true
}
