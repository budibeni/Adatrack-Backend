package controllers

// proto_totem.go — Totem as a pluggable decoder (B9).
//
// Reference: docs/docs-device/traccar-reference/03-priority-medium.md §3.1 and the
// upstream TotemProtocolDecoder, whose two text patterns are both implemented here:
//
//	PATTERN_1 (GPRMC):
//	$$<len>|<IMEI>|<alarm>$GPRMC,<time>,<A/V>,<lat>,<N>,<lon>,<E>,<speed>,<course>,<date>*
//	         |<pdop>|<hdop>|<vdop>|<io>|<battery>|<power>|<adc>|<lac>|<cid>|<temp>|<odo>|<serial><cs>
//
//	PATTERN_2 (pipe-delimited, no GPRMC):
//	$$<len>|<IMEI>|<alarm><DDMMYY><HHMMSS>|<A/V>|<lat DDMM.MMMM>|<N/S>|<lon DDDMM.MMMM>|<E/W>|
//	         <speed>|<course>|<hdop>|<io>|<battery>|<power>|<adc>|<lac>|<temp>|<odometer>|<serial>
//
// Only these two text forms are decoded; the binary/OTHER frames are counted in
// `ingestion_unsupported_frames_total` instead of being guessed. The documented
// `ACK OK` response is returned for both.

import (
	"log/slog"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"adatrack_gps/ingestion-tcp/models"
	"adatrack_gps/internal"
)

// totemPattern2 is the upstream PATTERN_2 regex (field order verified verbatim from
// TotemProtocolDecoder). Groups:
//
//	1 IMEI | 2 alarm | 3-5 date | 6-8 time | 9 validity |
//	10-11 lat + 12 hemisphere | 13-14 lon + 15 hemisphere | 16 speed | 17 course
var totemPattern2 = regexp.MustCompile(
	`^\$\$[0-9A-Fa-f]{2}\|?` + // header + length (a pipe after the length is tolerated)
		`(\d+)\|` + // IMEI
		`(..)` + // alarm type
		`(\d{2})(\d{2})(\d{2})` + // date (DDMMYY)
		`(\d{2})(\d{2})(\d{2})\|` + // time (HHMMSS)
		`([AV])\|` + // validity
		`(\d+)(\d{2}\.\d+)\|` + // latitude (DDMM.MMMM)
		`([NS])\|` + // hemisphere
		`(\d+)(\d{2}\.\d+)\|` + // longitude (DDDMM.MMMM)
		`([EW])\|` + // hemisphere
		`(\d+\.\d+)?\|` + // speed
		`(\d+)?\|` + // course
		`.*$`)

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
		// Identity: the IMEI sits either right after the `$$<len>` header (upstream
		// regex: `$$<2 hex><IMEI>|`) or in the first pipe field (in-repo doc layout).
		imei := totemIMEI(frame)
		if !s.ensureAuth(&st, imei, protoName, c.RemoteAddr().String()) {
			return nil, true
		}
		s.registerConn(&st, c, models.ProtoTotem)

		// PATTERN_1: a field that carries the embedded GPRMC sentence.
		if body, idx := totemGPRMCField(fields); body != "" {
			framesTotal.WithLabelValues(protoName, "position").Inc()
			tele, ok := parseGPRMCSentence(body)
			if !ok {
				tcpParseErrors.WithLabelValues(protoName).Inc()
				rejectedTotal.WithLabelValues("parse").Inc()
				return []byte("ACK OK"), false
			}
			// The battery/odometer pipe positions are only defined for the documented
			// layout (body in fields[2]); with the upstream layout they shift, so the
			// extras are applied only when the index proves the documented order.
			if idx == 2 {
				applyTotemExtras(&tele, fields)
			}
			s.publish(&st, tele, protoName)
			// Documented response for PATTERN_1.
			return []byte("ACK OK"), false
		}

		// PATTERN_2: pipe-delimited sentence without GPRMC.
		if tele, ok := parseTotemPattern2(frame); ok {
			framesTotal.WithLabelValues(protoName, "position").Inc()
			s.publish(&st, tele, protoName)
			return []byte("ACK OK"), false
		}

		framesTotal.WithLabelValues(protoName, "heartbeat").Inc()
		if st.authenticated() {
			s.publish(&st, models.TelemetryMessage{Timestamp: time.Now().Unix()}, protoName)
		}
		unsupportedFrames.WithLabelValues(protoName).Inc()
		slog.Debug("totem: frame not in PATTERN_1/PATTERN_2", "imei", imei, "fields", len(fields))
		return []byte("ACK OK"), false
	})
}

