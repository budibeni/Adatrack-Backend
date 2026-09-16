package main

import (
	"context"
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
	"backend/ingestion-tcp/internal/server"
	"backend/internal/config"
	"backend/internal/dbclient"
	"backend/internal/logger"
	"backend/internal/natsclient"
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

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	healthServer := &http.Server{Addr: ":8081", Handler: mux}
	go healthServer.ListenAndServe()

	// Traccar-style Port Binding for Universal Protocol Support
	var wg sync.WaitGroup
	servers := []*server.TCPServer{
		server.NewTCPServer(":9000", 5000, &gt06.Decoder{}),      // GT06, Concox, Jimilab
		server.NewTCPServer(":9001", 5000, &teltonika.Decoder{}), // Teltonika Codec 8/8E
		server.NewTCPServer(":9002", 5000, &coban.Decoder{}),     // Coban, TK103
		server.NewTCPServer(":9003", 5000, &meitrack.Decoder{}),  // Meitrack
		server.NewTCPServer(":9004", 5000, &h02.Decoder{}),       // H02, SinoTrack
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
