package controllers

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net"
	"time"

	"ajb_gps/ingestion-tcp/models"
	"ajb_gps/internal"
	"ajb_gps/internal/tenant"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	connCount     = prometheus.NewGauge(prometheus.GaugeOpts{Name: "ingestion_connections_active", Help: "Active TCP connections"})
	tcpConnsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "tcp_connections_total", Help: "Total TCP connections since startup (PRD §8.1)",
	})
	packetsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ingestion_packets_total", Help: "TCP frames received",
	}, []string{"protocol"})
	parsedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ingestion_parsed_messages_total", Help: "Telemetry messages parsed",
	}, []string{"protocol"})
	rejectedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ingestion_rejected_total", Help: "Rejected (unauthorised/parse-fail) packets",
	}, []string{"reason"})
	natsPublishErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "nats_publish_errors_total", Help: "NATS publish failures per company (PRD §8.1)",
	}, []string{"company_code"})
	natsPublishDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "nats_publish_duration_ms",
		Help:    "NATS publish latency in ms per company/subject (PRD §8.1)",
		Buckets: prometheus.ExponentialBuckets(0.1, 2, 12),
	}, []string{"company_code", "subject"})
	backpressureDrops = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "backpressure_drops_total", Help: "Messages dropped due to backpressure (PRD §8.1 / FR-1.5)",
	})

	tenantMgr *tenant.Manager
	natsCli   *internal.NATSClient
	maxConns  chan struct{}
	ctx       context.Context
	cancel    context.CancelFunc
)

// Configure binds the TenantManager + NATS clients and the connection semaphore.
func Configure(mgr *tenant.Manager, nats *internal.NATSClient, maxConnections int) {
	tenantMgr = mgr
	natsCli = nats
	ctx, cancel = context.WithCancel(context.Background())
	maxConns = make(chan struct{}, maxConnections)
}

// Cancel triggers a graceful shutdown of the accept loop.
func Cancel() {
	if cancel != nil {
		cancel()
	}
}

// RegisterMetrics registers the ingestion-specific collectors on the registry.
func RegisterMetrics(reg *prometheus.Registry) {
	reg.MustRegister(connCount, tcpConnsTotal, packetsTotal, parsedTotal, rejectedTotal,
		natsPublishErrors, natsPublishDuration, backpressureDrops)
}

// AcceptLoop accepts connections on a listener bound to a specific protocol
// and dispatches each to its protocol handler.
func AcceptLoop(ln net.Listener, out chan net.Conn, proto models.Protocol) {
	protoName := proto.String()
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				slog.Warn("accept error", "error", err, "protocol", protoName)
				continue
			}
		}

		select {
		case maxConns <- struct{}{}:
			connCount.Inc()
			tcpConnsTotal.Inc()
			go handleConn(conn, proto)
		case <-ctx.Done():
			_ = conn.Close()
			return
		default:
			slog.Warn("max connections reached, rejecting", "remote", conn.RemoteAddr(), "protocol", protoName)
			rejectedTotal.WithLabelValues("max_conn").Inc()
			_ = conn.Close()
		}
	}
}

// connClose releases the connection semaphore and closes the connection.
// Shared by all protocol handlers.
func connClose(c net.Conn, proto string) {
	<-maxConns
	connCount.Dec()
	_ = c.Close()
	slog.Info("connection closed", "remote", c.RemoteAddr(), "protocol", proto)
}

// handleConn dispatches a connection to the protocol-specific handler loop.
func handleConn(c net.Conn, proto models.Protocol) {
	switch proto {
	case models.ProtoTeltonika:
		handleTeltonika(c)
	case models.ProtoTK103:
		handleTK103(c)
	default:
		handleGT06(c)
	}
}