// totemIMEI extracts the device identity: the digit run between the `$$<len>`
// header and the first pipe (upstream layout) or the 15-digit IMEI of the
// documented layout. Returns "" when the frame carries neither, so the caller can
// reject it explicitly instead of treating it as an anonymous device.
func totemIMEI(frame string) string {
	if m := totemHeaderID.FindStringSubmatch(frame); m != nil {
		return m[1]
	}
	return parseIMEIPrefix(frame)
}

// totemHeaderID mirrors the identity part of the upstream patterns:
// `$$<2 hex>[|]<digits>|`.
var totemHeaderID = regexp.MustCompile(`^\$\$[0-9A-Fa-f]{2}\|?(\d{6,})\|`)

// totemGPRMCField finds the pipe field that carries the embedded NMEA sentence and
// returns it with its index (so the caller knows whether the documented layout is
// in play). Returns "" when the frame carries no GPRMC/GNRMC body.
func totemGPRMCField(fields []string) (string, int) {
	for i, f := range fields {
		if strings.Contains(f, "GPRMC") || strings.Contains(f, "GNRMC") {
			return f, i
		}
	}
	return "", -1
}

// parseTotemPattern2 decodes the pipe-delimited PATTERN_2 sentence.
//
// Coordinates use the NMEA convention (`DDMM.MMMM` + hemisphere, degrees =
// deg + min/60) exactly like PATTERN_1 and the upstream `third` format. The speed
// field is written into traccar's internal speed (knots) UNCONVERTED upstream, so
// it is read here as knots and converted to the pipeline's km/h — the same reading
// PATTERN_1's GPRMC field gets.
func parseTotemPattern2(frame string) (models.TelemetryMessage, bool) {
	var t models.TelemetryMessage
	m := totemPattern2.FindStringSubmatch(strings.TrimSpace(frame))
	if m == nil {
		return t, false
	}
	ts, ok := nmeaTimestamp(m[6]+m[7]+m[8], m[3]+m[4]+m[5])
	if !ok {
		return t, false
	}
	// The regex splits each coordinate into its degrees and `MM.MMMM` parts, so the
	// NMEA form nmeaCoordinate expects is the concatenation of both groups.
	lat, ok := nmeaCoordinate(m[10]+m[11], m[12])
	if !ok {
		return t, false
	}
	lon, ok := nmeaCoordinate(m[13]+m[14], m[15])
	if !ok {
		return t, false
	}

	t.Timestamp = ts
	t.Lat, t.Lon = lat, lon
	t.Fix = strings.EqualFold(m[9], "A")
	if m[16] != "" {
		if knots, err := strconv.ParseFloat(m[16], 64); err == nil {
			t.Speed = knots * 1.852 // upstream stores the raw value in a knots field
		}
	}
	if m[17] != "" {
		if course, err := strconv.ParseFloat(m[17], 64); err == nil {
			t.Heading = int16(course)
		}
	}
	// NOTE: the fields after the course (hdop/io/battery/power/adc/lac/temperature/
	// odometer) sit at DIFFERENT pipe positions than PATTERN_1, and the upstream code
	// writes them to non-canonical extended attributes only. They are therefore not
	// mapped here instead of being guessed into `battery`/`mileage`.
	return t, true
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
