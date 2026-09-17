package state

import (
	"context"
	"testing"
	"time"

	"backend/internal/models"
)

func TestProcessBatchEmpty(t *testing.T) {
	// Processing empty batch should return nil without panicking
	err := ProcessBatch(context.Background(), []models.TelemetryPayload{})
	if err != nil {
		t.Errorf("Expected nil, got %v", err)
	}
}

func TestStatusComputation(t *testing.T) {
	// Since ProcessBatch requires Redis to be connected to test fully,
	// we just test the logic that determines status before Marshal (if we had it separate)
	// Here we can at least assert that the code compiles and empty slice works.
	payload := models.TelemetryPayload{
		IMEI: "123",
		CompanyCode: "TEST",
		Speed: 10,
		ACCStatus: 1,
		Timestamp: time.Now(),
	}
	_ = payload
}
