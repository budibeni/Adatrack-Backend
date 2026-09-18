package coban

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"backend/internal/models"
)

type Decoder struct{}

func (d *Decoder) ProtocolName() string { return "COBAN" }

func (d *Decoder) DecodeLogin(data []byte) (string, []byte, error) {
	str := string(data)
	if !strings.HasPrefix(str, "imei:") {
		return "", nil, errors.New("invalid coban login header")
	}
	parts := strings.Split(str, ",")
	if len(parts) == 0 {
		return "", nil, errors.New("invalid coban format")
	}
	imei := strings.TrimPrefix(parts[0], "imei:")
	if len(imei) < 15 {
		return "", nil, errors.New("invalid imei length")
	}
	return imei[:15], []byte("LOAD"), nil
}

func (d *Decoder) IsHeartbeat(data []byte) bool {
	return strings.Contains(string(data), "heartbeat")
}

func (d *Decoder) GenerateHeartbeatResponse(data []byte) []byte {
	return []byte("ON")
}

func parseNMEA(coord string, dir string) float64 {
	if len(coord) < 4 {
		return 0
	}
	// DDMM.MMMM
	idx := strings.Index(coord, ".")
	if idx < 2 {
		return 0
	}

	degStr := coord[:idx-2]
	minStr := coord[idx-2:]

	deg, _ := strconv.ParseFloat(degStr, 64)
	min, _ := strconv.ParseFloat(minStr, 64)

	val := deg + (min / 60.0)
	if dir == "S" || dir == "W" {
		val = -val
	}
	return val
}

func (d *Decoder) DecodeLocation(data []byte, imei, companyCode string, vehicleID int) (models.TelemetryPayload, error) {
	str := string(data)
	if !strings.Contains(str, "tracker") {
		return models.TelemetryPayload{}, errors.New("not a location packet")
	}

	// imei:...,tracker,210917,143000,F,035022.000,A,2232.1234,N,11404.1234,E,0.00,0.00...
	parts := strings.Split(str, ",")
	var lat, lng, speed float64
	for i := 0; i < len(parts); i++ {
		if parts[i] == "A" && i+4 < len(parts) {
			lat = parseNMEA(parts[i+1], parts[i+2])
			lng = parseNMEA(parts[i+3], parts[i+4])
			speed, _ = strconv.ParseFloat(parts[i+5], 64)
			break
		}
	}

	return models.TelemetryPayload{
		IMEI:        imei,
		CompanyCode: companyCode,
		VehicleID:   vehicleID,
		Latitude:    lat,
		Longitude:   lng,
		Speed:       speed,
		Timestamp:   time.Now().UTC(),
		RawData:     str,
	}, nil
}

func (d *Decoder) FrameSplitter() bufio.SplitFunc {
	return func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		if atEOF && len(data) == 0 {
			return 0, nil, nil
		}
		if i := bytes.IndexByte(data, ';'); i >= 0 {
			return i + 1, data[:i+1], nil
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
	if cmdType == "engine_stop" {
		return []byte(fmt.Sprintf("**,%s,C,DYD,%s#", params["imei"], params["password"])), nil
	}
	return nil, errors.New("command not supported for this protocol")
}
