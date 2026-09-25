package server

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync"
	"time"

	"backend/ingestion-tcp/internal/handler"
	"backend/ingestion-tcp/internal/metrics"
	"backend/ingestion-tcp/internal/protocol"
	"backend/ingestion-tcp/internal/publisher"
	"backend/internal/logger"
)

type TCPServer struct {
	addr         string
	maxConns     int
	decoder      protocol.Decoder
	connLimiter  chan struct{}
	ipConnCount  map[string]int
	mu           sync.Mutex
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
		ipConnCount: make(map[string]int),
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
			ipAddr := strings.Split(conn.RemoteAddr().String(), ":")[0]
			s.mu.Lock()
			if s.ipConnCount[ipAddr] >= 50 { // max 50 conns per IP
				s.mu.Unlock()
				<-s.connLimiter
				logger.Log.Warn("Max connections per IP reached. Shedding load.", "ip", ipAddr)
				metrics.ConnectionErrors.WithLabelValues(s.decoder.ProtocolName(), "ip_limit").Inc()
				conn.Close()
				continue
			}
			s.ipConnCount[ipAddr]++
			s.mu.Unlock()

			metrics.TotalConnections.WithLabelValues(s.decoder.ProtocolName()).Inc()
			metrics.ActiveConnections.WithLabelValues(s.decoder.ProtocolName()).Inc()
			s.wg.Add(1)
			go s.handleConnection(conn)
		default:
			metrics.ConnectionErrors.WithLabelValues(s.decoder.ProtocolName(), "global_limit").Inc()
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
	ipAddr := strings.Split(conn.RemoteAddr().String(), ":")[0]
	defer func() {
		conn.Close()
		s.mu.Lock()
		s.ipConnCount[ipAddr]--
		if s.ipConnCount[ipAddr] == 0 {
			delete(s.ipConnCount, ipAddr)
		}
		s.mu.Unlock()
		metrics.ActiveConnections.WithLabelValues(s.decoder.ProtocolName()).Dec()
		<-s.connLimiter
		s.wg.Done()
	}()

	ip := conn.RemoteAddr().String()
	var imei string
	var tenant handler.TenantInfo
	authenticated := false

	// Increase scanner buffer to 1MB to prevent bufio.ErrTooLong on bulk offline packets
	scanner := bufio.NewScanner(conn)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	scanner.Split(s.decoder.FrameSplitter())

	go func() {
		<-s.ctx.Done()
		conn.SetReadDeadline(time.Now()) // Force immediate timeout for graceful shutdown
	}()

	// Buffered channel/timeout handling natively supported by setting read deadlines inside loop if needed
	// but standard scanner loop blocks nicely.
	for {
		// Only set deadline if context is not yet done
		if s.ctx.Err() == nil {
			conn.SetReadDeadline(time.Now().Add(5 * time.Minute))
		}
		if !scanner.Scan() {
			err := scanner.Err()
			// Ignore read deadline errors during shutdown
			if err != nil && s.ctx.Err() == nil {
				logger.Log.Error("TCP Scanner read error", "ip", ip, "err", err)
			}
			if authenticated {
				UnregisterConnection(imei)
			}
			break
		}

		data := scanner.Bytes()
		metrics.BytesReceived.WithLabelValues(s.decoder.ProtocolName()).Add(float64(len(data)))
		
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
			RegisterConnection(imei, conn, s.decoder)
			if response != nil { conn.Write(response) }
			logger.Log.Info("Device authenticated", "imei", imei, "protocol", s.decoder.ProtocolName())
			continue
		}

		if s.decoder.IsHeartbeat(data) {
			resp := s.decoder.GenerateHeartbeatResponse(data)
			if resp != nil { conn.Write(resp) }
			continue
		}

		payload, err := s.decoder.DecodeLocation(data, imei, tenant.CompanyCode, tenant.VehicleID)
		if err != nil {
			logger.Log.Warn("Invalid location packet", "imei", imei, "err", err)
			continue
		}
		
		// Send ACK if protocol requires it for location packets (e.g. Teltonika)
		if responder, ok := s.decoder.(interface{ GenerateLocationResponse([]byte) []byte }); ok {
			resp := responder.GenerateLocationResponse(data)
			if resp != nil {
				conn.Write(resp)
			}
		}

		if err := publisher.PublishTelemetry(payload); err != nil {
			logger.Log.Error("Publish failed", "imei", imei, "err", err)
		}
	}
}
