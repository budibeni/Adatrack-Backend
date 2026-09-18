package navigil

import (
	"bufio"
	"errors"
	"time"

	"backend/internal/models"
)

// Decoder for Navigil protocol
type Decoder struct{}

func (d *Decoder) ProtocolName() string {
	return "Navigil"
}

func (d *Decoder) DecodeLogin(data []byte) (string, []byte, error) {
	// TODO: implement actual login decoding
	return "", nil, errors.New("not implemented")
}

func (d *Decoder) IsHeartbeat(data []byte) bool {
	return false
}

func (d *Decoder) GenerateHeartbeatResponse(data []byte) []byte {
	return nil
}

func (d *Decoder) DecodeLocation(data []byte, imei, companyCode string, vehicleID int) (models.TelemetryPayload, error) {
	return models.TelemetryPayload{
		IMEI:        imei,
		CompanyCode: companyCode,
		VehicleID:   vehicleID,
		Timestamp:   time.Now().UTC(),
		RawData:     string(data), // stub
	}, errors.New("not implemented")
}

func (d *Decoder) FrameSplitter() bufio.SplitFunc {
	return func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		if atEOF && len(data) == 0 {
			return 0, nil, nil
		}
		// Basic line splitter as stub
		for i := 0; i < len(data); i++ {
			if data[i] == '\n' {
				return i + 1, data[:i+1], nil
			}
		}
		if atEOF {
			return len(data), data, nil
		}
		return 0, nil, nil
	}
}
