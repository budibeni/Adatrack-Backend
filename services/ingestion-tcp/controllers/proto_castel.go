package controllers

// proto_castel.go — Castel (SC/CC/MPIP) framing (B9 partial).
//
// Reference: docs/docs-device/traccar-reference/03-priority-medium.md §3.4. The
// common header IS documented and implemented here:
//
//	Header(2B LE, 0x4040) | Length(2B LE) | Version(1B) | ID(20B ASCII) | Type(2B) | Payload
//
// Unlike Navigil, Castel's device ID is a 20-character ASCII string that can
// carry the IMEI, so identity resolution reuses the normal allowlist path: every
// digit run of 15 characters inside the id is tried (FR-1.4).
//
// The GPS payload layout is documented at field level (timestamp 6B BCD, lat 4B,
// lon 4B, speed 1B, course 2B, status 4B) but WITHOUT the lat/lon scale, and the
// scale is exactly the kind of number that must never be guessed (a wrong factor
// silently reports a wrong position). GPS frames are therefore acknowledged and
// COUNTED as unsupported, and everything else (login/logout/heartbeat) works.

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

// Castel command types (docs §3.4).
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
)

// castelIMEIPattern finds a 15-digit IMEI inside the 20-character Castel id.
var castelIMEIPattern = regexp.MustCompile(`\d{15}`)

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

// readCastelFrame reads one 0x40 0x40 little-endian framed packet.
func readCastelFrame(r *bufio.Reader) (castelFrame, error) {
	var f castelFrame
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return f, err
	}
	if binary.LittleEndian.Uint16(hdr[0:2]) != castelHeaderMark {
		return f, fmt.Errorf("castel: bad header 0x%02x%02x", hdr[1], hdr[0])
	}
	length := int(binary.LittleEndian.Uint16(hdr[2:4]))
	if length < 25 || length > castelMaxFrameBytes {
		return f, fmt.Errorf("castel: invalid length %d", length)
	}
	body, err := readFrame(r, length, castelMaxFrameBytes)
	if err != nil {
		return f, err
	}
	f.Version = body[0]
	f.ID = strings.TrimRight(string(body[1:21]), "\x00 ")
	f.Type = binary.LittleEndian.Uint16(body[21:23])
	f.Body = body[23:]
	return f, nil
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
	case castelMsgLogin, castelMsgHeartbeat:
		framesTotal.WithLabelValues(protoName, "heartbeat").Inc()
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
