package controllers

import (
	"bufio"
	"encoding/binary"
	"io"
	"log/slog"
	"net"
	"time"

	"adatrack_gps/ingestion-tcp/models"
)

// handleTeltonika serves one Teltonika (Codec 8/8E) device connection:
//
//  1. IMEI login packet → 0x01 reply
//  2. AVL data packets → record count reply
//
// The same tenant allowlist (tm_vehicle_imei_map) gates the handshake, so
// spoofed IMEIs are rejected before any data is accepted (FR-1.4).
func (s *Server) handleTeltonika(c net.Conn) {
	protoName := models.ProtoTeltonika.String()
	defer s.connClose(c, protoName)

	r := bufio.NewReader(c)
	_ = c.SetReadDeadline(time.Now().Add(s.cfg.TCP.IdleTimeout))
	imei, err := readTeltonikaIMEI(r)
	if err != nil {
		slog.Debug("teltonika: no IMEI / login timeout", "remote", c.RemoteAddr(), "error", err)
		return
	}

	dev, resolveErr := s.tenants.ResolveDeviceByIMEI(s.ctx, imei)
	if resolveErr != nil {
		slog.Warn("teltonika: unauthorised IMEI rejected (anti-spoofing)",
			"imei", imei, "remote", c.RemoteAddr(), "reason", resolveErr)
		rejectedTotal.WithLabelValues("unauthorised").Inc()
		return
	}
	company, vehicleID := dev.CompanyCode, dev.VehicleID

	// Acknowledge the IMEI packet (0x01) as per the AVL TCP handshake.
	if _, err := c.Write([]byte{0x01}); err != nil {
		return
	}
	slog.Info("teltonika authenticated", "imei", imei, "company", company,
		"vehicle_id", vehicleID, "remote", c.RemoteAddr())

	// B8: track the socket in the downlink registry (Teltonika has no documented
	// downlink encoder yet, so the dispatcher reports `failed: unsupported` for
	// it — but the registry must still reflect the real connection state).
	dc := &DeviceConn{
		IMEI: imei, Protocol: models.ProtoTeltonika,
		Remote: c.RemoteAddr().String(), ConnectedAt: time.Now().UTC(), conn: c,
	}
	s.conns.Add(dc)
	devicesOnline.Set(float64(s.conns.Len()))
	defer func() {
		s.conns.Remove(dc)
		devicesOnline.Set(float64(s.conns.Len()))
	}()

	for {
		_ = c.SetReadDeadline(time.Now().Add(s.cfg.TCP.IdleTimeout))
		payload, err := readTeltonikaAVLPacket(r)
		if err != nil {
			if err != io.EOF {
				tcpParseErrors.WithLabelValues(protoName).Inc()
				slog.Debug("teltonika: read ended", "imei", imei, "error", err)
			}
			return
		}
		framesTotal.WithLabelValues(protoName, "avl").Inc()

		msgs, perr := parseTeltonikaAVL(payload)
		if perr != nil {
			tcpParseErrors.WithLabelValues(protoName).Inc()
			rejectedTotal.WithLabelValues("parse").Inc()
			slog.Warn("teltonika: parse failed", "imei", imei, "error", perr)
			// Reply with 0 accepted records so the device retries/advances.
			writeTeltonikaAck(c, 0)
			continue
		}

		accepted := 0
		for i := range msgs {
			if msgs[i].Timestamp <= 0 {
				msgs[i].Timestamp = time.Now().Unix()
			}
			msgs[i].IMEI = imei
			msgs[i].CompanyCode = company
			msgs[i].VehicleID = vehicleID
			if err := s.publishTelemetry(msgs[i], protoName); err != nil {
				slog.Error("teltonika: publish failed", "imei", imei, "error", err)
				continue
			}
			accepted++
		}

		if !writeTeltonikaAck(c, accepted) {
			return
		}
	}
}

// writeTeltonikaAck sends the 4-byte record-count acknowledgement required by
// Codec 8/8E TCP; it reports whether the write succeeded.
func writeTeltonikaAck(c net.Conn, accepted int) bool {
	var reply [4]byte
	binary.BigEndian.PutUint32(reply[:], uint32(accepted))
	_, err := c.Write(reply[:])
	return err == nil
}
