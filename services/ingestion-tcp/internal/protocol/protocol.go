package protocol

import (
	"backend/internal/models"
)

// Decoder defines the universal contract for all GPS protocols (like Traccar architecture).
// By defining this interface, we can add hundreds of protocols (Coban, Meitrack, Ruptela, etc.)
// just by creating a new package that implements these methods, and binding it to a specific port.
type Decoder interface {
	// ProtocolName returns the string identifier (e.g., "GT06", "TELTONIKA")
	ProtocolName() string

	// DecodeLogin parses the first packet to identify the device IMEI.
	// Returns: IMEI string, Response bytes to send back (if any), Error
	DecodeLogin(data []byte) (string, []byte, error)

	// IsHeartbeat checks if the packet is a keep-alive/heartbeat.
	IsHeartbeat(data []byte) bool

	// GenerateHeartbeatResponse generates the protocol-specific ACK for a heartbeat.
	GenerateHeartbeatResponse(data []byte) []byte

	// DecodeLocation parses telemetry bytes into our universal TelemetryPayload.
	DecodeLocation(data []byte, imei, companyCode string, vehicleID int) (models.TelemetryPayload, error)
}
