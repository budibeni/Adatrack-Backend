package state

import (
	"context"
	"testing"

	"backend/internal/models"
)

func TestProcessBatchEmpty(t *testing.T) {
	// Processing empty batch should return nil without panicking
	err := ProcessBatch(context.Background(), []models.TelemetryPayload{})
	if err != nil {
		t.Errorf("Expected nil, got %v", err)
	}
}

func TestDetermineStatus(t *testing.T) {
	if s := DetermineStatus(1); s != "ONLINE" {
		t.Errorf("Expected ONLINE for ACC 1, got %s", s)
	}
	
	if s := DetermineStatus(0); s != "IDLE" {
		t.Errorf("Expected IDLE for ACC 0, got %s", s)
	}
}
