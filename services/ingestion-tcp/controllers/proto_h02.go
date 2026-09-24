package controllers

// proto_h02.go — H02/H08 as a pluggable decoder (B9).
//
// Text mode (docs/docs-device/traccar-reference/02d-h02.md):
//
//	*<IMEI>,V3,<yyyyMMddHHmmss>,<A|V>,<lat>,<N|S>,<lon>,<E|W>,<speed>,<course>[,<status>]#
//
// Heartbeats are typed `V0`/`HTBT` and are answered by echoing the sentence up to
// the type with a trailing `#` (upstream H02ProtocolDecoder behaviour).
//
// Status bit 10 = ACC (upstream `processStatus` sets KEY_IGNITION from bit 10),
// so ACC stays tri-state: it is only published when the frame carries a status.
//
// The BINARY mode (`$<IMEI>,<len>,<cmd>,...`) documented in the same reference is
// NOT decoded yet — those frames are counted in `ingestion_unsupported_frames_total`
// instead of being guessed (honest B9 gap, see docs/B8-B10-VERIFICATION.md).

import (
	"net"
	"strconv"
	"strings"
	"time"

	"adatrack_gps/ingestion-tcp/models"
	"adatrack_gps/internal"
)

type h02Decoder struct{}

func (h02Decoder) Protocol() models.Protocol { return models.ProtoH02 }

func (h02Decoder) Port(cfg *internal.Config) string { return cfg.TCP.H02Port }

// Serve handles one H02 connection (text mode; the identity travels in every
// frame, so the first frame authenticates the device).
func (d h02Decoder) Serve(s *Server, c net.Conn) {
	protoName := models.ProtoH02.String()
	defer s.connClose(c, protoName)

	var st session
	defer s.unregisterConn(&st)

	s.serveLineProtocol(c, protoName, '#', 512, func(line []byte) ([]byte, bool) {
		frame := strings.TrimSpace(string(line))
		if frame == "" {
			return nil, false
		}
		if strings.HasPrefix(frame, "$") {
			// Binary mode: documented but not decoded yet (explicit gap).
			framesTotal.WithLabelValues(protoName, "binary").Inc()
			unsupportedFrames.WithLabelValues(protoName).Inc()
			return nil, false
		}
		if !strings.HasPrefix(frame, "*") {
			rejectedTotal.WithLabelValues("parse").Inc()
			return nil, false
		}

		fields := strings.Split(strings.TrimPrefix(frame, "*"), ",")
		if len(fields) < 3 {
			rejectedTotal.WithLabelValues("parse").Inc()
			return nil, false
		}
		imei := strings.TrimSpace(fields[0])
		kind := strings.ToUpper(strings.TrimSpace(fields[1]))
		if !s.ensureAuth(&st, imei, protoName, c.RemoteAddr().String()) {
			return nil, true
		}
		s.registerConn(&st, c, models.ProtoH02)

		switch kind {
		case "V0", "HTBT":
			framesTotal.WithLabelValues(protoName, "heartbeat").Inc()
			if st.authenticated() {
				s.publish(&st, models.TelemetryMessage{Timestamp: time.Now().Unix()}, protoName)
			}
			// Documented heartbeat reply: echo the sentence up to the type.
			return []byte("*" + fields[0] + "," + fields[1] + "#"), false
		case "V3", "VP1":
			framesTotal.WithLabelValues(protoName, "position").Inc()
			tele, ok := parseH02V3(fields)
			if !ok {
				tcpParseErrors.WithLabelValues(protoName).Inc()
				rejectedTotal.WithLabelValues("parse").Inc()
				return nil, false
			}
			s.publish(&st, tele, protoName)
			return nil, false
		default:
			framesTotal.WithLabelValues(protoName, "other").Inc()
			unsupportedFrames.WithLabelValues(protoName).Inc()
			return nil, false
		}
	})
}

// parseH02V3 decodes the documented `V3` position sentence. Timestamp is
// `yyyyMMddHHmmss` (the upstream DATE_FORMAT), coordinates are NMEA
// `DDMM.mmmm` + hemisphere, speed is km/h and course is degrees.
func parseH02V3(f []string) (models.TelemetryMessage, bool) {
	var t models.TelemetryMessage
	if len(f) < 10 {
		return t, false
	}
	ts, ok := parseH02Timestamp(strings.TrimSpace(f[2]))
	if !ok {
		return t, false
	}
	lat, ok := nmeaCoordinate(f[4], f[5])
	if !ok {
		return t, false
	}
	lon, ok := nmeaCoordinate(f[6], f[7])
	if !ok {
		return t, false
	}
	speed, _ := parseFloatField(f[8])
	heading, _ := parseFloatField(f[9])

	t.Timestamp = ts
	t.Lat, t.Lon = lat, lon
	t.Speed = speed
	t.Heading = int16(heading)
	t.Fix = strings.EqualFold(strings.TrimSpace(f[3]), "A")

	if len(f) >= 11 {
		// Status is a decimal bitmask; bit 10 is the ACC/ignition line.
		if status, ok := parseBoundedInt(f[10], 0, 1<<30); ok {
			t.ACC = models.BoolPtr(status&(1<<10) != 0)
		}
	}
	return t, true
}

// parseH02Timestamp converts `yyyyMMddHHmmss` (UTC) to a Unix timestamp.
func parseH02Timestamp(s string) (int64, bool) {
	if len(s) < 14 {
		return 0, false
	}
	n, err := strconv.Atoi(s[:14])
	if err != nil {
		return 0, false
	}
	parts := []int{
		n / 10000000000, n / 100000000 % 100, n / 1000000 % 100,
		n / 10000 % 100, n / 100 % 100, n % 100,
	}
	y, mo, d, h, mi, sec := parts[0], parts[1], parts[2], parts[3], parts[4], parts[5]
	if y < 2000 || y > 2100 || mo < 1 || mo > 12 || d < 1 || d > 31 ||
		h > 23 || mi > 59 || sec > 59 {
		return 0, false
	}
	return time.Date(y, time.Month(mo), d, h, mi, sec, 0, time.UTC).Unix(), true
}
