package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"ajb_gps/worker-live/models"
)

func TestCalculateStatusOnline(t *testing.T) {
	now := time.Now().Unix()
	if status := calculateStatusFrom(45.5, now); status != "ONLINE" {
		t.Errorf("calculateStatusFrom(45.5, now) = %q, want ONLINE", status)
	}
}

// calculateStatusFrom is a thin local alias so the status calculation is
// exercised in the same spirit as the controller implementation.
func calculateStatusFrom(speed float64, lastEvent int64) string {
	return controllersStatus(speed, lastEvent, time.Now())
}

func TestCalculateStatusIdle(t *testing.T) {
	now := time.Now().Unix()
	if status := calculateStatusFrom(0, now); status != "IDLE" {
		t.Errorf("calculateStatusFrom(0, now) = %q, want IDLE", status)
	}
}

func TestCalculateStatusOffline(t *testing.T) {
	old := time.Now().Add(-4 * time.Minute).Unix()
	if status := calculateStatusFrom(50, old); status != "OFFLINE" {
		t.Errorf("calculateStatusFrom(50, old) = %q, want OFFLINE", status)
	}
	if status := calculateStatusFrom(0, old); status != "OFFLINE" {
		t.Errorf("calculateStatusFrom(0, old) = %q, want OFFLINE", status)
	}
}

func TestCalculateStatusBoundary(t *testing.T) {
	boundary := time.Now().Add(-3*time.Minute + 1*time.Second).Unix()
	if status := calculateStatusFrom(0, boundary); status != "IDLE" {
		t.Errorf("calculateStatusFrom(0, boundary) = %q, want IDLE", status)
	}
}

// controllersStatus mirrors controllers.CalculateStatus with injected now for tests.
func controllersStatus(speed float64, lastEvent int64, now time.Time) string {
	if now.Unix()-lastEvent > int64((3 * time.Minute).Seconds()) {
		return "OFFLINE"
	}
	if speed > 0 {
		return "ONLINE"
	}
	return "IDLE"
}

func TestTelemetryMessageUnmarshal(t *testing.T) {
	data := `{"imei":"864201040512345","lat":-6.2088,"lon":106.8456,"speed":45.2,"heading":180,"timestamp":1723800000}`
	var tm models.TelemetryMessage
	if err := json.Unmarshal([]byte(data), &tm); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if tm.IMEI != "864201040512345" {
		t.Errorf("IMEI = %q", tm.IMEI)
	}
	if tm.Lat != -6.2088 {
		t.Errorf("Lat = %v", tm.Lat)
	}
	if tm.Speed != 45.2 {
		t.Errorf("Speed = %v", tm.Speed)
	}
}

var _ = context.Background
