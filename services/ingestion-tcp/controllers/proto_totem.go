package controllers

// proto_totem.go — Totem as a pluggable decoder (B9).
//
// Reference: docs/docs-device/traccar-reference/03-priority-medium.md §3.1 and the
// upstream TotemProtocolDecoder PATTERN_1 (the GPRMC-based text form):
//
//	$$<length>|<IMEI>|<alarm>$GPRMC,<time>,<A/V>,<lat>,<N>,<lon>,<E>,<speed>,<course>,<date>*<cs>|
//	<pdop>|<hdop>|<vdop>|<io>|<battery>|<power>|<adc>|<lac>|<cid>|<temp>|<odometer>|<serial><cs>
//
// Only PATTERN_1 is decoded: the pipe-delimited PATTERN_2 has no documented field
// order in either reference, so those frames are counted as unsupported rather
// than guessed. The NMEA body is decoded by the shared GPRMC parser (speed in
// knots → km/h), and the documented `ACK OK` response is returned.

import (
	"net"
	"strconv"
	"strings"
	"time"

	"adatrack_gps/ingestion-tcp/models"
	"adatrack_gps/internal"
)

type totemDecoder struct{}

func (totemDecoder) Protocol() models.Protocol { return models.ProtoTotem }

func (totemDecoder) Port(cfg *internal.Config) string { return cfg.TCP.TotemPort }

// Serve handles one Totem connection.
func (d totemDecoder) Serve(s *Server, c net.Conn) {
	protoName := models.ProtoTotem.String()
	defer s.connClose(c, protoName)

	var st session
	defer s.unregisterConn(&st)

	s.serveLineProtocol(c, protoName, '\n', 1024, func(line []byte) ([]byte, bool) {
		frame := strings.TrimSpace(string(line))
		if !strings.HasPrefix(frame, "$$") {
			return nil, false
		}
		fields := strings.Split(frame, "|")
		if len(fields) < 3 {
			rejectedTotal.WithLabelValues("parse").Inc()
			return nil, false
		}
		imei := strings.TrimSpace(fields[1])
		if !s.ensureAuth(&st, imei, protoName, c.RemoteAddr().String()) {
			return nil, true
		}
		s.registerConn(&st, c, models.ProtoTotem)

		if strings.Contains(fields[2], "GPRMC") || strings.Contains(fields[2], "GNRMC") {
			framesTotal.WithLabelValues(protoName, "position").Inc()
			tele, ok := parseGPRMCSentence(fields[2])
			if !ok {
				tcpParseErrors.WithLabelValues(protoName).Inc()
				rejectedTotal.WithLabelValues("parse").Inc()
				return []byte("ACK OK"), false
			}
			applyTotemExtras(&tele, fields)
			s.publish(&st, tele, protoName)
			// Documented response for PATTERN_1.
			return []byte("ACK OK"), false
		}

		framesTotal.WithLabelValues(protoName, "heartbeat").Inc()
		if st.authenticated() {
			s.publish(&st, models.TelemetryMessage{Timestamp: time.Now().Unix()}, protoName)
		}
		unsupportedFrames.WithLabelValues(protoName).Inc()
		return []byte("ACK OK"), false
	})
}

// applyTotemExtras fills the fields Totem appends after the NMEA sentence
// (battery, power, odometer — docs/docs-device/traccar-reference/03-priority-medium.md §3.1).
// Missing/blank fields are left absent (never coerced to zero).
func applyTotemExtras(t *models.TelemetryMessage, fields []string) {
	// Index mapping of PATTERN_1: 0=$$len, 1=imei, 2=NMEA, 3=pdop, 4=hdop,
	// 5=vdop, 6=io, 7=battery, 8=power, 9=adc, ...
	if len(fields) > 7 {
		if v, ok := parseBoundedInt(fields[7], 0, 100); ok {
			t.Battery = uint8(v)
		}
	}
	if len(fields) > 8 {
		// `power` is the external supply voltage — it has NO canonical field in the
		// telemetry contract (a voltage is not a fuel volume/level), so it stays
		// unpublished instead of being written into an unrelated column.
		_ = fields[8]
	}
	if len(fields) > 13 {
		if v, err := strconv.ParseUint(strings.TrimSpace(fields[13]), 10, 32); err == nil {
			t.Mileage = uint32(v)
		}
	}
}
