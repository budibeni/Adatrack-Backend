package meiligao

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"time"

	"backend/internal/models"
)

type Decoder struct{}

func (d *Decoder) ProtocolName() string { return "MEILIGAO" }

func (d *Decoder) DecodeLogin(data []byte) (string, []byte, error) {
	if len(data) < 18 {
		return "", nil, errors.New("packet too short")
	}
	if data[0] != '@' || data[1] != '@' {
		return "", nil, errors.New("invalid header")
	}
	imei := hex.EncodeToString(data[4:11]) // 7 bytes IMEI (14 digits)
	// Response to login command 0x5000 is usually 0x4000
	return imei, nil, nil
}

func (d *Decoder) IsHeartbeat(data []byte) bool                 { return false }
func (d *Decoder) GenerateHeartbeatResponse(data []byte) []byte { return nil }

func (d *Decoder) DecodeLocation(data []byte, imei, companyCode string, vehicleID int) (models.TelemetryPayload, error) {
	if len(data) < 30 {
		return models.TelemetryPayload{}, errors.New("not a location packet")
	}

	// Example parsing based on Meiligao specs
	// data[0:2] = @@
	// data[2:4] = length
	// data[4:11] = IMEI
	// data[11:13] = Command (e.g. 0x9955)

	cmd := binary.BigEndian.Uint16(data[11:13])
	if cmd != 0x9955 && cmd != 0x9999 && cmd != 0x4101 {
		return models.TelemetryPayload{}, errors.New("not a location packet")
	}

	// Assuming data[13:17] is lat and [17:21] is lon
	latRaw := binary.BigEndian.Uint32(data[13:17])
	lonRaw := binary.BigEndian.Uint32(data[17:21])

	lat := float64(latRaw) / 1000000.0
	lon := float64(lonRaw) / 1000000.0
	speed := float64(data[21])

	return models.TelemetryPayload{
		IMEI:        imei,
		CompanyCode: companyCode,
		VehicleID:   vehicleID,
		Latitude:    lat,
		Longitude:   lon,
		Speed:       speed,
		Timestamp:   time.Now().UTC(),
		RawData:     hex.EncodeToString(data),
	}, nil
}

func (d *Decoder) FrameSplitter() bufio.SplitFunc {
	return func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		if atEOF && len(data) == 0 {
			return 0, nil, nil
		}
		for i := 0; i < len(data)-1; i++ {
			if data[i] == '\r' && data[i+1] == '\n' {
				return i + 2, data[:i+2], nil
			}
		}
		if atEOF {
			return len(data), data, nil
		}
		return 0, nil, nil
	}
}

func (d *Decoder) EncodeCommand(cmdType string, params map[string]string, raw string) ([]byte, error) {
	if cmdType == "custom" {
		return []byte(raw), nil
	}
	return nil, errors.New("command not supported for meiligao")
}
