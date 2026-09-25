package protocol

import (
	"backend/internal/models"
	"bufio"
)

type Decoder interface {
	ProtocolName() string
	DecodeLogin(data []byte) (string, []byte, error)
	IsHeartbeat(data []byte) bool
	GenerateHeartbeatResponse(data []byte) []byte
	DecodeLocation(data []byte, imei, companyCode string, vehicleID int) (models.TelemetryPayload, error)
	EncodeCommand(cmdType string, params map[string]string, raw string) ([]byte, error)

	// FrameSplitter returns the bufio.SplitFunc used to bound network streams
	// solving the TCP Sticky Packets / Fragmentation issues natively.
	FrameSplitter() bufio.SplitFunc
}
