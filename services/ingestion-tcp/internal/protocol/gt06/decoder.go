package gt06

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"backend/internal/models"
)

const (
	ProtocolLogin    = 0x01
	ProtocolLocation = 0x12 
	ProtocolHeartbeat= 0x13
	ProtocolFuel     = 0x0D
)

type Decoder struct{}

func (d *Decoder) ProtocolName() string {
	return "GT06"
}

func (d *Decoder) DecodeLogin(data []byte) (string, []byte, error) {
	if len(data) < 18 {
		return "", nil, errors.New("packet too short")
	}
	if data[0] != 0x78 || data[1] != 0x78 {
		return "", nil, errors.New("invalid gt06 header")
	}
	
	imeiBytes := data[4:12]
	imei := hex.EncodeToString(imeiBytes)
	if imei[0] == '0' && len(imei) > 15 {
		imei = imei[1:16]
	}

	serial := data[12:14]
	response := []byte{0x78, 0x78, 0x05, ProtocolLogin, serial[0], serial[1], 0x00, 0x00, 0x0D, 0x0A}
	return imei, response, nil
}

func (d *Decoder) IsHeartbeat(data []byte) bool {
	return len(data) >= 4 && data[3] == ProtocolHeartbeat
}

func (d *Decoder) GenerateHeartbeatResponse(data []byte) []byte {
	if len(data) < 16 { return nil }
	serial := data[13:15]
	return []byte{0x78, 0x78, 0x05, ProtocolHeartbeat, serial[0], serial[1], 0x00, 0x00, 0x0D, 0x0A}
}

func (d *Decoder) DecodeLocation(data []byte, imei, companyCode string, vehicleID int) (models.TelemetryPayload, error) {
	if len(data) < 10 {
		return models.TelemetryPayload{}, errors.New("location packet too short")
	}
	
	dt := data[4:10]
	year := int(dt[0]) + 2000
	timestamp := time.Date(year, time.Month(dt[1]), int(dt[2]), int(dt[3]), int(dt[4]), int(dt[5]), 0, time.UTC)
	
	payload := models.TelemetryPayload{
		IMEI: imei, CompanyCode: companyCode, VehicleID: vehicleID,
		Timestamp: timestamp, RawData: hex.EncodeToString(data),
	}

	if data[3] == ProtocolFuel {
		// Fuel packet: !AILOIL,
		// Payload is ASCII
		if len(data) > 10 {
			strData := string(data[10:])
			if strings.HasPrefix(strData, "!AILOIL,") {
				parts := strings.Split(strData, ",")
				if len(parts) >= 2 {
					valStr := strings.TrimSpace(parts[1])
					// Handle possible trailing characters (like # or \r\n)
					idx := strings.IndexAny(valStr, "#\r\n\x00")
					if idx != -1 {
						valStr = valStr[:idx]
					}
					if val, err := strconv.ParseFloat(valStr, 64); err == nil {
						payload.FuelLevel = &val
					}
				}
			}
		}
		return payload, nil
	}

	if len(data) < 30 {
		return models.TelemetryPayload{}, errors.New("location packet too short for 0x12")
	}
	
	lat := float64(binary.BigEndian.Uint32(data[11:15])) / 1800000.0
	lon := float64(binary.BigEndian.Uint32(data[15:19])) / 1800000.0
	speed := float64(data[19])
	
	accStatus := int16(0)
	if len(data) >= 32 {
		termInfo := data[31]
		if (termInfo & 0x02) == 0x02 { accStatus = 1 }
	}

	payload.Latitude = lat
	payload.Longitude = lon
	payload.Speed = speed
	payload.ACCStatus = accStatus

	return payload, nil
}

func (d *Decoder) FrameSplitter() bufio.SplitFunc {
	return func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		if atEOF && len(data) == 0 { return 0, nil, nil }
		if len(data) < 4 { return 0, nil, nil } // Wait for more data
		
		// Ensure header matches GT06 (0x78 0x78 or 0x79 0x79)
		if (data[0] != 0x78 || data[1] != 0x78) && (data[0] != 0x79 || data[1] != 0x79) {
			// Corrupt bytes, advance 1 to recover
			return 1, nil, nil 
		}
		
		pktLength := int(data[2])
		totalFrameLen := pktLength + 5
		
		if len(data) < totalFrameLen {
			return 0, nil, nil // Need more data for full frame
		}
		
		return totalFrameLen, data[:totalFrameLen], nil
	}
}
