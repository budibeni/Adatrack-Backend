package controllers

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"net"
	"time"

	"ajb_gps/ingestion-tcp/models"
)

// ============================================================================
// TK-103 adapter — PROVISIONAL (honest).
//
// TK-103/10x adalah keluarga protokol Cina yang sangat terfragmentasi & tanpa
// satu spesifikasi baku yang diterbitkan secara terbuka. Repo ini TIDAK memuat
// dokumen vendor TK-103. Implementasi di bawah adalah SUBSET frame umum yang
// banyak gateway (mis. OpenGTS-class) pakai untuk perangkat TK-* "GT-clone":
//
//	frame : 0x78 0x78 | len(1) | cmd(1) | content | checksum(XOR) | 0x0D 0x0A
//
// Status: login/handshake + publish telemetry dasar. LAT/LON & alarm BELUM
// lengkap — wajib diverifikasi dgn dokument vendor & device nyata sebelum
// dipakai produksi (catat GAP di PRD Module 1a).
// ============================================================================

const (
	tkCmdLogin     = 0x12 // login / IMEI
	tkCmdHeartbeat = 0x13
	tkCmdPosition  = 0x22 // GT-clone position (reuse GT06 layout)
	tkCmdAlarm     = 0x26
)

// parseTK103Frame reads one TK-103 (GT-clone) frame and returns the command
// byte + payload. Uses a single XOR checksum byte before the stop bits (a
// common TK-103 variant).
func parseTK103Frame(r *bufio.Reader) (Packet, error) {
	var p Packet
	start, err := r.ReadByte()
	if err != nil {
		return p, err
	}
	if start != models.FrameStartShort { // 0x78
		return p, fmt.Errorf("tk103: bad start 0x%02x", start)
	}
	second, err := r.ReadByte()
	if err != nil {
		return p, err
	}
	if second != models.FrameStartShort {
		return p, fmt.Errorf("tk103: bad start2 0x%02x", second)
	}
	length, err := r.ReadByte()
	if err != nil {
		return p, err
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return p, err
	}
	// XOR checksum over header + payload (from start bytes through content).
	var x byte
	for _, b := range []byte{start, second, length} {
		x ^= b
	}
	for _, b := range payload {
		x ^= b
	}
	cks, err := r.ReadByte()
	if err != nil {
		return p, err
	}
	if cks != x {
		return p, fmt.Errorf("tk103: checksum mismatch")
	}
	// consume stop bits
	if _, err := r.ReadByte(); err != nil {
		return p, err
	}
	if _, err := r.ReadByte(); err != nil {
		return p, err
	}
	p.Protocol = payload[0]
	p.Data = payload[1:]
	return p, nil
}

// handleTK103 services a TK-103 (GT-clone) connection. PROVISIONAL.
func handleTK103(c net.Conn) {
	protoName := models.ProtoTK103.String()
	defer connClose(c, protoName)

	r := bufio.NewReader(c)
	imei, company, vehicleID := "", "", int64(0)

	for {
		_ = c.SetReadDeadline(time.Now().Add(models.IdleTimeout))
		packet, err := parseTK103Frame(r)
		if err != nil {
			if err != io.EOF {
				slog.Debug("tk103: read error", "remote", c.RemoteAddr(), "imei", imei, "error", err)
			}
			return
		}
		packetsTotal.WithLabelValues(protoName).Inc()

		switch packet.Protocol {
		case tkCmdLogin:
			devIMEI := ParseLoginIMEI(packet.Data)
			dev, resolveErr := tenantMgr.ResolveDeviceByIMEI(ctx, devIMEI)
			if resolveErr != nil {
				slog.Warn("tk103: unauthorised IMEI rejected", "imei", devIMEI, "reason", resolveErr)
				rejectedTotal.WithLabelValues("unauthorised").Inc()
				return
			}
			imei = devIMEI
			company = dev.CompanyCode
			vehicleID = dev.VehicleID
			_ = WriteAck(c, models.ProtoLogin, []byte{0x00, 0x00, 0x00}) // accept
			slog.Info("tk103 authenticated", "imei", imei, "company", company, "vehicle_id", vehicleID)
		case tkCmdHeartbeat:
			_ = WriteAck(c, 0x13, []byte{0x00})
		case tkCmdPosition, tkCmdAlarm:
			if imei == "" {
				rejectedTotal.WithLabelValues("no_auth").Inc()
				continue
			}
			var tele models.TelemetryMessage
			var ok bool
			if packet.Protocol == tkCmdAlarm {
				tele, ok = ParseAlarm(packet.Data)
			} else {
				tele, ok = ParsePosition(packet.Data)
			}
			if !ok {
				rejectedTotal.WithLabelValues("parse").Inc()
				slog.Warn("tk103: parse failed", "imei", imei)
				continue
			}
			tele.IMEI = imei
			tele.CompanyCode = company
			tele.VehicleID = vehicleID
			if publishTelemetry(tele) == nil {
				parsedTotal.WithLabelValues(protoName).Inc()
			}
			if len(packet.Data) >= 2 {
				_ = WriteAck(c, 0x05, packet.Data[len(packet.Data)-2:])
			}
		default:
			slog.Debug("tk103: unknown command", "cmd", packet.Protocol)
		}
	}
}
