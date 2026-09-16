package server

import (
	"context"
	"io"
	"net"
	"sync"
	"time"

	"backend/ingestion-tcp/internal/handler"
	"backend/ingestion-tcp/internal/protocol"
	"backend/ingestion-tcp/internal/publisher"
	"backend/internal/logger"
)

type TCPServer struct {
	addr         string
	maxConns     int
	decoder      protocol.Decoder
	connLimiter  chan struct{}
	wg           sync.WaitGroup
	ctx          context.Context
	cancel       context.CancelFunc
}

func NewTCPServer(addr string, maxConns int, decoder protocol.Decoder) *TCPServer {
	ctx, cancel := context.WithCancel(context.Background())
	return &TCPServer{
		addr:        addr,
		maxConns:    maxConns,
		decoder:     decoder,
		connLimiter: make(chan struct{}, maxConns),
		ctx:         ctx,
		cancel:      cancel,
	}
}

func (s *TCPServer) Start() error {
	listener, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	logger.Log.Info("TCP Ingestion Server started", "protocol", s.decoder.ProtocolName(), "addr", s.addr)

	go func() {
		<-s.ctx.Done()
		listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			if s.ctx.Err() != nil {
				return nil 
			}
			logger.Log.Error("Failed to accept connection", "error", err)
			continue
		}

		select {
		case s.connLimiter <- struct{}{}:
			s.wg.Add(1)
			go s.handleConnection(conn)
		default:
			logger.Log.Warn("Max connections reached. Shedding load.", "ip", conn.RemoteAddr().String(), "protocol", s.decoder.ProtocolName())
			conn.Close()
		}
	}
}

func (s *TCPServer) Stop() {
	logger.Log.Info("Stopping TCP Server", "protocol", s.decoder.ProtocolName())
	s.cancel()
	s.wg.Wait()
}

func (s *TCPServer) handleConnection(conn net.Conn) {
	defer func() {
		conn.Close()
		<-s.connLimiter
		s.wg.Done()
	}()

	ip := conn.RemoteAddr().String()
	var imei string
	var tenant handler.TenantInfo
	authenticated := false

	buffer := make([]byte, 2048)
	for {
		conn.SetReadDeadline(time.Now().Add(5 * time.Minute))
		n, err := conn.Read(buffer)
		if err != nil {
			if err != io.EOF {
				logger.Log.Error("Read error", "ip", ip, "error", err)
			}
			break
		}

		data := buffer[:n]
		
		// Attempt Login if not authenticated
		if !authenticated {
			deviceIMEI, response, err := s.decoder.DecodeLogin(data)
			if err != nil {
				logger.Log.Warn("Login parse failed", "ip", ip, "protocol", s.decoder.ProtocolName(), "err", err)
				break
			}
			
			tenantData, err := handler.ResolveDevice(s.ctx, deviceIMEI)
			if err != nil {
				logger.Log.Warn("Unauthorized device", "imei", deviceIMEI, "err", err)
				break
			}
			
			imei = deviceIMEI
			tenant = tenantData
			authenticated = true
			if response != nil { conn.Write(response) }
			logger.Log.Info("Device authenticated", "imei", imei, "protocol", s.decoder.ProtocolName())
			continue
		}

		// Handle Heartbeat
		if s.decoder.IsHeartbeat(data) {
			resp := s.decoder.GenerateHeartbeatResponse(data)
			if resp != nil { conn.Write(resp) }
			continue
		}

		// Handle Telemetry Location
		payload, err := s.decoder.DecodeLocation(data, imei, tenant.CompanyCode, tenant.VehicleID)
		if err != nil {
			logger.Log.Warn("Invalid location packet", "imei", imei, "err", err)
			continue
		}
		
		if err := publisher.PublishTelemetry(payload); err != nil {
			logger.Log.Error("Publish failed", "imei", imei, "err", err)
		}
	}
}
