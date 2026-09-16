package server

import (
	"context"
	"io"
	"net"
	"sync"
	"time"

	"backend/ingestion-tcp/internal/handler"
	"backend/ingestion-tcp/internal/protocol/gt06"
	"backend/ingestion-tcp/internal/publisher"
	"backend/internal/logger"
)

type TCPServer struct {
	addr         string
	maxConns     int
	connLimiter  chan struct{}
	wg           sync.WaitGroup
	ctx          context.Context
	cancel       context.CancelFunc
}

func NewTCPServer(addr string, maxConns int) *TCPServer {
	ctx, cancel := context.WithCancel(context.Background())
	return &TCPServer{
		addr:        addr,
		maxConns:    maxConns,
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
	logger.Log.Info("TCP Ingestion Server started", "addr", s.addr, "maxConns", s.maxConns)

	go func() {
		<-s.ctx.Done()
		listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			if s.ctx.Err() != nil {
				return nil // Shutting down gracefully
			}
			logger.Log.Error("Failed to accept connection", "error", err)
			continue
		}

		select {
		case s.connLimiter <- struct{}{}:
			s.wg.Add(1)
			go s.handleConnection(conn)
		default:
			logger.Log.Warn("Max connections reached. Shedding load.", "ip", conn.RemoteAddr().String())
			conn.Close()
		}
	}
}

func (s *TCPServer) Stop() {
	logger.Log.Info("Stopping TCP Server. Waiting for connections to drain...")
	s.cancel()
	s.wg.Wait()
	logger.Log.Info("TCP Server stopped gracefully")
}

func (s *TCPServer) handleConnection(conn net.Conn) {
	defer func() {
		conn.Close()
		<-s.connLimiter
		s.wg.Done()
	}()

	ip := conn.RemoteAddr().String()
	logger.Log.Info("New device connection", "ip", ip)

	var imei string
	var tenant handler.TenantInfo
	authenticated := false

	buffer := make([]byte, 1024)
	for {
		conn.SetReadDeadline(time.Now().Add(5 * time.Minute)) // Idle timeout
		n, err := conn.Read(buffer)
		if err != nil {
			if err != io.EOF {
				logger.Log.Error("Read error", "ip", ip, "error", err)
			}
			break
		}

		data := buffer[:n]
		
		if len(data) >= 4 && data[3] == gt06.ProtocolLogin {
			deviceIMEI, response, err := gt06.DecodeLogin(data)
			if err != nil {
				logger.Log.Warn("Login parse failed", "ip", ip, "err", err)
				break
			}
			
			// Resolve tenant
			tenantData, err := handler.ResolveDevice(s.ctx, deviceIMEI)
			if err != nil {
				logger.Log.Warn("Unauthorized device", "imei", deviceIMEI, "err", err)
				break
			}
			
			imei = deviceIMEI
			tenant = tenantData
			authenticated = true
			conn.Write(response)
			logger.Log.Info("Device authenticated", "imei", imei, "tenant", tenant.CompanyCode)
			continue
		}

		if !authenticated {
			logger.Log.Warn("Unauthenticated payload dropped", "ip", ip)
			break
		}

		if gt06.IsHeartbeat(data) {
			resp := gt06.GenerateHeartbeatResponse(data)
			if resp != nil { conn.Write(resp) }
			continue
		}

		if len(data) >= 4 && (data[3] == gt06.ProtocolLocation || data[3] == 0x22) {
			payload, err := gt06.DecodeLocation(data, imei, tenant.CompanyCode, tenant.VehicleID)
			if err != nil {
				logger.Log.Warn("Invalid location packet", "imei", imei, "err", err)
				continue
			}
			
			if err := publisher.PublishTelemetry(payload); err != nil {
				logger.Log.Error("Publish failed", "imei", imei, "err", err)
				// Note: in enterprise we don't drop silently. NATS handles dead-letters.
			}
		}
	}
}
