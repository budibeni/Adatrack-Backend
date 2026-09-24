package controllers

// gt06decoder.go — GT06/Concox as a `Decoder` (B9 refactor of the B0 handler).
//
// The wire logic lives in handlergt06.go/gt06*.go unchanged; this adapter only
// wires the handler into the pluggable registry (one `Decoder` per protocol) and
// exposes its port + the GT06 downlink encoder (B8).

import (
	"net"

	"adatrack_gps/ingestion-tcp/models"
	"adatrack_gps/internal"
)

// gt06Decoder adapts the GT06 handler to the Decoder interface.
type gt06Decoder struct{}

func (gt06Decoder) Protocol() models.Protocol { return models.ProtoGT06 }

func (gt06Decoder) Port(cfg *internal.Config) string { return cfg.TCP.Port }

func (gt06Decoder) Serve(s *Server, c net.Conn) { s.handleGT06(c) }

// teltonikaDecoder adapts the Teltonika Codec 8/8E handler (own protocol
// reference, PRD Module 1b).
type teltonikaDecoder struct{}

func (teltonikaDecoder) Protocol() models.Protocol { return models.ProtoTeltonika }

func (teltonikaDecoder) Port(cfg *internal.Config) string { return cfg.TCP.TeltonikaPort }

func (teltonikaDecoder) Serve(s *Server, c net.Conn) { s.handleTeltonika(c) }

func init() {
	RegisterDecoder(gt06Decoder{})
	RegisterDecoder(teltonikaDecoder{})
}
