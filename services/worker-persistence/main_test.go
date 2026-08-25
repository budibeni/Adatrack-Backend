package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"ajb_gps/worker-persistence/controllers"
	"ajb_gps/worker-persistence/models"
)

func TestIsTransientErrorConnectionRefused(t *testing.T) {
	if !controllers.IsTransientError(errors.New("connection refused")) {
		t.Error("connection refused should be transient")
	}
}

func TestIsTransientErrorConnectionReset(t *testing.T) {
	if !controllers.IsTransientError(errors.New("connection reset by peer")) {
		t.Error("connection reset should be transient")
	}
}

func TestIsTransientErrorTimeout(t *testing.T) {
	if !controllers.IsTransientError(errors.New("i/o timeout")) {
		t.Error("timeout should be transient")
	}
}

func TestIsTransientErrorTooManyConnections(t *testing.T) {
	if !controllers.IsTransientError(errors.New("too many connections")) {
		t.Error("too many connections should be transient")
	}
}

func TestIsTransientErrorLockWaitTimeout(t *testing.T) {
	if !controllers.IsTransientError(errors.New("lock wait timeout exceeded")) {
		t.Error("lock wait timeout should be transient")
	}
}

func TestIsTransientErrorDeadlock(t *testing.T) {
	if !controllers.IsTransientError(errors.New("deadlock found when trying to get lock")) {
		t.Error("deadlock should be transient")
	}
}

func TestIsTransientErrorServerGoneAway(t *testing.T) {
	if !controllers.IsTransientError(errors.New("server has gone away")) {
		t.Error("server has gone away should be transient")
	}
}

func TestIsTransientErrorNonTransient(t *testing.T) {
	if controllers.IsTransientError(errors.New("invalid password")) {
		t.Error("non-transient error should not be flagged as transient")
	}
	if controllers.IsTransientError(errors.New("duplicate entry")) {
		t.Error("duplicate entry should not be transient")
	}
}

func TestIsTransientErrorCaseInsensitive(t *testing.T) {
	if !controllers.IsTransientError(errors.New("CONNECTION REFUSED")) {
		t.Error("error matching should be case-insensitive")
	}
	if !controllers.IsTransientError(errors.New("Deadlock detected")) {
		t.Error("deadlock matching should be case-insensitive")
	}
}

func TestIsTransientErrorNil(t *testing.T) {
	if controllers.IsTransientError(nil) {
		t.Error("nil error should not be transient")
	}
}

func TestIsTransientErrorEmptyString(t *testing.T) {
	if controllers.IsTransientError(errors.New("")) {
		t.Error("empty error should not be transient")
	}
}

func TestTelemetryRowStruct(t *testing.T) {
	tr := models.TelemetryRow{
		IMEI:        "864201040512345",
		CompanyCode: "DEV001",
		VehicleID:   1,
		EventTS:     time.Now(),
		Lat:         -6.2088,
		Lon:         106.8456,
		Speed:       45.2,
		Heading:     180,
	}
	if tr.IMEI != "864201040512345" {
		t.Errorf("imei = %q", tr.IMEI)
	}
	if tr.CompanyCode != "DEV001" {
		t.Errorf("company_code = %q", tr.CompanyCode)
	}
}

func TestConstants(t *testing.T) {
	if models.MaxBatchSize != 500 {
		t.Errorf("MaxBatchSize = %d, want 500", models.MaxBatchSize)
	}
	if models.FlushInterval != 5*time.Second {
		t.Errorf("FlushInterval = %v, want 5s", models.FlushInterval)
	}
	if models.MaxRetries != 3 {
		t.Errorf("MaxRetries = %d, want 3", models.MaxRetries)
	}
}

func TestTelemetryMessageStruct(t *testing.T) {
	data := `{"imei":"123456789012345","company_code":"DEV001","vehicle_id":3,"lat":-6.2,"lon":106.8,"speed":50,"heading":90,"satellites":8,"hdop":1.2,"battery_level":80,"timestamp":1723800000}`
	var tm models.TelemetryMessage
	dec := json.NewDecoder(strings.NewReader(data))
	if err := dec.Decode(&tm); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if tm.IMEI != "123456789012345" {
		t.Errorf("IMEI = %q", tm.IMEI)
	}
	if tm.CompanyCode != "DEV001" {
		t.Errorf("CompanyCode = %q", tm.CompanyCode)
	}
	if tm.VehicleID != 3 {
		t.Errorf("VehicleID = %d", tm.VehicleID)
	}
}

func TestGroupByCompany(t *testing.T) {
	rows := []models.TelemetryRow{
		{IMEI: "1", CompanyCode: "DEV001"},
		{IMEI: "2", CompanyCode: "dev001"},
		{IMEI: "3", CompanyCode: "LOGI002"},
		{IMEI: "4"}, // empty → default
	}
	groups := controllers.GroupByCompany(rows)
	if len(groups) != 3 {
		t.Fatalf("groups = %d, want 3 (dev001, logi002, default)", len(groups))
	}
	if len(groups["DEV001"]) != 2 {
		t.Errorf("DEV001 rows = %d, want 2", len(groups["DEV001"]))
	}
	if len(groups["LOGI002"]) != 1 {
		t.Errorf("LOGI002 rows = %d, want 1", len(groups["LOGI002"]))
	}
	if len(groups["default"]) != 1 {
		t.Errorf("default rows = %d, want 1", len(groups["default"]))
	}
}
