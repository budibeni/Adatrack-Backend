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

type Decoder struct{}

func (d *Decoder) ProtocolName() string {
	return "TELTONIKA"
}

func (d *Decoder) DecodeLogin(data []byte) (string, []byte, error) {
	if len(data) < 17 {
		return "", nil, errors.New("teltonika login too short")
	}

	imeiLength := int(binary.BigEndian.Uint16(data[0:2]))
	if len(data) < 2+imeiLength {
		return "", nil, errors.New("invalid imei length")
	}

	imei := string(data[2 : 2+imeiLength])
	response := []byte{0x01}
	return imei, response, nil
}

func (d *Decoder) IsHeartbeat(data []byte) bool { return false }
func (d *Decoder) GenerateHeartbeatResponse(data []byte) []byte { return nil }

func crc16(data []byte) uint16 {
	crc := uint16(0)
	for _, b := range data {
		crc ^= uint16(b)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0xA001
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}

func (d *Decoder) GenerateLocationResponse(data []byte) []byte {
	if len(data) < 10 {
		return nil
	}
	// The number of records is at byte 9 (for Codec 8 / 8E, after header)
	// We return 4 bytes representing the number of records accepted
	records := int32(data[9])
	resp := make([]byte, 4)
	binary.BigEndian.PutUint32(resp, uint32(records))
	return resp
}

func (d *Decoder) DecodeLocation(data []byte, imei, companyCode string, vehicleID int) (models.TelemetryPayload, error) {
	if len(data) < 45 {
		return models.TelemetryPayload{}, errors.New("avl packet too short")
	}

	if data[0] != 0 && data[1] != 0 && data[2] != 0 && data[3] != 0 {
		return models.TelemetryPayload{}, errors.New("invalid codec header")
	}

	dataLen := int(binary.BigEndian.Uint32(data[4:8]))
	if len(data) < dataLen+12 {
		return models.TelemetryPayload{}, errors.New("packet length mismatch")
	}

	// Validate CRC
	expectedCRC := binary.BigEndian.Uint32(data[dataLen+8 : dataLen+12]) // CRC is 4 bytes
	actualCRC := crc16(data[8 : dataLen+8]) // calculated over data part
	// Traccar uses crc16 over the payload (bytes 8 to dataLen+8), and it is stored in 4 bytes at the end
	if uint32(actualCRC) != expectedCRC && expectedCRC != 0 {
		// Log but don't strictly reject if some devices have buggy firmware
		// Wait, the finding says "Abaikan validasi CRC (rentan korupsi data over TCP)". We MUST validate it.
		return models.TelemetryPayload{}, errors.New("crc validation failed")
	}

	codec := data[8]
	if codec != 0x08 && codec != 0x8E {
		return models.TelemetryPayload{}, errors.New("unsupported codec")
	}
	
	is8E := codec == 0x8E

	tsMs := binary.BigEndian.Uint64(data[10:18])
	timestamp := time.UnixMilli(int64(tsMs)).UTC()

	lonInt := int32(binary.BigEndian.Uint32(data[19:23]))
	lon := float64(lonInt) / 10000000.0

	latInt := int32(binary.BigEndian.Uint32(data[23:27]))
	lat := float64(latInt) / 10000000.0

	altitude := float64(binary.BigEndian.Uint16(data[27:29]))
	heading := float64(binary.BigEndian.Uint16(data[29:31]))
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

	fuelIOID := 86
	if envVal := os.Getenv("TELTONIKA_IO_FUEL_LEVEL"); envVal != "" {
		if v, err := strconv.Atoi(envVal); err == nil { fuelIOID = v }
	}
	fuelVolumeIOID := -1
	if envVal := os.Getenv("TELTONIKA_IO_FUEL_VOLUME"); envVal != "" {
		if v, err := strconv.Atoi(envVal); err == nil { fuelVolumeIOID = v }
	}
	fuelTempIOID := -1
	if envVal := os.Getenv("TELTONIKA_IO_FUEL_TEMP"); envVal != "" {
		if v, err := strconv.Atoi(envVal); err == nil { fuelTempIOID = v }
	}

	offset := 34
	
	readID := func() int {
		if is8E {
			if offset+2 > len(data) { return -1 }
			id := int(binary.BigEndian.Uint16(data[offset : offset+2]))
			offset += 2
			return id
		}
		if offset+1 > len(data) { return -1 }
		id := int(data[offset])
		offset += 1
		return id
	}

	if offset+2 <= len(data) {
		// Event IO ID
		readID()
		// N of Total IO
		if is8E { offset += 2 } else { offset += 1 }

		processIO := func(id int, val float64) {
			if id == fuelIOID { fVal := val; payload.FuelLevel = &fVal }
			if id == fuelVolumeIOID { fVal := val; payload.FuelVolume = &fVal }
			if id == fuelTempIOID { fVal := val; payload.FuelTempC = &fVal }
			if id == 253 && val > 0 { payload.EventCode = 1 }
			if id == 254 && val > 0 { payload.EventCode = 2 }
			if id == 252 && val > 0 { payload.EventCode = 3 }
			if id == 239 {
				if val > 0 { payload.ACCStatus = 1 } else { payload.ACCStatus = 0 }
			}
		}

		// 1-byte IOs
		if offset < len(data) {
			n1 := int(data[offset])
			if is8E {
			    if offset+2 > len(data) { return payload, errors.New("packet overflow") }
			    n1 = int(binary.BigEndian.Uint16(data[offset : offset+2]))
			    offset += 2
			} else {
			    offset++
			}
			for i := 0; i < n1 && offset+1 <= len(data); i++ {
				id := readID()
				if id == -1 || offset+1 > len(data) { return payload, errors.New("packet overflow") }
				val := float64(data[offset])
				processIO(id, val)
				offset += 1
			}
		}
		// 2-byte IOs
		if offset < len(data) {
			n2 := int(data[offset])
			if is8E {
			    if offset+2 > len(data) { return payload, errors.New("packet overflow") }
			    n2 = int(binary.BigEndian.Uint16(data[offset : offset+2]))
			    offset += 2
			} else {
			    offset++
			}
			for i := 0; i < n2 && offset+2 <= len(data); i++ {
				id := readID()
				if id == -1 || offset+2 > len(data) { return payload, errors.New("packet overflow") }
				val := float64(binary.BigEndian.Uint16(data[offset : offset+2]))
				processIO(id, val)
				offset += 2
			}
		}
		// 4-byte IOs
		if offset < len(data) {
			n4 := int(data[offset])
			if is8E {
			    if offset+2 > len(data) { return payload, errors.New("packet overflow") }
			    n4 = int(binary.BigEndian.Uint16(data[offset : offset+2]))
			    offset += 2
			} else {
			    offset++
			}
			for i := 0; i < n4 && offset+4 <= len(data); i++ {
				id := readID()
				if id == -1 || offset+4 > len(data) { return payload, errors.New("packet overflow") }
				val := float64(binary.BigEndian.Uint32(data[offset : offset+4]))
				processIO(id, val)
				offset += 4
			}
		}
		// 8-byte IOs
		if offset < len(data) {
			n8 := int(data[offset])
			if is8E {
			    if offset+2 > len(data) { return payload, errors.New("packet overflow") }
			    n8 = int(binary.BigEndian.Uint16(data[offset : offset+2]))
			    offset += 2
			} else {
			    offset++
			}
			for i := 0; i < n8 && offset+8 <= len(data); i++ {
				id := readID()
				if id == -1 || offset+8 > len(data) { return payload, errors.New("packet overflow") }
				val := float64(binary.BigEndian.Uint64(data[offset : offset+8]))
				processIO(id, val)
				offset += 8
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
		}
		imeiLen := int(binary.BigEndian.Uint16(data[0:2]))
		if imeiLen > 10 && imeiLen < 25 && len(data) >= imeiLen+2 {
			return imeiLen + 2, data[:imeiLen+2], nil
		}
		if data[0] == 0 && data[1] == 0 && data[2] == 0 && data[3] == 0 {
			dataLen := int(binary.BigEndian.Uint32(data[4:8]))
			totalFrame := dataLen + 12
			if len(data) < totalFrame {
				return 0, nil, nil
			}
			return totalFrame, data[:totalFrame], nil
		}
		return 1, nil, nil
	}
}

func encodeCodec12(raw string) []byte {
    // Stub
    return []byte(raw)
}

func (d *Decoder) EncodeCommand(cmdType string, params map[string]string, raw string) ([]byte, error) {
	if cmdType == "custom" {
		return encodeCodec12(raw), nil
	}
	if cmdType == "engine_stop" {
		return encodeCodec12("setdigout 1"), nil
	}
	if cmdType == "engine_resume" {
		return encodeCodec12("setdigout 0"), nil
	}
	return nil, errors.New("command not supported for teltonika")
}
