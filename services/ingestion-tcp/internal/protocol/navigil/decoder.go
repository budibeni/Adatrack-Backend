package navigil

import (
	"bufio"
	"encoding/hex"
	"errors"
	"time"

	"backend/internal/models"
)

type Decoder struct{}
func (d *Decoder) ProtocolName() string { return "NAVIGIL" }
func (d *Decoder) DecodeLogin(data []byte) (string, []byte, error) {
	if len(data) < 10 { return "", nil, errors.New("packet too short") }
	return hex.EncodeToString(data[2:8]), nil, nil
}
func (d *Decoder) IsHeartbeat(data []byte) bool { return false }
func (d *Decoder) GenerateHeartbeatResponse(data []byte) []byte { return nil }
func (d *Decoder) DecodeLocation(data []byte, imei, companyCode string, vehicleID int) (models.TelemetryPayload, error) {
	return models.TelemetryPayload{
		IMEI:        imei,
		CompanyCode: companyCode,
		VehicleID:   vehicleID,
		Timestamp:   time.Now().UTC(),
		RawData:     hex.EncodeToString(data),
	}, nil
}
func (d *Decoder) FrameSplitter() bufio.SplitFunc {
	return func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		if atEOF && len(data) == 0 { return 0, nil, nil }
		if atEOF { return len(data), data, nil }
		return len(data), data, nil
	}
}
func (d *Decoder) EncodeCommand(cmdType string, params map[string]string, raw string) ([]byte, error) {
	if cmdType == "custom" { return []byte(raw), nil }
	return nil, errors.New("command not supported for navigil")
}