// handleGT06 processes a single GT06/Concox device connection.
func handleGT06(c net.Conn) {
	defer func() {
		<-maxConns
		connCount.Dec()
		_ = c.Close()
	}()
	defer slog.Info("connection closed", "remote", c.RemoteAddr())

	r := bufio.NewReader(c)
	imei := ""
	company := ""
	var vehicleID int64
	for {
		_ = c.SetReadDeadline(time.Now().Add(models.IdleTimeout))
		packet, err := ReadPacket(r)
		if err != nil {
			if err == io.EOF {
				slog.Info("client closed connection", "remote", c.RemoteAddr(), "imei", imei)
			} else {
				slog.Info("read error / idle timeout", "remote", c.RemoteAddr(), "imei", imei, "error", err)
			}
			return
		}

		switch packet.Protocol {
		case models.ProtoLogin:
			deviceIMEI := ParseLoginIMEI(packet.Data)
			packetsTotal.WithLabelValues("login").Inc()
			// Tenant resolution (anti-spoofing, PRD FR-1.4): lookup IMEI →
			// company_code + vehicle_id via master.vehicle_imei_map.
			dev, resolveErr := tenantMgr.ResolveDeviceByIMEI(ctx, deviceIMEI)
			if resolveErr != nil {
				slog.Warn("unauthorised IMEI rejected (anti-spoofing)", "imei", deviceIMEI,
					"remote", c.RemoteAddr(), "reason", resolveErr)
				rejectedTotal.WithLabelValues("unauthorised").Inc()
				_ = WriteAck(c, models.ProtoLogin, []byte{0x00, 0x00, 0x03}) // reject
				return
			}
			imei = deviceIMEI
			company = dev.CompanyCode
			vehicleID = dev.VehicleID
			slog.Info("device authenticated", "imei", imei, "company", company,
				"vehicle_id", vehicleID, "remote", c.RemoteAddr())
			_ = WriteAck(c, models.ProtoLogin, []byte{0x00, 0x00, 0x00}) // accept

		case models.ProtoPosition, models.ProtoPosition2:
			if imei == "" {
				slog.Warn("position received before authentication", "remote", c.RemoteAddr())
				rejectedTotal.WithLabelValues("no_auth").Inc()
				return
			}
			tele, ok := ParsePosition(packet.Data)
			tele.IMEI = imei
			tele.CompanyCode = company
			tele.VehicleID = vehicleID
			packetsTotal.WithLabelValues("position").Inc()
			if !ok {
				rejectedTotal.WithLabelValues("parse").Inc()
				slog.Warn("failed to parse position", "imei", imei)
				continue
			}
			_ = publishTelemetry(tele)
			parsedTotal.WithLabelValues("position").Inc()
			// Reply with position serial ACK (0x05) echoing the last 2 bytes.
			if len(packet.Data) >= 2 {
				serial := packet.Data[len(packet.Data)-2:]
				_ = WriteAck(c, 0x05, serial)
			}

		case models.ProtoHeartbeat, models.ProtoHeartbeatEG:
			packetsTotal.WithLabelValues("heartbeat").Inc()
			// Echo the actual heartbeat protocol (0x13 default / 0x23 EG02/EG03).
			_ = WriteAck(c, packet.Protocol, []byte{0x00})
			if imei != "" {
				_ = publishTelemetry(models.TelemetryMessage{
					IMEI: imei, CompanyCode: company, VehicleID: vehicleID,
					Timestamp: time.Now().Unix(),
				})
			}

		case models.ProtoAlarm, models.ProtoAlarmHVT, models.ProtoAlarmLBS:
			packetsTotal.WithLabelValues("alarm").Inc()
			if imei == "" {
				slog.Warn("alarm received before authentication", "remote", c.RemoteAddr())
				rejectedTotal.WithLabelValues("no_auth").Inc()
				continue
			}
			var tele models.TelemetryMessage
			var ack bool
			if packet.Protocol == models.ProtoAlarmLBS {
				tele, ack = ParseLBSAlarm(packet.Data, imei, company, vehicleID)
			} else {
				tele, ack = ParseAlarm(packet.Data)
				tele.IMEI = imei
				tele.CompanyCode = company
				tele.VehicleID = vehicleID
			}
			if !ack {
				rejectedTotal.WithLabelValues("parse").Inc()
				slog.Warn("failed to parse alarm", "imei", imei, "proto", packet.Protocol)
				continue
			}
			_ = publishTelemetry(tele)
			parsedTotal.WithLabelValues("alarm").Inc()
			// Reply with the alarm protocol echoing the info serial number.
			if len(packet.Data) >= 2 {
				serial := packet.Data[len(packet.Data)-2:]
				_ = WriteAck(c, packet.Protocol, serial)
			}

		case models.ProtoTimeCheck:
			packetsTotal.WithLabelValues("time").Inc()
			// Reply with the current UTC time (server clock) per v3.1 §9.2.
			_ = WriteAck(c, models.ProtoTimeCheck, EncodeTime6(time.Now().UTC()))

		case models.ProtoInfoTransmit:
			packetsTotal.WithLabelValues("info_transmit").Inc()
			if imei == "" {
				slog.Warn("info transmit received before authentication", "remote", c.RemoteAddr())
				rejectedTotal.WithLabelValues("no_auth").Inc()
				continue
			}
			// B5a: fuel sensor data (Information Type 0x0D, v3.1 §10.1).
			tele, ok := ParseInfoTransmit(packet.Data)
			if !ok {
				fuelSensorError(imei, "unsupported info type or malformed payload")
				continue
			}
			tele.IMEI = imei
			tele.CompanyCode = company
			tele.VehicleID = vehicleID
			// Fuel-only packets are partial messages (no GPS fix) — publish
			// them on the same telemetry.raw.<IMEI> subject so downstream
			// workers (persistence/live/alert) can merge them.
			if err := publishTelemetry(tele); err != nil {
				slog.Error("failed to publish fuel telemetry", "imei", imei, "error", err)
				continue
			}
			parsedTotal.WithLabelValues("fuel_sensor").Inc()
			fuelReadingsTotal.WithLabelValues("gt06").Inc()
			// ACK with the info serial number echoed back.
			if len(packet.Data) >= 2 {
				serial := packet.Data[len(packet.Data)-2:]
				_ = WriteAck(c, packet.Protocol, serial)
			}

		default:
			packetsTotal.WithLabelValues("other").Inc()
		}
	}
}

