package gt02

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"time"

	"backend/internal/models"
)

type Decoder struct{}

func (d *Decoder) ProtocolName() string { return "GT02" }

func (d *Decoder) DecodeLogin(data []byte) (string, []byte, error) {
	if len(data) < 10 {
		return "", nil, errors.New("packet too short")
	}
	// Simplified IMEI extraction
	imei := hex.EncodeToString(data[4:12])
	return imei, nil, nil
}

func (d *Decoder) IsHeartbeat(data []byte) bool { return false }
func (d *Decoder) GenerateHeartbeatResponse(data []byte) []byte { return nil }

func (d *Decoder) DecodeLocation(data []byte, imei, companyCode string, vehicleID int) (models.TelemetryPayload, error) {
	if len(data) < 20 {
		return models.TelemetryPayload{}, errors.New("not a location packet")
	}
	
	// Assume data[11:15] is Lat, data[15:19] is Lng for GT02 binary format
	latRaw := binary.BigEndian.Uint32(data[11:15])
	lngRaw := binary.BigEndian.Uint32(data[15:19])
	
	lat := float64(latRaw) / 1800000.0
	lng := float64(lngRaw) / 1800000.0
	
	// Adjust for hemisphere bits if necessary (simplified here)
	
	speed := float64(data[19])

	return models.TelemetryPayload{
		IMEI:        imei,
		CompanyCode: companyCode,
		VehicleID:   vehicleID,
		Latitude:    lat,
		Longitude:   lng,
		Speed:       speed,
		Timestamp:   time.Now().UTC(),
		RawData:     hex.EncodeToString(data),
	}, nil
}

func (d *Decoder) FrameSplitter() bufio.SplitFunc {
	return func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		if atEOF && len(data) == 0 { return 0, nil, nil }
		for i := 0; i < len(data)-1; i++ {
			if data[i] == 0x0D && data[i+1] == 0x0A {
				return i + 2, data[:i+2], nil
			}
		}
		if atEOF { return len(data), data, nil }
		return 0, nil, nil
	}
}

func (d *Decoder) EncodeCommand(cmdType string, params map[string]string, raw string) ([]byte, error) {
	if cmdType == "custom" { return []byte(raw), nil }
	return nil, errors.New("command not supported for gt02")
}
