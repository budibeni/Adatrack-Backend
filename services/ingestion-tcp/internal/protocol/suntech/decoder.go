package suntech

import (
	"bufio"
	"bytes"
	"errors"
	"strings"
	"time"

	"backend/internal/models"
)

type Decoder struct{}

func (d *Decoder) ProtocolName() string { return "SUNTECH" }

func (d *Decoder) DecodeLogin(data []byte) (string, []byte, error) {
	str := string(data)
	if !strings.HasPrefix(str, "S") {
		return "", nil, errors.New("invalid suntech header")
	}
	parts := strings.Split(str, ";")
	if len(parts) < 2 {
		return "", nil, errors.New("invalid suntech format")
	}
	imei := parts[1]
	if len(imei) < 5 {
		return "", nil, errors.New("invalid imei length")
	}
	return imei, nil, nil
}

func (d *Decoder) IsHeartbeat(data []byte) bool { return strings.Contains(string(data), "ALV") }
func (d *Decoder) GenerateHeartbeatResponse(data []byte) []byte { return nil }

func (d *Decoder) DecodeLocation(data []byte, imei, companyCode string, vehicleID int) (models.TelemetryPayload, error) {
	str := string(data)
	return models.TelemetryPayload{
		IMEI:        imei,
		CompanyCode: companyCode,
		VehicleID:   vehicleID,
		Timestamp:   time.Now().UTC(),
		RawData:     str,
	}, nil
}

func (d *Decoder) FrameSplitter() bufio.SplitFunc {
	return func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		if atEOF && len(data) == 0 { return 0, nil, nil }
		if i := bytes.IndexByte(data, '\r'); i >= 0 {
			if i+1 < len(data) && data[i+1] == '\n' {
				return i + 2, data[:i+2], nil
			}
			return i + 1, data[:i+1], nil
		}
		if atEOF { return len(data), data, nil }
		return 0, nil, nil
	}
}

func (d *Decoder) EncodeCommand(cmdType string, params map[string]string, raw string) ([]byte, error) {
	if cmdType == "custom" { return []byte(raw), nil }
	return nil, errors.New("command not supported for suntech")
}
