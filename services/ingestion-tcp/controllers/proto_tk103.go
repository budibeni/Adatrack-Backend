package controllers

// proto_tk103.go — TK103 (GT-clone family) as a pluggable decoder (B9).
//
// Reference: docs/docs-device/traccar-reference/01-overview.md ("TK103
// provisional: login/heartbeat/position dasar") + the upstream
// Tk103ProtocolDecoder field order. Scope is deliberately the PLAIN POSITION
// SUBSET: login, heartbeat and `imei:<15>,tracker,...` position reports. The
// TK103 command matrix (alarms, RFID, BMS/OBD, temperature, handshake BP00/BS50)
// is explicitly NOT implemented and is counted as unsupported instead of being
// guessed — the same honesty rule the GT06/Teltonika work followed.

import (
	"net"
	"strings"
	"time"

	"adatrack_gps/ingestion-tcp/models"
	"adatrack_gps/internal"
)

type tk103Decoder struct{}

func (tk103Decoder) Protocol() models.Protocol { return models.ProtoTK103 }

func (tk103Decoder) Port(cfg *internal.Config) string { return cfg.TCP.TK103Port }

// Serve handles one TK103 connection.
func (d tk103Decoder) Serve(s *Server, c net.Conn) {
	protoName := models.ProtoTK103.String()
	defer s.connClose(c, protoName)

	var st session
	defer s.unregisterConn(&st)

	s.serveLineProtocol(c, protoName, ';', 512, func(line []byte) ([]byte, bool) {
		frame := strings.TrimSpace(string(line))
		if frame == "" {
			return nil, false
		}

		// Login: `##,imei:123456789012345,A;`
		if strings.HasPrefix(frame, "##") {
			framesTotal.WithLabelValues(protoName, "login").Inc()
			imei := parseIMEIPrefix(frame)
			if !s.ensureAuth(&st, imei, protoName, c.RemoteAddr().String()) {
				return nil, true
			}
			s.registerConn(&st, c, models.ProtoTK103)
			return []byte("LOAD"), false
		}

		// Heartbeat: a bare 15-digit IMEI.
		if isIMEI(frame) {
			framesTotal.WithLabelValues(protoName, "heartbeat").Inc()
			if !s.ensureAuth(&st, frame, protoName, c.RemoteAddr().String()) {
				return nil, true
			}
			s.registerConn(&st, c, models.ProtoTK103)
			if st.authenticated() {
				s.publish(&st, models.TelemetryMessage{Timestamp: time.Now().Unix()}, protoName)
			}
			return []byte("ON"), false
		}

		// Position: `imei:<15>,tracker,...`
		imei := parseIMEIPrefix(frame)
		if !s.ensureAuth(&st, imei, protoName, c.RemoteAddr().String()) {
			if imei != "" {
				return nil, true
			}
			return nil, false
		}
		if !st.authenticated() {
			return nil, false // not a frame this decoder can attribute to a device
		}
		framesTotal.WithLabelValues(protoName, "position").Inc()
		tele, ok := parseTK103Sentence(frame)
		if !ok {
			rejectedTotal.WithLabelValues("parse").Inc()
			unsupportedFrames.WithLabelValues(protoName).Inc()
			return nil, false
		}
		s.publish(&st, tele, protoName)
		return nil, false
	})
}
