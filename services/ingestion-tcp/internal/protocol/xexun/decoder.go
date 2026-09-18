package xexun

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"strings"
	"time"

	"backend/internal/models"
)

type Decoder struct{}

func (d *Decoder) ProtocolName() string { return "XEXUN" }

func (d *Decoder) DecodeLogin(data []byte) (string, []byte, error) {
	// Xexun is text based. Format: GPRMC header with IMEI usually somewhere or imei: prefix
	str := string(data)
	if !strings.Contains(str, "imei:") {
		return "", nil, errors.New("invalid xexun login header")
	}
	parts := strings.Split(str, "imei:")
	if len(parts) < 2 {
		return "", nil, errors.New("invalid format")
	}
	imei := parts[1]
	if len(imei) > 15 {
		imei = imei[:15]
	}
	return imei, nil, nil
}

func (d *Decoder) IsHeartbeat(data []byte) bool {
	return false // Xexun uses regular messages
}

func (d *Decoder) GenerateHeartbeatResponse(data []byte) []byte { return nil }

func (d *Decoder) DecodeLocation(data []byte, imei, companyCode string, vehicleID int) (models.TelemetryPayload, error) {
	str := string(data)
	if !strings.Contains(str, "GPRMC") {
		return models.TelemetryPayload{}, errors.New("not a location packet")
	}
	
	// Example format: $GPRMC,081836,A,3751.65,S,14507.36,E,000.0,360.0,130998,011.3,E*62
	return models.TelemetryPayload{
		IMEI:        imei,
		CompanyCode: companyCode,
		VehicleID:   vehicleID,
		Latitude:    0, // To be extracted from NMEA
		Longitude:   0, // To be extracted from NMEA
		Timestamp:   time.Now().UTC(),
		RawData:     str,
	}, nil
}

func (d *Decoder) FrameSplitter() bufio.SplitFunc {
	return func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		if atEOF && len(data) == 0 { return 0, nil, nil }
		if i := bytes.IndexByte(data, '\n'); i >= 0 { return i + 1, data[:i+1], nil }
		if atEOF { return len(data), data, nil }
		return 0, nil, nil
	}
}

func (d *Decoder) EncodeCommand(cmdType string, params map[string]string, raw string) ([]byte, error) {
	if cmdType == "custom" { return []byte(raw), nil }
	if cmdType == "engine_stop" {
		return []byte(fmt.Sprintf("**,%s,J#", params["imei"])), nil
	}
	return nil, errors.New("command not supported for xexun")
}
