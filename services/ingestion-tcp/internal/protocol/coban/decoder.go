package coban

import (
	"bufio"
	"bytes"
	"errors"
	"strings"
	"time"

	"backend/internal/models"
)

// Coban (TK103) is a popular ASCII/Text-based protocol.
// Format example: imei:123456789012345,tracker,210917...
type Decoder struct{}

func (d *Decoder) ProtocolName() string {
	return "COBAN"
}

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

	// Coban expects an ON ACK for login/activation (e.g. LOAD)
	return imei[:15], []byte("LOAD"), nil
}

func (d *Decoder) IsHeartbeat(data []byte) bool {
	return strings.Contains(string(data), "heartbeat")
}

func (d *Decoder) GenerateHeartbeatResponse(data []byte) []byte {
	return []byte("ON")
}

func (d *Decoder) DecodeLocation(data []byte, imei, companyCode string, vehicleID int) (models.TelemetryPayload, error) {
	// Dummy extraction for enterprise demonstration of text protocol
	// In production, we parse: 035022.000,A,2232.1234,N,11404.1234,E,0.00
	str := string(data)
	if !strings.Contains(str, "tracker") {
		return models.TelemetryPayload{}, errors.New("not a location packet")
	}

	return models.TelemetryPayload{
		IMEI:        imei,
		CompanyCode: companyCode,
		VehicleID:   vehicleID,
		Latitude:    0, // Parsed from NMEA format in real implementation
		Longitude:   0,
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
