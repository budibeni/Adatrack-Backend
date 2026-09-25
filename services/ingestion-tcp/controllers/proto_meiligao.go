package controllers

// proto_meiligao.go — Meiligao (GT30i/GT60/VT300) as a pluggable decoder (B9).
//
// Frame (docs/docs-device/traccar-reference/02-priority-high.md §2.1 + upstream
// MeiligaoProtocolDecoder):
//
//	0x24 0x24 | Length(2B BE) | Command(2B BE) | Payload(N) | Serial(2B BE) |
//	Checksum(2B) | 0x0D 0x0A
//
// `Length` counts Command..Serial, so Payload = Length − 4 bytes. The position
// payload is an ASCII sentence WITHOUT the GPRMC tag (the command header carries
// the type), exactly as the upstream `decodeRegular` parses it.
//
// The login packet carries the IMEI as 7 BCD bytes (14 digits). `tm_vehicle_imei_map.imei`
// stores 15 digits, so both forms are tried (14-digit as-is, then left-padded with
// '0') — the allowlist stays authoritative either way (FR-1.4).

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"

	"adatrack_gps/ingestion-tcp/models"
	"adatrack_gps/internal"
)

// Meiligao command codes.
//
// Two upstream revisions and the in-repo reference disagree on the values, so the
// decoder accepts the UNION and lets the strict sentence parser decide:
//
//	upstream v3.0 : MSG_HEARTBEAT 0x0001, MSG_LOGIN 0x5000, MSG_POSITION 0x9955,
//	                MSG_POSITION_LOGGED 0x9016, MSG_ALARM 0x9999
//	in-repo doc   : MSG_LOGIN 0x5001, MSG_POSITION 0x5002, MSG_HEARTBEAT 0x5003,
//	                MSG_ALARM 0x5004, MSG_LOGIN_RESPONSE 0x9999
//
// A real device frame from the upstream test suite (`2424011e…9999` + position
// sentence, no leading alarm byte) proves that position-bearing frames DO arrive
// with 0x9999, which the in-repo doc labels as the login response — so accepting
// both roles is the only honest option (see docs/B8-B10-VERIFICATION.md §2.7).
const (
	meiligaoMsgHeartbeatLegacy = 0x0001
	meiligaoMsgHeartbeat       = 0x5003
	meiligaoMsgLoginLegacy     = 0x5000
	meiligaoMsgLogin           = 0x5001
	meiligaoMsgPosition        = 0x5002
	meiligaoMsgAlarm           = 0x5004
	meiligaoMsgPositionLogged  = 0x9016
	meiligaoMsgPositionLegacy  = 0x9955
	meiligaoMsgPositionLatest  = 0x9999
	// meiligaoMsgAck is the command this server sends back for login/heartbeat
	// (same value the in-repo reference documents).
	meiligaoMsgAck = 0x9999
)

// isMeiligaoPositionCommand reports whether a command carries a position sentence.
func isMeiligaoPositionCommand(cmd uint16) bool {
	switch cmd {
	case meiligaoMsgPosition, meiligaoMsgAlarm, meiligaoMsgPositionLogged,
		meiligaoMsgPositionLegacy, meiligaoMsgPositionLatest:
		return true
	}
	return false
}

type meiligaoDecoder struct{}

func (meiligaoDecoder) Protocol() models.Protocol { return models.ProtoMeiligao }

func (meiligaoDecoder) Port(cfg *internal.Config) string { return cfg.TCP.MeiligaoPort }

// meiligaoFrame is the decoded envelope.
type meiligaoFrame struct {
	Command uint16
	Payload []byte
	Serial  uint16
}

