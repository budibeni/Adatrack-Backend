package totem

import (
	"bufio"
	"bytes"
	"errors"
	"strconv"
	"strings"
	"time"

	"backend/internal/models"
)

type Decoder struct{}

func (d *Decoder) ProtocolName() string { return "TOTEM" }

func (d *Decoder) DecodeLogin(data []byte) (string, []byte, error) {
	str := string(data)
	if !strings.HasPrefix(str, "$$") {
		return "", nil, errors.New("invalid totem header")
	}
	parts := strings.Split(str, ",")
	if len(parts) < 2 {
		return "", nil, errors.New("invalid format")
	}
	// IMEI extraction
	return parts[0][2:], nil, nil
}

func (d *Decoder) IsHeartbeat(data []byte) bool                 { return false }
func (d *Decoder) GenerateHeartbeatResponse(data []byte) []byte { return nil }

func (d *Decoder) DecodeLocation(data []byte, imei, companyCode string, vehicleID int) (models.TelemetryPayload, error) {
	str := string(data)
	if !strings.Contains(str, "A") { // A = Valid GPS
		return models.TelemetryPayload{}, errors.New("not a valid location packet")
	}

	parts := strings.Split(str, ",")
	if len(parts) < 10 {
		return models.TelemetryPayload{}, errors.New("invalid format")
	}

	lat, _ := strconv.ParseFloat(parts[3], 64)
	if parts[4] == "S" {
		lat = -lat
	}
	lon, _ := strconv.ParseFloat(parts[5], 64)
	if parts[6] == "W" {
		lon = -lon
	}
	speed, _ := strconv.ParseFloat(parts[7], 64)
	heading, _ := strconv.ParseFloat(parts[8], 64)

	return models.TelemetryPayload{
		IMEI:        imei,
		CompanyCode: companyCode,
		VehicleID:   vehicleID,
		Latitude:    lat,
		Longitude:   lon,
		Speed:       speed,
		Heading:     heading,
		Timestamp:   time.Now().UTC(),
		RawData:     str,
	}, nil
}

func (d *Decoder) FrameSplitter() bufio.SplitFunc {
	return func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		if atEOF && len(data) == 0 {
			return 0, nil, nil
		}
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			return i + 1, data[:i+1], nil
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
	return nil, errors.New("command not supported for totem")
}
