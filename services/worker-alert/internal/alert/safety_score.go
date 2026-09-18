package alert

import (
	"context"
	"fmt"
	"math"
	"backend/internal/dbclient"
	"backend/internal/models"
)

type SafetyEngine struct{}

func NewSafetyEngine() *SafetyEngine {
	return &SafetyEngine{}
}

func (s *SafetyEngine) Evaluate(ctx context.Context, companyCode string, payload models.TelemetryPayload, previousSpeed float64, prevHeading float64) {
	var deduction float64
	
	if payload.Speed > 80 {
		deduction += math.Floor((payload.Speed - 80) / 10) * 2
	}
	
	speedDiff := previousSpeed - payload.Speed
	if speedDiff > 15 {
		deduction += 5 // hard braking
	} else if speedDiff < -15 {
		deduction += 5 // harsh acceleration
	}
	
	headingDiff := math.Abs(payload.Heading - prevHeading)
	if headingDiff > 180 {
		headingDiff = 360 - headingDiff
	}
	if headingDiff > 45 && payload.Speed > 40 {
		deduction += 5 // hard cornering
	}
	
	if deduction > 0 {
		schema := fmt.Sprintf("adatrack_gps_%s", companyCode)
		query := fmt.Sprintf(`
			INSERT INTO %s.th_incidents (vehicle_id, incident_time, incident_type, severity, description, location_lat, location_lon)
			VALUES ($1, $2, 'SAFETY_VIOLATION', 'medium', $3, $4, $5)
		`, schema)
		desc := fmt.Sprintf("Safety violation detected. Deduction: %.1f points. Speed: %.1f", deduction, payload.Speed)
		
		dbclient.Pool.Exec(ctx, query, payload.VehicleID, payload.Timestamp, desc, payload.Latitude, payload.Longitude)
	}
}
