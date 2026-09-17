package controllers

import (
	"bufio"
	"io"
	"log/slog"
	"net"
	"time"

	"adatrack_gps/ingestion-tcp/models"
)

// handleGT06 serves one GT06/Concox device connection: login (tenant resolution +
// anti-spoofing), position, heartbeat, alarm, time-check and info packets.
func (s *Server) handleGT06(c net.Conn) {
	protoName := models.ProtoGT06.String()
	defer s.connClose(c, protoName)

	r := bufio.NewReader(c)
	var (
		imei      string
		company   string
		vehicleID int64
	)

	for {
		packet, err := s.readDeadlined(c, r)
		if err != nil {
			if err != io.EOF {
				tcpParseErrors.WithLabelValues(protoName).Inc()
				slog.Debug("gt06 read ended", "remote", c.RemoteAddr(), "imei", imei, "error", err)
			}
			return
		}

		switch packet.Protocol {
		case models.ProtoLogin:
			framesTotal.WithLabelValues(protoName, "login").Inc()
			deviceIMEI := ParseLoginIMEI(packet.Data)
			dev, resolveErr := s.tenants.ResolveDeviceByIMEI(s.ctx, deviceIMEI)
			if resolveErr != nil {
				// Anti-spoofing (FR-1.4): only IMEIs registered in
				// master.tm_vehicle_imei_map are processed.
				slog.Warn("gt06: unauthorised IMEI rejected (anti-spoofing)",
					"imei", deviceIMEI, "remote", c.RemoteAddr(), "reason", resolveErr)
				rejectedTotal.WithLabelValues("unauthorised").Inc()
				_ = WriteAck(c, models.ProtoLogin, []byte{0x00, 0x00, 0x03}) // reject
				return
			}
			imei, company, vehicleID = deviceIMEI, dev.CompanyCode, dev.VehicleID
			slog.Info("gt06 authenticated", "imei", imei, "company", company,
				"vehicle_id", vehicleID, "remote", c.RemoteAddr())
			_ = WriteAck(c, models.ProtoLogin, []byte{0x00, 0x00, 0x00}) // accept

		case models.ProtoPosition, models.ProtoPosition2:
			framesTotal.WithLabelValues(protoName, "position").Inc()
			if imei == "" {
				slog.Warn("gt06: position before login", "remote", c.RemoteAddr())
				rejectedTotal.WithLabelValues("no_auth").Inc()
				return
			}
			tele, ok := ParsePosition(packet.Data)
			if !ok {
				tcpParseErrors.WithLabelValues(protoName).Inc()
				rejectedTotal.WithLabelValues("parse").Inc()
				slog.Warn("gt06: position parse failed", "imei", imei)
				continue
			}
			tele.IMEI, tele.CompanyCode, tele.VehicleID = imei, company, vehicleID
			if err := s.publishTelemetry(tele, protoName); err != nil {
				slog.Error("gt06: publish failed", "imei", imei, "error", err)
			}
			// Position ACK (0x05) echoes the information serial number.
			if len(packet.Data) >= 2 {
				_ = WriteAck(c, models.ProtoPositionAck, packet.Data[len(packet.Data)-2:])
			}

		case models.ProtoHeartbeat, models.ProtoHeartbeatEG:
			framesTotal.WithLabelValues(protoName, "heartbeat").Inc()
			_ = WriteAck(c, packet.Protocol, []byte{0x00})
			if imei != "" {
				if err := s.publishTelemetry(models.TelemetryMessage{
					IMEI: imei, CompanyCode: company, VehicleID: vehicleID,
					Timestamp: time.Now().Unix(),
				}, protoName); err != nil {
					slog.Error("gt06: heartbeat publish failed", "imei", imei, "error", err)
				}
			}

		case models.ProtoAlarm, models.ProtoAlarmHVT, models.ProtoAlarmLBS:
			framesTotal.WithLabelValues(protoName, "alarm").Inc()
			if imei == "" {
				slog.Warn("gt06: alarm before login", "remote", c.RemoteAddr())
				rejectedTotal.WithLabelValues("no_auth").Inc()
				continue
			}
			var tele models.TelemetryMessage
			if packet.Protocol == models.ProtoAlarmLBS {
				tele = ParseLBSAlarm(packet.Data, imei, company, vehicleID)
			} else {
				var ok bool
				tele, ok = ParseAlarm(packet.Data)
				if !ok {
					tcpParseErrors.WithLabelValues(protoName).Inc()
					rejectedTotal.WithLabelValues("parse").Inc()
					slog.Warn("gt06: alarm parse failed", "imei", imei, "proto", packet.Protocol)
					continue
				}
				tele.IMEI, tele.CompanyCode, tele.VehicleID = imei, company, vehicleID
			}
			if err := s.publishTelemetry(tele, protoName); err != nil {
				slog.Error("gt06: alarm publish failed", "imei", imei, "error", err)
			}
			if len(packet.Data) >= 2 {
				_ = WriteAck(c, packet.Protocol, packet.Data[len(packet.Data)-2:])
			}

		case models.ProtoTimeCheck:
			framesTotal.WithLabelValues(protoName, "time").Inc()
			_ = WriteAck(c, models.ProtoTimeCheck, EncodeTime6(time.Now().UTC()))

		case models.ProtoInfoTransmit:
			framesTotal.WithLabelValues(protoName, "info_transmit").Inc()
			if imei == "" {
				rejectedTotal.WithLabelValues("no_auth").Inc()
				continue
			}
			tele, ok := ParseInfoTransmit(packet.Data)
			if !ok {
				tcpParseErrors.WithLabelValues(protoName).Inc()
				slog.Debug("gt06: unsupported information transmission", "imei", imei)
				continue
			}
			tele.IMEI, tele.CompanyCode, tele.VehicleID = imei, company, vehicleID
			fuelReadingsTotal.WithLabelValues(protoName).Inc()
			if err := s.publishTelemetry(tele, protoName); err != nil {
				slog.Error("gt06: fuel publish failed", "imei", imei, "error", err)
			}

		default:
			framesTotal.WithLabelValues(protoName, "other").Inc()
			slog.Debug("gt06: unhandled protocol number", "proto", packet.Protocol, "imei", imei)
		}
	}
}
