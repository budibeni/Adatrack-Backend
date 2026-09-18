package teltonika

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"strconv"
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
	if codec != 0x08 && codec != 0x8E {
		return models.TelemetryPayload{}, errors.New("unsupported codec, only 0x08 and 0x8E supported")
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

	payload := models.TelemetryPayload{
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
	}

	// Default Fuel IO ID is 86 (can be overridden by TELTONIKA_IO_FUEL_LEVEL)
	fuelIOID := 86
	if envVal := os.Getenv("TELTONIKA_IO_FUEL_LEVEL"); envVal != "" {
		if v, err := strconv.Atoi(envVal); err == nil {
			fuelIOID = v
		}
	}
	fuelVolumeIOID := -1
	if envVal := os.Getenv("TELTONIKA_IO_FUEL_VOLUME"); envVal != "" {
		if v, err := strconv.Atoi(envVal); err == nil {
			fuelVolumeIOID = v
		}
	}
	fuelTempIOID := -1
	if envVal := os.Getenv("TELTONIKA_IO_FUEL_TEMP"); envVal != "" {
		if v, err := strconv.Atoi(envVal); err == nil {
			fuelTempIOID = v
		}
	}

	offset := 34
	if offset+2 <= len(data) {
		// eventIO := data[offset]
		offset++
		_ = int(data[offset])
		offset++

		processIO := func(id int, val float64) {
			if id == fuelIOID {
				fVal := val
				payload.FuelLevel = &fVal
			}
			if id == fuelVolumeIOID {
				fVal := val
				payload.FuelVolume = &fVal
			}
			if id == fuelTempIOID {
				fVal := val
				payload.FuelTempC = &fVal
			}
			// Driver Behavior Analysis (B8)
			if id == 253 && val > 0 { // Harsh Acceleration
				payload.EventCode = 1
			}
			if id == 254 && val > 0 { // Harsh Braking
				payload.EventCode = 2
			}
			if id == 252 && val > 0 { // Harsh Cornering
				payload.EventCode = 3
			}
			if id == 239 { // Ignition
				if val > 0 {
					payload.ACCStatus = 1
				} else {
					payload.ACCStatus = 0
				}
			}
		}

		// 1-byte IOs
		if offset < len(data) {
			n1 := int(data[offset])
			offset++
			for i := 0; i < n1 && offset+1 < len(data); i++ {
				id := int(data[offset])
				val := float64(data[offset+1])
				processIO(id, val)
				offset += 2
			}
		}
		// 2-byte IOs
		if offset < len(data) {
			n2 := int(data[offset])
			offset++
			for i := 0; i < n2 && offset+2 < len(data); i++ {
				id := int(data[offset])
				val := float64(binary.BigEndian.Uint16(data[offset+1 : offset+3]))
				processIO(id, val)
				offset += 3
			}
		}
		// 4-byte IOs
		if offset < len(data) {
			n4 := int(data[offset])
			offset++
			for i := 0; i < n4 && offset+4 < len(data); i++ {
				id := int(data[offset])
				val := float64(binary.BigEndian.Uint32(data[offset+1 : offset+5]))
				processIO(id, val)
				offset += 5
			}
		}
		// 8-byte IOs
		if offset < len(data) {
			n8 := int(data[offset])
			offset++
			for i := 0; i < n8 && offset+8 < len(data); i++ {
				id := int(data[offset])
				val := float64(binary.BigEndian.Uint64(data[offset+1 : offset+9]))
				processIO(id, val)
				offset += 9
			}
		}
	}

	return payload, nil
}

func (d *Decoder) FrameSplitter() bufio.SplitFunc {
	return func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		if atEOF && len(data) == 0 {
			return 0, nil, nil
		}
		if len(data) < 8 {
			return 0, nil, nil
		} // Wait for length bytes

		// Teltonika Login Check (starts with 2-byte IMEI length, e.g. 0x00 0x0F for 15)
		imeiLen := int(binary.BigEndian.Uint16(data[0:2]))
		if imeiLen > 10 && imeiLen < 25 && len(data) >= imeiLen+2 { // Looks like a login packet
			return imeiLen + 2, data[:imeiLen+2], nil
		}

		// Teltonika AVL Check (starts with 0x00 0x00 0x00 0x00)
		if data[0] == 0 && data[1] == 0 && data[2] == 0 && data[3] == 0 {
			dataLen := int(binary.BigEndian.Uint32(data[4:8]))
			totalFrame := dataLen + 12
			if len(data) < totalFrame {
				return 0, nil, nil
			}
			return totalFrame, data[:totalFrame], nil
		}

		// Unknown, advance 1
		return 1, nil, nil
	}
}
