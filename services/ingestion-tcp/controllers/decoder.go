package controllers

// decoder.go — pluggable protocol architecture (B9, PRD Module 1c).
//
// Every device family is a `Decoder`: it owns its listener port and the whole
// connection lifecycle. Adding a protocol means adding one type + one
// `RegisterDecoder` call — `server.go`, the publish path, the tenant allowlist
// and every downstream service (worker-live/persistence/alert/websocket) stay
// untouched, which is exactly the acceptance criterion of B9:
//
//	"Device non-GT06 bisa ingest end-to-end tanpa perubahan service lain."
//
// The protocol is selected by LISTENER (one port per family, PRD Module 1c table),
// never by sniffing the first bytes: a malformed stream can therefore never be
// mis-routed into a decoder that would misinterpret it.

import (
	"bufio"
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

// Decoder is one device wire protocol.
type Decoder interface {
	// Protocol is the identity used for metrics, logs and the port table.
	Protocol() models.Protocol
	// Port returns the configured listener port ("" / "0" disables it).
	Port(cfg *internal.Config) string
	// Serve handles one accepted connection until the device disconnects,
	// the idle timeout fires or the server shuts down.
	Serve(s *Server, c net.Conn)
}

// decoders is the protocol registry (populated at init time).
var decoders = map[models.Protocol]Decoder{}

// RegisterDecoder adds a decoder to the registry. Registering the same protocol
// twice panics: two implementations for one label would make metrics ambiguous.
func RegisterDecoder(d Decoder) {
	if d == nil {
		panic("controllers: nil decoder")
	}
	p := d.Protocol()
	if _, dup := decoders[p]; dup {
		panic(fmt.Sprintf("controllers: decoder for %s registered twice", p))
	}
	decoders[p] = d
}

// DecoderFor returns the registered decoder of a protocol.
func DecoderFor(p models.Protocol) (Decoder, bool) {
	d, ok := decoders[p]
	return d, ok
}

// RegisteredDecoders lists the registry sorted by protocol number (boot logging
// and the port-clash guard iterate this).
func RegisteredDecoders() []Decoder {
	out := make([]Decoder, 0, len(decoders))
	for _, d := range decoders {
		out = append(out, d)
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].Protocol() < out[i].Protocol() {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Shared session scaffolding
// ---------------------------------------------------------------------------

// session is the per-connection state a decoder fills from the login frame and
// reuses for every position frame (IMEI + resolved tenant/vehicle).
type session struct {
	imei      string
	company   string
	vehicleID int64
	// dc is the B8 downlink registration of this connection (nil until the login
	// handshake succeeded) so the decoder can unregister exactly once.
	dc *DeviceConn
}

// authenticated reports whether the login handshake completed.
func (st *session) authenticated() bool { return st.imei != "" }

// registerConn publishes the accepted socket in the B8 downlink registry. It is
// idempotent: a device that re-sends its login must not register twice.
func (s *Server) registerConn(st *session, c net.Conn, proto models.Protocol) {
	if st.dc != nil || st.imei == "" {
		return
	}
	st.dc = &DeviceConn{
		IMEI: st.imei, Protocol: proto, Remote: c.RemoteAddr().String(),
		ConnectedAt: time.Now().UTC(), conn: c,
	}
	s.conns.Add(st.dc)
	devicesOnline.Set(float64(s.conns.Len()))
}

// unregisterConn removes the downlink registration of a finished connection.
func (s *Server) unregisterConn(st *session) {
	if st.dc == nil {
		return
	}
	s.conns.Remove(st.dc)
	st.dc = nil
	devicesOnline.Set(float64(s.conns.Len()))
}

// login runs the anti-spoofing allowlist check (FR-1.4) for the IMEI a decoder
// extracted from its login frame. A rejected IMEI is logged + counted and the
// caller must close the connection.
func (s *Server) login(st *session, imei, protoName, remote string) bool {
	dev, err := s.tenants.ResolveDeviceByIMEI(s.ctx, imei)
	if err != nil {
		slog.Warn("ingestion: unauthorised IMEI rejected (anti-spoofing)",
			"protocol", protoName, "imei", imei, "remote", remote, "reason", err)
		rejectedTotal.WithLabelValues("unauthorised").Inc()
		return false
	}
	st.imei, st.company, st.vehicleID = imei, dev.CompanyCode, dev.VehicleID
	slog.Info("ingestion authenticated", "protocol", protoName, "imei", imei,
		"company", dev.CompanyCode, "vehicle_id", dev.VehicleID, "remote", remote)
	return true
}

// publish fills the tenant identity into a decoded message and publishes it to
// `telemetry.raw.<IMEI>` (FR-1.6). A publish failure is logged, never swallowed.
func (s *Server) publish(st *session, t models.TelemetryMessage, protoName string) {
	t.IMEI, t.CompanyCode, t.VehicleID = st.imei, st.company, st.vehicleID
	if t.Timestamp <= 0 {
		t.Timestamp = time.Now().Unix()
	}
	if err := s.publishTelemetry(t, protoName); err != nil {
		slog.Error("ingestion: publish failed", "protocol", protoName,
			"imei", st.imei, "error", err)
	}
}

// readLine reads one delimiter-terminated text frame with a hard length cap, so
// a hostile or broken device cannot make the server allocate without bound
// (PRD §9.6). The delimiter is included in the returned slice.
func readLine(r *bufio.Reader, delim byte, max int) ([]byte, error) {
	buf := make([]byte, 0, 128)
	for {
		b, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		buf = append(buf, b)
		if b == delim {
			return buf, nil
		}
		if len(buf) > max {
			return nil, fmt.Errorf("text frame exceeds %d bytes without delimiter 0x%02x", max, delim)
		}
	}
}

// readFrame reads exactly n bytes (bounded by max); the framing header must
// already have been consumed by the caller.
func readFrame(r *bufio.Reader, n, max int) ([]byte, error) {
	if n <= 0 || n > max {
		return nil, fmt.Errorf("invalid frame length %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// ---------------------------------------------------------------------------
// Shared decoding helpers (XOR checksum, NMEA, BCD, IMEI)
// ---------------------------------------------------------------------------

// xorChecksum returns the XOR of every byte (Meiligao, Xexun, Suntech, H02,
// Totem — docs/docs-device/traccar-reference/07-appendix.md).
func xorChecksum(data []byte) byte {
	var sum byte
	for _, b := range data {
		sum ^= b
	}
	return sum
}

// nmeaCoordinate converts an NMEA DDMM.mmmm / DDDMM.mmmm value plus the N/S/E/W
// indicator into signed decimal degrees.
func nmeaCoordinate(value, direction string) (float64, bool) {
	v := strings.TrimSpace(value)
	if v == "" {
		return 0, false
	}
	dot := strings.IndexByte(v, '.')
	if dot < 4 || dot > 5 {
		return 0, false
	}
	degLen := dot - 2
	deg, err := strconv.ParseFloat(v[:degLen], 64)
	if err != nil {
		return 0, false
	}
	min, err := strconv.ParseFloat(v[degLen:], 64)
	if err != nil {
		return 0, false
	}
	out := deg + min/60.0
	switch strings.ToUpper(strings.TrimSpace(direction)) {
	case "S", "W":
		out = -out
	}
	return out, true
}

// nmeaTimestamp converts the NMEA `hhmmss.sss` + `ddmmyy` pair to a Unix
// timestamp (UTC). Both fields are mandatory: a frame without them is rejected
// instead of being stored with a fabricated time.
func nmeaTimestamp(timeStr, dateStr string) (int64, bool) {
	ts := strings.TrimSpace(timeStr)
	ds := strings.TrimSpace(dateStr)
	if len(ts) < 6 || len(ds) != 6 {
		return 0, false
	}
	h, err1 := strconv.Atoi(ts[0:2])
	m, err2 := strconv.Atoi(ts[2:4])
	sec, err3 := strconv.Atoi(ts[4:6])
	d, err4 := strconv.Atoi(ds[0:2])
	mo, err5 := strconv.Atoi(ds[2:4])
	y, err6 := strconv.Atoi(ds[4:6])
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil || err5 != nil || err6 != nil {
		return 0, false
	}
	if h > 23 || m > 59 || sec > 59 || d < 1 || d > 31 || mo < 1 || mo > 12 {
		return 0, false
	}
	return time.Date(2000+y, time.Month(mo), d, h, m, sec, 0, time.UTC).Unix(), true
}

// bcdToIntN decodes a big-endian BCD byte slice (Meiligao/H02/Castel timestamps)
// on top of the GT06 bcdToInt (gt06frame.go), so every protocol shares ONE BCD
// decoder and cannot drift apart.
func bcdToIntN(data []byte) int {
	out := 0
	for _, b := range data {
		out = out*100 + bcdToInt(b)
	}
	return out
}

// hexIMEI normalises an 8-byte binary IMEI (GT02) into the 15-character ASCII
// form used by the allowlist: the first byte is the family marker.
func hexIMEI(raw []byte) string {
	s := fmt.Sprintf("%X", raw)
	if len(s) >= 16 {
		s = s[1:] // drop the family marker nibble
	}
	if len(s) > 15 {
		s = s[:15]
	}
	return s
}

// isIMEI reports whether s looks like a 15-digit IMEI (the only value the
// allowlist can resolve).
func isIMEI(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) != 15 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// parseFloatField parses a decimal field, returning ok=false on garbage.
func parseFloatField(s string) (float64, bool) {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// parseBoundedInt parses a base-10 int and rejects values outside [min,max].
func parseBoundedInt(s string, min, max int) (int, bool) {
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || v < min || v > max {
		return 0, false
	}
	return v, true
}

// writeAll writes a response frame and reports whether it succeeded (a failed
// write means the device is gone and the connection must be closed).
func writeAll(c net.Conn, frame []byte) bool {
	_, err := c.Write(frame)
	return err == nil
}
