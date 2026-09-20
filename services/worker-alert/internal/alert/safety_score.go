package alert

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"backend/internal/dbclient"
	"backend/internal/models"
	"backend/internal/tenant"
)

type SafetyConfig struct {
	MaxSpeedKMH        float64
	HarshAccelKPHS     float64
	HarshBrakeKPHS     float64
	HardCorneringDeg   float64
	Enabled            bool
	FetchedAt          time.Time
}

type SafetyEngine struct {
	cache sync.Map // map[string]SafetyConfig, key: "company:vehicle_id"
}

func NewSafetyEngine() *SafetyEngine {
	return &SafetyEngine{}
}

func (s *SafetyEngine) getConfig(ctx context.Context, companyCode string, vehicleID int) SafetyConfig {
	key := fmt.Sprintf("%s:%d", companyCode, vehicleID)
	if val, ok := s.cache.Load(key); ok {
		config := val.(SafetyConfig)
		if time.Since(config.FetchedAt) < 5*time.Minute {
			return config
		}
	}

	config := SafetyConfig{
		MaxSpeedKMH:      80.0,
		HarshAccelKPHS:   15.0,
		HarshBrakeKPHS:   15.0,
		HardCorneringDeg: 45.0,
		Enabled:          true,
		FetchedAt:        time.Now(),
	}

	schema := fmt.Sprintf("adatrack_gps_%s", companyCode)
	err := tenant.NewReadRouter(companyCode).QueryRow(ctx, fmt.Sprintf(`
		SELECT max_speed_kmh, harsh_accel_kph_s, harsh_brake_kph_s, hard_cornering_deg, enabled 
		FROM %s.tm_safety_configs 
		WHERE (vehicle_id = $1 OR vehicle_id IS NULL) AND deleted_at IS NULL
		ORDER BY vehicle_id NULLS LAST LIMIT 1
	`, schema), vehicleID).Scan(&config.MaxSpeedKMH, &config.HarshAccelKPHS, &config.HarshBrakeKPHS, &config.HardCorneringDeg, &config.Enabled)

	if err != nil {
		// Fallback defaults
	}

	s.cache.Store(key, config)
	return config
}

func (s *SafetyEngine) Evaluate(ctx context.Context, companyCode string, payload models.TelemetryPayload, previousSpeed float64, prevHeading float64) {
	config := s.getConfig(ctx, companyCode, payload.VehicleID)
	if !config.Enabled {
		return
	}

	var deduction float64
	var violationTypes []string

	if payload.Speed > config.MaxSpeedKMH {
		deduction += math.Floor((payload.Speed - config.MaxSpeedKMH) / 10) * 2
		violationTypes = append(violationTypes, "Speeding")
	}
	
	speedDiff := previousSpeed - payload.Speed
	if speedDiff > config.HarshBrakeKPHS {
		deduction += 5 // hard braking
		violationTypes = append(violationTypes, "Harsh Braking")
	} else if speedDiff < -config.HarshAccelKPHS {
		deduction += 5 // harsh acceleration
		violationTypes = append(violationTypes, "Harsh Acceleration")
	}
	
	headingDiff := math.Abs(payload.Heading - prevHeading)
	if headingDiff > 180 {
		headingDiff = 360 - headingDiff
	}
	if headingDiff > config.HardCorneringDeg && payload.Speed > 40 {
		deduction += 5 // hard cornering
		violationTypes = append(violationTypes, "Hard Cornering")
	}
	
	if deduction > 0 {
		schema := fmt.Sprintf("adatrack_gps_%s", companyCode)
		query := fmt.Sprintf(`
			INSERT INTO %s.th_incidents (vehicle_id, incident_time, incident_type, severity, description, location_lat, location_lon)
			VALUES ($1, $2, 'SAFETY_VIOLATION', 'medium', $3, $4, $5)
		`, schema)
		desc := fmt.Sprintf("Safety violation detected: %v. Deduction: %.1f points. Speed: %.1f", violationTypes, deduction, payload.Speed)
		
		if _, err := dbclient.Pool.Exec(ctx, query, payload.VehicleID, payload.Timestamp, desc, payload.Latitude, payload.Longitude); err != nil {
			logger.Log.Error("Failed to log safety incident", "err", err, "vehicle_id", payload.VehicleID)
		}
	}
}
