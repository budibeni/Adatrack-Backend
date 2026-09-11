package controllers

// coverage_vehicles_a_test.go (B4 2026-09-08): handler test vehicles list /
// detail / create based on sqlmock — pola companyCtx + stubMasterDB dari
// b4_mock_test.go. Target: coverage api-vehicle >= 80%.

import (
	"errors"
	"net/http"
	"testing"

	"ajb_gps/internal"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
)

func init() {
	gin.SetMode(gin.TestMode)
	_ = internal.RegisterMetrics(prometheus.NewRegistry())
}

// ---------------------------------------------------------------------
// GET /vehicles — vehiclesListHandler
// ---------------------------------------------------------------------

func TestVehiclesListAdmin(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "GET", "/vehicles", "")

	m.ExpectQuery("(?i)COUNT\\(\\*\\) FROM vehicles WHERE deleted_at IS NULL$").
		WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(2)))
	m.ExpectQuery("(?i)FROM vehicles WHERE deleted_at IS NULL ORDER BY id LIMIT \\? OFFSET \\?$").
		WithArgs(100, 0).
		WillReturnRows(vehicleRow(1, "1001", "B-1", "Toyota", "Hilux", "diesel", "truck", nil).
			AddRow(vehicleRowVals(2, "1002", "B-2", "Honda", "Civic", "petrol", "car", nil)...))

	vehiclesListHandler(c)

	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("list admin status = %d, want 200", code)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestVehiclesListAdminWithStatusFilter(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "GET", "/vehicles?status=active", "")

	m.ExpectQuery("(?i)COUNT\\(\\*\\) FROM vehicles WHERE deleted_at IS NULL AND status = \\?$").
		WithArgs("active").WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(1)))
	m.ExpectQuery("(?i)FROM vehicles WHERE deleted_at IS NULL AND status = \\? ORDER BY id LIMIT \\? OFFSET \\?$").
		WithArgs("active", 100, 0).
		WillReturnRows(vehicleRow(7, "1007", "B-7", "Suzuki", "Swift", "petrol", "car", nil))

	vehiclesListHandler(c)

	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("status filter = %d, want 200", code)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestVehiclesListNonAdminEmpty(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, false, "GET", "/vehicles", "")
	c.Set(ctxAllowedKey, map[uint64]struct{}{}) // paksa path len(allowed)==0

	vehiclesListHandler(c)

	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("non-admin empty = %d, want 200", code)
	}
}

func TestVehiclesListNonAdminFiltered(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, false, "GET", "/vehicles", "")

	m.ExpectQuery("(?i)COUNT\\(\\*\\) FROM vehicles WHERE deleted_at IS NULL AND id IN \\(\\?,\\?\\)$").
		WithArgs(uint64(1), uint64(2)).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(1)))
	m.ExpectQuery("(?i)FROM vehicles WHERE deleted_at IS NULL AND id IN \\(\\?,\\?\\) ORDER BY id LIMIT \\? OFFSET \\?$").
		WithArgs(uint64(1), uint64(2), 100, 0).
		WillReturnRows(vehicleRow(1, "1001", "B-1", "Toyota", "Hilux", "diesel", "truck", nil))

	vehiclesListHandler(c)

	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("non-admin filtered = %d, want 200", code)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestVehiclesListCountError2(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "GET", "/vehicles", "")

	m.ExpectQuery("(?i)COUNT\\(\\*\\) FROM vehicles WHERE deleted_at IS NULL$").
		WillReturnError(errors.New("boom"))

	vehiclesListHandler(c)

	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("count err = %d, want 500", code)
	}
}

// ---------------------------------------------------------------------
// GET /vehicles/:id — vehicleDetailHandler
// ---------------------------------------------------------------------

func TestVehicleDetailFound(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "GET", "/vehicles/5", "")
	c.AddParam("id", "5")

	m.ExpectQuery("(?i)FROM vehicles WHERE id = \\? AND deleted_at IS NULL$").
		WithArgs(uint64(5)).
		WillReturnRows(vehicleRow(5, "1005", "B-5", "Ford", "Ranger", "diesel", "truck", nil))

	vehicleDetailHandler(c)

	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("detail found = %d, want 200", code)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestVehicleDetailNotFound(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "GET", "/vehicles/404", "")
	c.AddParam("id", "404")

	m.ExpectQuery("(?i)FROM vehicles WHERE id = \\? AND deleted_at IS NULL$").
		WithArgs(uint64(404)).WillReturnRows(sqlmock.NewRows(vehicleRowCols))

	vehicleDetailHandler(c)

	if code := c.Writer.Status(); code != http.StatusNotFound {
		t.Fatalf("detail 404 = %d, want 404", code)
	}
}

func TestVehicleDetailBadID(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "GET", "/vehicles/abc", "")
	c.AddParam("id", "abc")

	vehicleDetailHandler(c)

	if code := c.Writer.Status(); code != http.StatusBadRequest {
		t.Fatalf("detail bad id = %d, want 400", code)
	}
}

// ---------------------------------------------------------------------
// POST /vehicles — vehiclesCreateHandler
// ---------------------------------------------------------------------

func TestVehicleCreateOperatorForbidden(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, false, "POST", "/vehicles", `{"imei":"2001","plate_number":"C-1"}`)

	vehiclesCreateHandler(c)

	if code := c.Writer.Status(); code != http.StatusForbidden {
		t.Fatalf("create operator = %d, want 403", code)
	}
}

func TestVehicleCreateSuccess(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "POST", "/vehicles", `{"imei":"2001","plate_number":"C-1","make":"Kia"}`)
	_, mm, _ := stubMasterDB(t)

	m.ExpectExec("(?i)INSERT INTO vehicles \\(imei, plate_number, make").WithArgs(
		"2001", "C-1", "Kia", nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil).
		WillReturnResult(sqlmock.NewResult(42, 1))
	mm.ExpectExec("(?i)INSERT INTO vehicle_imei_map \\(imei, company_code, vehicle_id\\)").
		WithArgs("2001", "DEV001", int64(42)).WillReturnResult(sqlmock.NewResult(1, 1))

	vehiclesCreateHandler(c)

	if code := c.Writer.Status(); code != http.StatusCreated {
		t.Fatalf("create success = %d, want 201", code)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatalf("company expectations: %v", err)
	}
	if err := mm.ExpectationsWereMet(); err != nil {
		t.Fatalf("master expectations: %v", err)
	}
}

func TestVehicleCreateDuplicate(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "POST", "/vehicles", `{"imei":"2001","plate_number":"C-1"}`)
	_, _, _ = stubMasterDB(t)

	m.ExpectExec("(?i)INSERT INTO vehicles \\(imei, plate_number, make").
		WithArgs("2001", "C-1", nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil).
		WillReturnError(errors.New("Duplicate entry '2001' for key 'imei'"))

	vehiclesCreateHandler(c)

	if code := c.Writer.Status(); code != http.StatusConflict {
		t.Fatalf("create duplicate = %d, want 409", code)
	}
}

func TestVehicleCreateBadBody(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "POST", "/vehicles", "not-json")

	vehiclesCreateHandler(c)

	if code := c.Writer.Status(); code != http.StatusBadRequest {
		t.Fatalf("create bad body = %d, want 400", code)
	}
}