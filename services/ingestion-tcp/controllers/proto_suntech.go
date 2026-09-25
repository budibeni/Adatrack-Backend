package controllers

// proto_suntech.go — Suntech (ST215/ST235/ST240/ST300STT) framing, identity and the
// classic TEXT POSITION sentence (B9).
//
// Reference: docs/docs-device/traccar-reference/02c-suntech.md plus the upstream
// SuntechProtocolDecoder, whose `universal` text pattern is available in full and
// is what this decoder implements:
//
//	<header>;<device id>;<version>;<YYYYMMDD>;<HH:MM:SS>;[cell;]<lat>;<lon>;<speed>;<course>;…
//	          ↑ 6+ digits (a 15-digit IMEI authenticates through the normal allowlist)
//
//	header   : S<one char><3 digits>[<3 word chars>]   e.g. ST215 / ST300STT
//	latitude : signed decimal degrees, 2 integer digits  (e.g. -06.20)
//	longitude: signed decimal degrees, 3 integer digits  (e.g. 106.80)
//	speed    : km/h (upstream converts km/h → knots, so the wire unit is km/h = ours)
//	course   : degrees, 3 integer + 2 decimal digits
//
// Only sentences matching this layout are decoded; everything else (binary mode
// 0x02/0x03, the newer per-model variants ST2xx/ST4xx/ST9xx, CRR crash reports, HTE
// travel reports) is counted in
// `ingestion_unsupported_frames_total{protocol="suntech"}` and logged — never guessed.

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

type suntechDecoder struct{}

func (suntechDecoder) Protocol() models.Protocol { return models.ProtoSuntech }

func (suntechDecoder) Port(cfg *internal.Config) string { return cfg.TCP.SuntechPort }

// suntechTextPattern mirrors the upstream `universal` pattern. The dots in the
// coordinate/speed groups are escaped on purpose (the upstream pattern uses `.` as
// "any char", which would accept garbage between the digits).
var suntechTextPattern = regexp.MustCompile(
	`^S[A-Za-z]\d{3}(?:[A-Za-z]{3})?;` + // header (ST215 / ST300STT)
		`(?:[^;]+;)?` + // optional extra field
		`(\d{6,});` + // device id (can be the 15-digit IMEI)
		`(?:\d+;)?` + // optional field
		`(\d+);` + // version
		`(\d{4})(\d{2})(\d{2});` + // date YYYYMMDD
		`(\d{2}):(\d{2}):(\d{2});` + // time HH:MM:SS
		`(?:[0-9A-Fa-f]+;)?` + // cell (optional, hex)
		`([-+]\d{2}\.\d+);` + // latitude  (decimal degrees)
		`([-+]\d{3}\.\d+);` + // longitude (decimal degrees)
		`(\d{3}\.\d{3});` + // speed (km/h)
		`(\d{3}\.\d{2});` + // course (degrees)
		`.*$`)

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
		if !st.authenticated() {
			return nil, false
		}
		s.registerConn(&st, c, models.ProtoSuntech)

		tele, ok := parseSuntechTextLine(body)
		if !ok {
			framesTotal.WithLabelValues(protoName, "unsupported_payload").Inc()
			unsupportedFrames.WithLabelValues(protoName).Inc()
			slog.Debug("suntech: sentence recognised but not the classic text layout",
				"imei", imei, "fields", len(fields), "prefix", fields[0])
			return nil, false
		}
		framesTotal.WithLabelValues(protoName, "position").Inc()
		s.publish(&st, tele, protoName)
		return nil, false
	})
}

// parseSuntechTextLine decodes the classic universal text sentence (see the file
// header). The device id must be a 15-digit IMEI: a 6-digit legacy id cannot be
// matched against the IMEI allowlist (FR-1.4), so such a sentence is reported as
// unsupported instead of being attributed to an arbitrary device.
func parseSuntechTextLine(body string) (models.TelemetryMessage, bool) {
	var t models.TelemetryMessage
	m := suntechTextPattern.FindStringSubmatch(body)
	if m == nil {
		return t, false
	}
	if !isIMEI(m[1]) {
		return t, false
	}
	ts, ok := suntechTextTime(m[3], m[4], m[5], m[6], m[7], m[8])
	if !ok {
		return t, false
	}
	lat, err := strconv.ParseFloat(m[9], 64)
	if err != nil {
		return t, false
	}
	lon, err := strconv.ParseFloat(m[10], 64)
	if err != nil {
		return t, false
	}
	// A coordinate outside the physical range means the sentence is not a position
	// report (or the device is un-fixed): count it, never publish it as telemetry.
	if lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return t, false
	}
	speed, err := strconv.ParseFloat(m[11], 64)
	if err != nil {
		return t, false
	}
	course, err := strconv.ParseFloat(m[12], 64)
	if err != nil {
		return t, false
	}

	t.Timestamp = ts
	t.Lat, t.Lon = lat, lon
	t.Speed = speed // already km/h (upstream converts km/h → knots, not the reverse)
	t.Heading = int16(course)
	t.Fix = true // the classic layout carries no A/V flag (upstream: valid = true)
	return t, true
}

// suntechTextTime builds the UTC timestamp from the separate date/time fields.
func suntechTextTime(year, month, day, hour, minute, second string) (int64, bool) {
	atoi := func(s string) (int, bool) {
		n, err := strconv.Atoi(s)
		return n, err == nil
	}
	y, ok1 := atoi(year)
	mo, ok2 := atoi(month)
	d, ok3 := atoi(day)
	h, ok4 := atoi(hour)
	mi, ok5 := atoi(minute)
	se, ok6 := atoi(second)
	if !(ok1 && ok2 && ok3 && ok4 && ok5 && ok6) {
		return 0, false
	}
	if mo < 1 || mo > 12 || d < 1 || d > 31 || h > 23 || mi > 59 || se > 60 {
		return 0, false
	}
	return time.Date(y, time.Month(mo), d, h, mi, se, 0, time.UTC).Unix(), true
}
