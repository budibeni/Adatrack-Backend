package main

import (
	_ "net/http/pprof"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"backend/ingestion-tcp/internal/protocol/coban"
	"backend/ingestion-tcp/internal/protocol/gt06"
	"backend/ingestion-tcp/internal/protocol/h02"
	"backend/ingestion-tcp/internal/protocol/meitrack"
	"backend/ingestion-tcp/internal/protocol/teltonika"
	"backend/ingestion-tcp/internal/protocol/meiligao"
	"backend/ingestion-tcp/internal/protocol/xexun"
	"backend/ingestion-tcp/internal/protocol/suntech"
	"backend/ingestion-tcp/internal/protocol/totem"
	"backend/ingestion-tcp/internal/protocol/gt02"
	"backend/ingestion-tcp/internal/protocol/navigil"
	"backend/ingestion-tcp/internal/protocol/castel"
	"backend/ingestion-tcp/internal/server"
	"backend/internal/config"
	"backend/internal/dbclient"
	"backend/internal/logger"
	"backend/internal/natsclient"

	"github.com/nats-io/nats.go"
)

func main() {
	logger.InitLogger()
	cfg := config.Load()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := dbclient.Connect(ctx, cfg); err != nil {
		logger.Log.Error("FATAL DB", "err", err); os.Exit(1)
	}
	if err := natsclient.Connect(cfg); err != nil {
		logger.Log.Error("FATAL NATS", "err", err); os.Exit(1)
	}

	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	healthServer := &http.Server{Addr: ":8081", Handler: nil}
	go healthServer.ListenAndServe()

	natsclient.NC.Subscribe("downlink.commands.*", func(m *nats.Msg) {
		subject := m.Subject
		// subject is downlink.commands.{imei}
		imei := subject[len("downlink.commands."):]

		dc, ok := server.GetDeviceConn(imei)
		if !ok {
			logger.Log.Warn("Device offline or connection not in this node", "imei", imei)
			return
		}

		type CmdMsg struct {
			Type   string            `json:"type"`
			Params map[string]string `json:"params"`
			Raw    string            `json:"raw"`
		}

		var cmdMsg CmdMsg
		if err := json.Unmarshal(m.Data, &cmdMsg); err != nil {
			logger.Log.Error("Invalid command message", "err", err)
			return
		}

		encodedBytes, err := dc.Decoder.EncodeCommand(cmdMsg.Type, cmdMsg.Params, cmdMsg.Raw)
		if err != nil {
			logger.Log.Error("Failed to encode command", "err", err, "imei", imei, "type", cmdMsg.Type)
			return
		}

		success := server.SendCommand(imei, encodedBytes)
		if success {
			logger.Log.Info("Command sent", "imei", imei, "type", cmdMsg.Type)
		} else {
			logger.Log.Warn("Command failed to send", "imei", imei)
		}
	})

	// Traccar-style Port Binding using Dynamic Ports from ENV
	var wg sync.WaitGroup
	servers := []*server.TCPServer{
		server.NewTCPServer(":"+cfg.PortGT06, 5000, &gt06.Decoder{}),
		server.NewTCPServer(":"+cfg.PortTeltonika, 5000, &teltonika.Decoder{}),
		// H-06 Enterprise Hardening: Disabled experimental / non-production protocols
		// to prevent edge-case panics and instability. Must be rigorously tested before re-enabling.
		// server.NewTCPServer(":"+cfg.PortCoban, 5000, &coban.Decoder{}),
		// server.NewTCPServer(":"+cfg.PortMeitrack, 5000, &meitrack.Decoder{}),
		// server.NewTCPServer(":"+cfg.PortH02, 5000, &h02.Decoder{}),
		// server.NewTCPServer(":"+cfg.PortMeiligao, 5000, &meiligao.Decoder{}),
		// server.NewTCPServer(":"+cfg.PortXexun, 5000, &xexun.Decoder{}),
		// server.NewTCPServer(":"+cfg.PortSuntech, 5000, &suntech.Decoder{}),
		// server.NewTCPServer(":"+cfg.PortTotem, 5000, &totem.Decoder{}),
		// server.NewTCPServer(":"+cfg.PortGT02, 5000, &gt02.Decoder{}),
		// server.NewTCPServer(":"+cfg.PortNavigil, 5000, &navigil.Decoder{}),
		// server.NewTCPServer(":"+cfg.PortCastel, 5000, &castel.Decoder{}),
	}

	for _, srv := range servers {
		wg.Add(1)
		go func(s *server.TCPServer) {
			defer wg.Done()
			if err := s.Start(); err != nil {
				logger.Log.Error("TCP Server crashed", "err", err)
			}
		}(srv)
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Log.Info("Initiating Graceful Shutdown across all protocols...")
	for _, srv := range servers {
		srv.Stop()
	}
	wg.Wait()
	
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	healthServer.Shutdown(shutdownCtx)
	
	dbclient.Pool.Close()
	natsclient.NC.Close()
	logger.Log.Info("Shutdown complete")
}
