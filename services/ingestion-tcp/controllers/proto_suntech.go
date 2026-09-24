package controllers

// proto_suntech.go — Suntech (ST215/ST240/ST340/ST440) framing + identity (B9 partial).
//
// Reference: docs/docs-device/traccar-reference/02c-suntech.md. The framing IS
// documented and implemented here (identity + anti-spoofing + metrics work), but
// the POSITION PAYLOAD is not described in either the in-repo reference or the
// reachable part of the upstream decoder. Those frames are therefore counted in
// `ingestion_unsupported_frames_total{protocol="suntech"}` and logged instead of
// being decoded with invented offsets — the explicit gap is recorded in
// docs/B8-B10-VERIFICATION.md §3.
//
//	text:   <payload_length>;<IMEI>;<Command>;<Data...>#<checksum>
//	binary: 0x02 | Length(2B) | Command(1B) | Payload | CRC(2B) | 0x03

import (
	"net"
	"strings"

	"adatrack_gps/ingestion-tcp/models"
	"adatrack_gps/internal"
)

type suntechDecoder struct{}

func (suntechDecoder) Protocol() models.Protocol { return models.ProtoSuntech }

func (suntechDecoder) Port(cfg *internal.Config) string { return cfg.TCP.SuntechPort }

// Serve handles one Suntech connection.
func (d suntechDecoder) Serve(s *Server, c net.Conn) {
	protoName := models.ProtoSuntech.String()
	defer s.connClose(c, protoName)

	var st session
	defer s.unregisterConn(&st)

	s.serveLineProtocol(c, protoName, '\n', 1024, func(line []byte) ([]byte, bool) {
		raw := strings.TrimSpace(string(line))
		if raw == "" {
			return nil, false
		}
		if strings.HasPrefix(raw, "\x02") {
			// Documented binary mode (ST340/ST440): framing known, payload not.
			framesTotal.WithLabelValues(protoName, "binary").Inc()
			unsupportedFrames.WithLabelValues(protoName).Inc()
			return nil, false
		}

		// Text mode: the checksum sits AFTER the '#' terminator, so everything from
		// '#' on is discarded (never strip trailing hex characters from the payload:
		// that would silently truncate real data).
		body := raw
		if hash := strings.IndexByte(raw, '#'); hash >= 0 {
			body = raw[:hash]
		}
		fields := strings.Split(body, ";")
		if len(fields) < 3 {
			rejectedTotal.WithLabelValues("parse").Inc()
			return nil, false
		}
		imei := strings.TrimSpace(fields[1])
		if !s.ensureAuth(&st, imei, protoName, c.RemoteAddr().String()) {
			return nil, true
		}
		s.registerConn(&st, c, models.ProtoSuntech)

		// The command/type prefix is recognised (docs §2c Text Mode Commands) but the
		// position field order is undocumented in-repo → counted, never guessed.
		framesTotal.WithLabelValues(protoName, "unsupported_payload").Inc()
		unsupportedFrames.WithLabelValues(protoName).Inc()
		return nil, false
	})
}
