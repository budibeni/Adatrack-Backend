package totem

import (
	"bufio"
	"bytes"
	"errors"
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

func (d *Decoder) IsHeartbeat(data []byte) bool { return false }
func (d *Decoder) GenerateHeartbeatResponse(data []byte) []byte { return nil }

func (d *Decoder) DecodeLocation(data []byte, imei, companyCode string, vehicleID int) (models.TelemetryPayload, error) {
	return models.TelemetryPayload{
		IMEI:        imei,
		CompanyCode: companyCode,
		VehicleID:   vehicleID,
		Timestamp:   time.Now().UTC(),
		RawData:     string(data),
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
	return nil, errors.New("command not supported for totem")
}
