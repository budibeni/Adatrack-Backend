package controllers

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"ajb_gps/worker-persistence/models"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/prometheus/client_golang/prometheus"
)

// mkRow membangun satu TelemetryRow (imei, company).
func mkRow(imei, company string) models.TelemetryRow {
	return models.TelemetryRow{IMEI: imei, CompanyCode: company, EventTS: time.Now()}
}

// stubCompanyDB menimpa companyDBFn dengan hasil resolve pilihan.
//   returnErr=true  → resolve gagal (tenant routing error)
//   returnErr=false → kembalikan *sql.DB dari mock sqlmock
func stubCompanyDB(t *testing.T, returnErr bool) (sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	orig := companyDBFn
	if returnErr {
		companyDBFn = func(code string) (*sql.DB, error) {
			return nil, errors.New("tenant:routing:fail")
		}
	} else {
		companyDBFn = func(code string) (*sql.DB, error) { return db, nil }
	}
	cleanup := func() {
		companyDBFn = orig
		_ = db.Close()
	}
	return mock, cleanup
}

// ---------------------------------------------------------------------
// insertCompanyBatch — telemetry_logs (sukses / retry / gagal)
// ---------------------------------------------------------------------

func TestInsertCompanyBatchSuccess(t *testing.T) {
	resetPersistState(t)
	mock, cleanup := stubCompanyDB(t, false)
	defer cleanup()
	getErr := captureErrors(t)

	mock.ExpectExec("(?i)telemetry_logs").WillReturnResult(sqlmock.NewResult(1, 2))

	insertCompanyBatch("DEV001", []models.TelemetryRow{
		mkRow("1", "dev001"),
		mkRow("2", "dev001"),
	})

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
	if n := len(getErr()); n != 0 {
		t.Fatalf("tidak boleh ada error publish pada sukses, dapat %d", n)
	}
}

func TestInsertCompanyBatchRoutingErrorPublishes(t *testing.T) {
	resetPersistState(t)
	_, cleanup := stubCompanyDB(t, true)
	defer cleanup()
	getErr := captureErrors(t)

	insertCompanyBatch("DEV001", []models.TelemetryRow{mkRow("dev001", "1")})

	if n := len(getErr()); n != 1 {
		t.Fatalf("routing fail harus publish 1 error, dapat %d", n)
	}
}

func TestInsertCompanyBatchTransientThenSuccess(t *testing.T) {
	resetPersistState(t)
	mock, cleanup := stubCompanyDB(t, false)
	defer cleanup()

	mock.ExpectExec("(?i)telemetry_logs").WillReturnError(errors.New("connection refused"))
	mock.ExpectExec("(?i)telemetry_logs").WillReturnResult(sqlmock.NewResult(1, 1))

	insertCompanyBatch("DEV001", []models.TelemetryRow{mkRow("dev001", "x")})

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestInsertCompanyBatchNonTransientErrorPublishesAll(t *testing.T) {
	resetPersistState(t)
	mock, cleanup := stubCompanyDB(t, false)
	defer cleanup()
	getErr := captureErrors(t)

	mock.ExpectExec("(?i)telemetry_logs").WillReturnError(errors.New("unknown column 'z'"))

	insertCompanyBatch("DEV001", []models.TelemetryRow{
		mkRow("dev001", "a"),
		mkRow("dev001", "b"),
	})

	if n := len(getErr()); n != 2 {
		t.Fatalf("harus publish 2 error (per row), dapat %d", n)
	}
}

// ---------------------------------------------------------------------
// insertFuelCompanyBatch — fuel_logs (sukses / routing fail)
// ---------------------------------------------------------------------

func TestInsertFuelCompanyBatchSuccess(t *testing.T) {
	resetPersistState(t)
	mock, cleanup := stubCompanyDB(t, false)
	defer cleanup()

	mock.ExpectExec("(?i)fuel_logs").WillReturnResult(sqlmock.NewResult(1, 1))

	lvl := 50.0
	insertFuelCompanyBatch("DEV001", []models.TelemetryRow{{
		IMEI: "f", CompanyCode: "dev001", EventTS: time.Now(), FuelLevel: &lvl,
	}})

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestInsertFuelCompanyBatchRoutingErrorPublishes(t *testing.T) {
	resetPersistState(t)
	_, cleanup := stubCompanyDB(t, true)
	defer cleanup()
	getErr := captureErrors(t)

	insertFuelCompanyBatch("DEV001", []models.TelemetryRow{mkRow("dev001", "fuel")})

	if n := len(getErr()); n != 1 {
		t.Fatalf("fuel routing fail harus publish 1 error, dapat %d", n)
	}
}

// ---------------------------------------------------------------------
// RegisterMetrics — worker-persistence collector terdaftar.
// ---------------------------------------------------------------------

func TestRegisterMetricsPersistence(t *testing.T) {
	reg := prometheus.NewRegistry()
	RegisterMetrics(reg) // tidak boleh panic / double-register
}

// ---------------------------------------------------------------------
// IsTransientError — deteksi error sementara untuk retry.
// ---------------------------------------------------------------------

func TestIsTransientErrorPatterns(t *testing.T) {
	positives := []string{
		"dial: connection refused", "reset by peer: connection reset",
		"i/o timeout", "too many connections", "lock wait timeout exceeded",
		"deadlock found", "server has gone away",
	}
	for _, e := range positives {
		if !IsTransientError(errors.New(e)) {
			t.Errorf("%q harus transient", e)
		}
	}
	if IsTransientError(nil) || IsTransientError(errors.New("unknown column 'x'")) {
		t.Error("nil / non-transient tidak boleh transient")
	}
}