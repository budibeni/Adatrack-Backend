package controllers

// proto_xexun.go — Xexun/GPS103 family as a pluggable decoder (B9).
//
// Reference: docs/docs-device/traccar-reference/02b-xexun.md + upstream
// XexunProtocolDecoder (basic + full NMEA variants). Authentication comes from
// the trailing `imei:<15>` field, so the very first frame (a heartbeat or a
// position report) both identifies and authenticates the device — no dedicated
// login packet exists for this family. There is NO ACK: Xexun is
// fire-and-forget, which is why the decoder never writes back.

import (
	"net"
	"strings"
	"time"

	"adatrack_gps/ingestion-tcp/models"
	"adatrack_gps/internal"
)

type xexunDecoder struct{}

func (xexunDecoder) Protocol() models.Protocol { return models.ProtoXexun }

func (xexunDecoder) Port(cfg *internal.Config) string { return cfg.TCP.XexunPort }

// Serve handles one Xexun connection.
func (d xexunDecoder) Serve(s *Server, c net.Conn) {
	protoName := models.ProtoXexun.String()
	defer s.connClose(c, protoName)

	var st session
	defer s.unregisterConn(&st)

	s.serveLineProtocol(c, protoName, ';', 1024, func(line []byte) ([]byte, bool) {
		frame := strings.TrimSpace(string(line))
		if frame == "" {
			return nil, false
		}
		imei := parseIMEIPrefix(frame)
		if !s.ensureAuth(&st, imei, protoName, c.RemoteAddr().String()) {
			return nil, true // IMEI not on the allowlist (FR-1.4)
		}
		s.registerConn(&st, c, models.ProtoXexun)

		if strings.Contains(frame, "GPRMC") || strings.Contains(frame, "GNRMC") {
			framesTotal.WithLabelValues(protoName, "position").Inc()
			tele, ok := parseGPRMCSentence(frame)
			if !ok {
				tcpParseErrors.WithLabelValues(protoName).Inc()
				rejectedTotal.WithLabelValues("parse").Inc()
				return nil, false
			}
			applyXexunStatus(&tele, frame)
			s.publish(&st, tele, protoName)
			return nil, false
		}

		// Heartbeat / status sentence (`acc on`, `low battery`, `help me!`, ...).
		framesTotal.WithLabelValues(protoName, "status").Inc()
		if !st.authenticated() {
			unsupportedFrames.WithLabelValues(protoName).Inc()
			return nil, false
		}
		tele := models.TelemetryMessage{Timestamp: time.Now().Unix()}
		applyXexunStatus(&tele, frame)
		s.publish(&st, tele, protoName)
		return nil, false
	})
}

// applyXexunStatus maps the documented Xexun status strings onto the canonical
// fields (docs/docs-device/traccar-reference/02b-xexun.md "Status Strings").
// Unknown strings leave the ACC tri-state untouched (never inferred).
func applyXexunStatus(t *models.TelemetryMessage, frame string) {
	lower := strings.ToLower(frame)
	switch {
	case strings.Contains(lower, "acc on"), strings.Contains(lower, "accstart"):
		t.ACC = models.BoolPtr(true)
	case strings.Contains(lower, "acc off"), strings.Contains(lower, "accstop"):
		t.ACC = models.BoolPtr(false)
	}
	if strings.Contains(lower, "help me") {
		// SOS is an alert concern (worker-alert B3): the frame is marked with the
		// GT06 SOS alarm reason so the same life-cycle applies to every family.
		t.AlarmCode = 0x01
	}
}
