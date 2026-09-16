package teltonika

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"time"

	"backend/internal/models"
)

// Teltonika Codec 8/8E Implementation
type Decoder struct{}

func (d *Decoder) ProtocolName() string {
	return "TELTONIKA"
}

func (d *Decoder) DecodeLogin(data []byte) (string, []byte, error) {
	// Teltonika login starts with 2 bytes length of IMEI
	if len(data) < 17 {
		return "", nil, errors.New("teltonika login too short")
	}
	
	imeiLength := int(binary.BigEndian.Uint16(data[0:2]))
	if len(data) < 2+imeiLength {
		return "", nil, errors.New("invalid imei length")
	}
	
	imei := string(data[2 : 2+imeiLength])
	
	// Server responds with 0x01 to accept connection
	response := []byte{0x01}
	
	return imei, response, nil
}

func (d *Decoder) IsHeartbeat(data []byte) bool {
	// Teltonika doesn't use distinct heartbeat packets; it sends AVL data directly.
	return false 
}

func (d *Decoder) GenerateHeartbeatResponse(data []byte) []byte {
	return nil
}

func (d *Decoder) DecodeLocation(data []byte, imei, companyCode string, vehicleID int) (models.TelemetryPayload, error) {
	// Teltonika Codec 8 starts with 4 zeros, 4 bytes Data Length, 1 byte Codec ID (0x08)
	if len(data) < 45 {
		return models.TelemetryPayload{}, errors.New("avl packet too short")
	}
	
	if data[0] != 0 && data[1] != 0 && data[2] != 0 && data[3] != 0 {
		return models.TelemetryPayload{}, errors.New("invalid codec 8 header")
	}

	// Codec ID is at byte 8
	codec := data[8]
	if codec != 0x08 {
		return models.TelemetryPayload{}, errors.New("unsupported codec, only 0x08 for now")
	}

	// Number of Data records at byte 9
	// Timestamp starts at byte 10 (8 bytes, in milliseconds)
	tsMs := binary.BigEndian.Uint64(data[10:18])
	timestamp := time.UnixMilli(int64(tsMs)).UTC()

	// Priority at byte 18
	// Longitude at byte 19 (4 bytes)
	lonInt := int32(binary.BigEndian.Uint32(data[19:23]))
	lon := float64(lonInt) / 10000000.0

	// Latitude at byte 23 (4 bytes)
	latInt := int32(binary.BigEndian.Uint32(data[23:27]))
	lat := float64(latInt) / 10000000.0

	// Altitude at byte 27 (2 bytes)
	altitude := float64(binary.BigEndian.Uint16(data[27:29]))
	
	// Angle at byte 29 (2 bytes)
	heading := float64(binary.BigEndian.Uint16(data[29:31]))
	
	// Satellites at byte 31 (1 byte)
	// Speed at byte 32 (2 bytes)
	speed := float64(binary.BigEndian.Uint16(data[32:34]))

	return models.TelemetryPayload{
		IMEI:        imei,
		CompanyCode: companyCode,
		VehicleID:   vehicleID,
		Latitude:    lat,
		Longitude:   lon,
		Altitude:    altitude,
		Heading:     heading,
		Speed:       speed,
		Timestamp:   timestamp,
		RawData:     hex.EncodeToString(data),
	}, nil
}