// Serve handles one Meiligao connection.
func (d meiligaoDecoder) Serve(s *Server, c net.Conn) {
	protoName := models.ProtoMeiligao.String()
	defer s.connClose(c, protoName)

	var st session
	defer s.unregisterConn(&st)
	var rawIMEI []byte // 7 BCD bytes echoed in the login/heartbeat ACK

	r := bufio.NewReader(c)
	for {
		_ = c.SetReadDeadline(time.Now().Add(s.cfg.TCP.IdleTimeout))
		frame, err := readMeiligaoFrame(r)
		if err != nil {
			if err != io.EOF {
				tcpParseErrors.WithLabelValues(protoName).Inc()
			}
			return
		}
		switch {
		case frame.Command == meiligaoMsgLogin || frame.Command == meiligaoMsgLoginLegacy:
			framesTotal.WithLabelValues(protoName, "login").Inc()
			rawIMEI = frame.Payload[:min(len(frame.Payload), 7)]
			imei := bcdIMEI(rawIMEI)
			// Candidates, most likely first: the 15-digit IMEI the device means
			// (Luhn-completed, upstream behaviour), the legacy zero-padded form and the
			// raw 14-digit id (in case the allowlist stores it that way).
			if !s.loginAny(&st, []string{luhnIMEI(imei), padIMEI(imei), imei},
				protoName, c.RemoteAddr().String()) {
				return
			}
			s.registerConn(&st, c, models.ProtoMeiligao)
			if !writeAll(c, buildMeiligaoAck(rawIMEI, frame.Serial)) {
				return
			}

		case frame.Command == meiligaoMsgHeartbeat || frame.Command == meiligaoMsgHeartbeatLegacy:
			framesTotal.WithLabelValues(protoName, "heartbeat").Inc()
			if st.authenticated() {
				s.publish(&st, models.TelemetryMessage{Timestamp: time.Now().Unix()}, protoName)
			}
			if len(rawIMEI) > 0 && !writeAll(c, buildMeiligaoAck(rawIMEI, frame.Serial)) {
				return
			}

		case isMeiligaoPositionCommand(frame.Command):
			framesTotal.WithLabelValues(protoName, "position").Inc()
			if !st.authenticated() {
				rejectedTotal.WithLabelValues("no_auth").Inc()
				return
			}
			tele, alarm, ok := parseMeiligaoPositionPayload(frame.Payload)
			if !ok {
				tcpParseErrors.WithLabelValues(protoName).Inc()
				rejectedTotal.WithLabelValues("parse").Inc()
				continue
			}
			if alarm > 0 {
				tele.AlarmCode = alarm
			}
			s.publish(&st, tele, protoName)

		default:
			framesTotal.WithLabelValues(protoName, "other").Inc()
			unsupportedFrames.WithLabelValues(protoName).Inc()
			slog.Debug("meiligao: unhandled command", "command", fmt.Sprintf("0x%04x", frame.Command),
				"imei", st.imei)
		}
	}
}

// parseMeiligaoPositionPayload decodes the sentence of a position-bearing command.
//
// The revisions disagree on whether an alarm/logged header precedes the sentence
// (v3.0 skips 6 bytes for MSG_POSITION_LOGGED and one alarm byte for MSG_ALARM, the
// real 0x9999 frame from the upstream test suite has none), so the documented
// offsets are tried IN ORDER and the first one the strict sentence parser accepts
// wins. A payload that matches none is counted as unsupported — never guessed.
func parseMeiligaoPositionPayload(payload []byte) (models.TelemetryMessage, byte, bool) {
	for _, skip := range []int{0, 1, 6} {
		if skip >= len(payload) {
			continue
		}
		tele, ok := parseMeiligaoSentence(string(payload[skip:]))
		if !ok {
			continue
		}
		var alarm byte
		if skip == 1 {
			alarm = payload[0]
		}
		return tele, alarm, true
	}
	return models.TelemetryMessage{}, 0, false
}

