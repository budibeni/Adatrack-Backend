package gt06

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"backend/internal/models"
)

// Simplified GT06 Decoder for Enterprise demonstration.
// Actual implementations require handling dozens of packet types.
const (
	ProtocolLogin    = 0x01
	ProtocolLocation = 0x12 // Also 0x22 for some variants
	ProtocolHeartbeat= 0x13
)

// Decode extracts IMEI from a Login packet
func DecodeLogin(data []byte) (string, []byte, error) {
	if len(data) < 18 {
		return "", nil, errors.New("login packet too short")
	}
	if data[0] != 0x78 || data[1] != 0x78 {
		return "", nil, errors.New("invalid gt06 header")
	}
	
	// Extract 8-byte IMEI hex
	imeiBytes := data[4:12]
	imei := hex.EncodeToString(imeiBytes)
	if imei[0] == '0' && len(imei) > 15 {
		imei = imei[1:16] // Strip leading zero for standard 15-digit IMEI
	}

	// Generate Login Response (0x78 0x78 + length + protocol + serial + errorcheck + 0x0D 0x0A)
	serial := data[12:14]
	response := []byte{0x78, 0x78, 0x05, ProtocolLogin, serial[0], serial[1], 0x00, 0x00, 0x0D, 0x0A}
	// Note: CRC16 calculation omitted for brevity, assuming 0x00 0x00 placeholder.
	
	return imei, response, nil
}

// DecodeLocation parses telemetry bytes into standardized payload
func DecodeLocation(data []byte, imei, companyCode string, vehicleID int) (models.TelemetryPayload, error) {
	if len(data) < 30 {
		return models.TelemetryPayload{}, errors.New("location packet too short")
	}
	
	// Byte 4-9: Datetime
	dt := data[4:10]
	year := int(dt[0]) + 2000
	timestamp := time.Date(year, time.Month(dt[1]), int(dt[2]), int(dt[3]), int(dt[4]), int(dt[5]), 0, time.UTC)
	
	// Bytes 11-14: Latitude
	latRaw := binary.BigEndian.Uint32(data[11:15])
	lat := float64(latRaw) / 1800000.0 // GT06 standard math (1800000 = 30000 * 60)
	
	// Bytes 15-18: Longitude
	lonRaw := binary.BigEndian.Uint32(data[15:19])
	lon := float64(lonRaw) / 1800000.0
	
	// Byte 19: Speed
	speed := float64(data[19])
	
	// ACC Status (often buried in terminal info byte)
	accStatus := int16(0)
	if len(data) >= 32 {
		termInfo := data[31]
		if (termInfo & 0x02) == 0x02 { // Bit 1 is ACC
			accStatus = 1
		}
	}

	return models.TelemetryPayload{
		IMEI:        imei,
		CompanyCode: companyCode,
		VehicleID:   vehicleID,
		Latitude:    lat,
		Longitude:   lon,
		Speed:       speed,
		ACCStatus:   accStatus,
		Timestamp:   timestamp,
		RawData:     hex.EncodeToString(data),
	}, nil
}

func IsHeartbeat(data []byte) bool {
	return len(data) >= 4 && data[3] == ProtocolHeartbeat
}

func GenerateHeartbeatResponse(data []byte) []byte {
	if len(data) < 16 { return nil }
	serial := data[13:15]
	return []byte{0x78, 0x78, 0x05, ProtocolHeartbeat, serial[0], serial[1], 0x00, 0x00, 0x0D, 0x0A}
}
