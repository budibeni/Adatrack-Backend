package navigil

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"time"

	"backend/internal/models"
)

type Decoder struct{}

func (d *Decoder) ProtocolName() string { return "NAVIGIL" }
func (d *Decoder) DecodeLogin(data []byte) (string, []byte, error) {
	if len(data) < 10 {
		return "", nil, errors.New("packet too short")
	}
	return hex.EncodeToString(data[2:8]), nil, nil
}
func (d *Decoder) IsHeartbeat(data []byte) bool                 { return false }
func (d *Decoder) GenerateHeartbeatResponse(data []byte) []byte { return nil }
func (d *Decoder) DecodeLocation(data []byte, imei, companyCode string, vehicleID int) (models.TelemetryPayload, error) {
	if len(data) < 24 {
		return models.TelemetryPayload{}, errors.New("not a location packet")
	}

	latRaw := binary.LittleEndian.Uint32(data[10:14])
	lonRaw := binary.LittleEndian.Uint32(data[14:18])
	speedRaw := binary.LittleEndian.Uint16(data[18:20])
	headingRaw := binary.LittleEndian.Uint16(data[20:22])

	lat := float64(int32(latRaw)) / 10000000.0
	lon := float64(int32(lonRaw)) / 10000000.0
	speed := float64(speedRaw) * 0.036
	heading := float64(headingRaw)

	return models.TelemetryPayload{
		IMEI:        imei,
		CompanyCode: companyCode,
		VehicleID:   vehicleID,
		Latitude:    lat,
		Longitude:   lon,
		Speed:       speed,
		Heading:     heading,
		Timestamp:   time.Now().UTC(),
		RawData:     hex.EncodeToString(data),
	}, nil
}
func (d *Decoder) FrameSplitter() bufio.SplitFunc {
	return func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		if atEOF && len(data) == 0 {
			return 0, nil, nil
		}
		if atEOF {
			return len(data), data, nil
		}
		return len(data), data, nil
	}
}
func (d *Decoder) EncodeCommand(cmdType string, params map[string]string, raw string) ([]byte, error) {
	if cmdType == "custom" {
		return []byte(raw), nil
	}
	return nil, errors.New("command not supported for navigil")
}