// readMeiligaoFrame reads one 0x24 0x24 frame.
func readMeiligaoFrame(r *bufio.Reader) (meiligaoFrame, error) {
	var f meiligaoFrame
	b0, err := r.ReadByte()
	if err != nil {
		return f, err
	}
	b1, err := r.ReadByte()
	if err != nil {
		return f, err
	}
	// Leading 0x24 bytes are stripped on the device side too, so a single start
	// byte means the previous frame ended early — resynchronise instead of
	// desynchronising the whole stream.
	if b0 != 0x24 {
		return f, fmt.Errorf("meiligao: bad start byte 0x%02x", b0)
	}
	if b1 != 0x24 {
		return f, fmt.Errorf("meiligao: bad second start byte 0x%02x", b1)
	}

	var lenBuf [2]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return f, err
	}
	length := int(binary.BigEndian.Uint16(lenBuf[:]))
	if length < 6 || length > 2048 {
		return f, fmt.Errorf("meiligao: invalid length %d", length)
	}
	content, err := readFrame(r, length, 2048)
	if err != nil {
		return f, err
	}
	var crc [2]byte
	if _, err := io.ReadFull(r, crc[:]); err != nil {
		return f, err
	}
	if peek, perr := r.Peek(2); perr == nil && peek[0] == 0x0D && peek[1] == 0x0A {
		_, _ = r.Discard(2)
	}

	f.Command = binary.BigEndian.Uint16(content[0:2])
	f.Serial = binary.BigEndian.Uint16(content[len(content)-2:])
	f.Payload = content[2 : len(content)-2]

	// XOR checksum over Length||Content (both documented variants accepted; see
	// proto_gt02.go for the rationale on tolerating a documentation ambiguity).
	got := crc[1]
	wantA := xorChecksum(append(append([]byte{}, lenBuf[:]...), content...))
	wantB := xorChecksum(content)
	if got != wantA && got != wantB && checksumStrict {
		return f, fmt.Errorf("meiligao: checksum mismatch (got 0x%02x want 0x%02x/0x%02x)", got, wantA, wantB)
	}
	return f, nil
}

// buildMeiligaoAck frames the server response (login/heartbeat ACK, MSG 0x9999):
// 0x24 0x24 | Length | 0x9999 | IMEI(7B) | Serial(2B) | Checksum(2B) | 0x0D 0x0A.
func buildMeiligaoAck(imeiRaw []byte, serial uint16) []byte {
	content := []byte{0x99, 0x99}
	content = append(content, imeiRaw...)
	content = append(content, byte(serial>>8), byte(serial))

	frame := []byte{0x24, 0x24, byte(len(content) >> 8), byte(len(content))}
	frame = append(frame, content...)
	frame = append(frame, 0x00, xorChecksum(content))
	frame = append(frame, 0x0D, 0x0A)
	return frame
}

// bcdIMEI decodes the 7-byte BCD IMEI of the login packet (14 digits).
func bcdIMEI(raw []byte) string {
	var sb strings.Builder
	for _, b := range raw {
		sb.WriteString(fmt.Sprintf("%02d", bcdToInt(b)))
	}
	return sb.String()
}

// padIMEI left-pads a 14-digit Meiligao IMEI to the 15 digits the allowlist
// stores (some Meiligao firmwares report 15 digits themselves, in which case the
// candidate list already contains the exact value first).
func padIMEI(imei string) string {
	if len(imei) >= 15 {
		return imei
	}
	return strings.Repeat("0", 15-len(imei)) + imei
}

// luhnIMEI completes a 14-digit device id with the IMEI Luhn check digit, exactly
// as the upstream decoder does (`id += Crc.luhnChecksum(id)`); this is the form a
// real Meiligao terminal reports, so it is tried BEFORE the legacy zero-padded
// variant.
func luhnIMEI(id string) string {
	if len(id) != 14 {
		return ""
	}
	sum, double := 0, true
	for i := len(id) - 1; i >= 0; i-- {
		d := int(id[i] - '0')
		if d < 0 || d > 9 {
			return ""
		}
		if double {
			if d *= 2; d > 9 {
				d -= 9
			}
		}
		sum += d
		double = !double
	}
	return id + strconv.Itoa((10-sum%10)%10)
}

// loginAny runs the allowlist check against the first candidate that resolves,
// so protocol-specific IMEI representations (14/15 digit) do not need a schema
// change while the allowlist remains the single source of truth (FR-1.4).
func (s *Server) loginAny(st *session, candidates []string, protoName, remote string) bool {
	var lastErr error
	for _, imei := range candidates {
		if imei == "" {
			continue
		}
		if dev, err := s.tenants.ResolveDeviceByIMEI(s.ctx, imei); err == nil {
			st.imei, st.company, st.vehicleID = imei, dev.CompanyCode, dev.VehicleID
			slog.Info("ingestion authenticated", "protocol", protoName, "imei", imei,
				"company", dev.CompanyCode, "vehicle_id", dev.VehicleID, "remote", remote)
			return true
		} else {
			lastErr = err
		}
	}
	slog.Warn("ingestion: unauthorised IMEI rejected (anti-spoofing)",
		"protocol", protoName, "candidates", len(candidates), "remote", remote, "reason", lastErr)
	rejectedTotal.WithLabelValues("unauthorised").Inc()
	return false
}