// WriteAck writes a GT06 response frame: start(0x78,0x78), length, protocol,
// serial/content, CRC-16, stop(0x0d,0x0a). Uses the 1-byte length short frame.
func WriteAck(c net.Conn, proto byte, data []byte) error {
	// data here is the full Information Content (may include serial).
	frame := []byte{0x78, 0x78, byte(1 + len(data)), proto}
	frame = append(frame, data...)
	sum := crc16(frame[2:]) // CRC over length||proto||content||serial
	frame = append(frame, byte(sum>>8), byte(sum), models.FrameStop0, models.FrameStop1)
	_, err := c.Write(frame)
	return err
}

// ReadPacket reads a single GT06 frame and returns the protocol + payload.
// Supports both framings:
//   - 0x78 0x78 : 1-byte Packet Length (v1.8.1 / v3.1 base)
//   - 0x79 0x79 : 2-byte Packet Length (v3.1 §8.2.1 large replies)
//
// The frame checksum is validated as CRC-ITU over
// length||protocol||content||serial (matches the GT06 "Error Check" spec).
func ReadPacket(r *bufio.Reader) (Packet, error) {
	var p Packet
	b0, err := r.ReadByte()
	if err != nil {
		return p, err
	}
	b1, err := r.ReadByte()
	if err != nil {
		return p, err
	}
	// Determine length encoding from the start-bit pair.
	lengthBytes := 1
	switch {
	case b0 == models.FrameStartShort && b1 == models.FrameStartShort:
		// 1-byte length
	case b0 == models.FrameStartLong && b1 == models.FrameStartLong:
		// 2-byte length (v3.1 §8.2.1)
		lengthBytes = 2
	default:
		return p, fmt.Errorf("bad start bytes 0x%02x 0x%02x", b0, b1)
	}

	// Read length field then the remaining frame body.
	hdr := make([]byte, lengthBytes)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return p, err
	}
	var length int
	if lengthBytes == 1 {
		length = int(hdr[0])
	} else {
		length = int(binary.BigEndian.Uint16(hdr))
	}

	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return p, err
	}
	cks := make([]byte, 2)
	if _, err := io.ReadFull(r, cks); err != nil {
		return p, err
	}
	// Validate CRC-ITU over length||protocol||content||serial.
	crcData := make([]byte, 0, lengthBytes+length)
	crcData = append(crcData, hdr...)
	crcData = append(crcData, payload...)
	if have, want := binary.BigEndian.Uint16(cks), crc16(crcData); have != want {
		return p, fmt.Errorf("crc mismatch: have 0x%04x want 0x%04x", have, want)
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
	// The Information Serial Number is the last 2 bytes of the payload
	// (2-byte length variant has it right after the variable content). For
	// frames that carry one we expose it; primary callers use p.Data for parsing.
	return p, nil
}
