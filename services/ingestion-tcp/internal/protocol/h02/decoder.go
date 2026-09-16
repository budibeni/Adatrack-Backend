package h02

import (
	"bufio"
	"bytes"
	"errors"
	"strings"
	"time"

	"backend/internal/models"
)

// H02 (SinoTrack) uses a text format starting with *HQ
// Example: *HQ,351234567890123,V1,035022,V,2232...
type Decoder struct{}

func (d *Decoder) ProtocolName() string {
	return "H02"
}

func (d *Decoder) DecodeLogin(data []byte) (string, []byte, error) {
	str := string(data)
	if !strings.HasPrefix(str, "*HQ") {
		return "", nil, errors.New("invalid h02 header")
	}

	parts := strings.Split(str, ",")
	if len(parts) < 2 {
		return "", nil, errors.New("invalid h02 format")
	}

	imei := parts[1]
	return imei, nil, nil
}

func (d *Decoder) IsHeartbeat(data []byte) bool {
	return strings.Contains(string(data), ",LINK,")
}

func (d *Decoder) GenerateHeartbeatResponse(data []byte) []byte {
	return nil
}

func (d *Decoder) DecodeLocation(data []byte, imei, companyCode string, vehicleID int) (models.TelemetryPayload, error) {
	str := string(data)
	if !strings.Contains(str, ",V1,") { // V1 is location data
		return models.TelemetryPayload{}, errors.New("not an h02 location packet")
	}

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
		if atEOF && len(data) == 0 {
			return 0, nil, nil
		}
		if i := bytes.IndexByte(data, '#'); i >= 0 {
			return i + 1, data[:i+1], nil
		}
		if atEOF {
			return len(data), data, nil
		}
		return 0, nil, nil
	}
}
