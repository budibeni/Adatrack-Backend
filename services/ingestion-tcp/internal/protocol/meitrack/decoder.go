package meitrack

import (
	"errors"
	"strings"
	"time"

	"backend/internal/models"
)

// Meitrack uses a hybrid CSV-like format starting with $$
// Example: $$Q,23,123456789012345,AAA,DATA*CS\r\n
type Decoder struct{}

func (d *Decoder) ProtocolName() string {
	return "MEITRACK"
}

func (d *Decoder) DecodeLogin(data []byte) (string, []byte, error) {
	str := string(data)
	if !strings.HasPrefix(str, "$$") {
		return "", nil, errors.New("invalid meitrack header")
	}
	
	parts := strings.Split(str, ",")
	if len(parts) < 3 {
		return "", nil, errors.New("invalid meitrack format")
	}
	
	imei := parts[2]
	return imei, nil, nil // Meitrack doesn't strictly require an ACK for login
}

func (d *Decoder) IsHeartbeat(data []byte) bool {
	return false
}

func (d *Decoder) GenerateHeartbeatResponse(data []byte) []byte {
	return nil
}

func (d *Decoder) DecodeLocation(data []byte, imei, companyCode string, vehicleID int) (models.TelemetryPayload, error) {
	str := string(data)
	if !strings.Contains(str, "AAA") { // AAA is the command for regular position
		return models.TelemetryPayload{}, errors.New("not a meitrack location packet")
	}

	return models.TelemetryPayload{
		IMEI:        imei,
		CompanyCode: companyCode,
		VehicleID:   vehicleID,
		Timestamp:   time.Now().UTC(),
		RawData:     str,
	}, nil
}
