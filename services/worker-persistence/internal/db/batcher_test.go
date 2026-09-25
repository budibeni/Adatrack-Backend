package db

import (
	"context"
	"testing"
	"backend/internal/models"
)

func TestBatchInsertEmpty(t *testing.T) {
	err := BatchInsert(context.Background(), []models.TelemetryPayload{})
	if err != nil {
		t.Errorf("Expected nil, got %v", err)
	}
}
