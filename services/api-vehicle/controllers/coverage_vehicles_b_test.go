package controllers

// coverage_vehicles_b_test.go (B4 2026-09-08): vehicles PATCH/DELETE +
// user<->vehicle assignment handlers, based on sqlmock.

import (
	"errors"
	"net/http"
	"testing"
	"time"

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
// PATCH /vehicles/:id — vehiclesUpdateHandler
// ---------------------------------------------------------------------

func TestVehicleUpdateOperatorForbidden(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, false, "PATCH", "/vehicles/5", `{"plate_number":"Z-9"}`)
	c.AddParam("id", "5")

	vehiclesUpdateHandler(c)

	if code := c.Writer.Status(); code != http.StatusForbidden {
		t.Fatalf("update operator = %d, want 403", code)
	}
}

func TestVehicleUpdateInvalidStatus(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "PATCH", "/vehicles/5", `{"status":"bogus"}`)
	c.AddParam("id", "5")

	vehiclesUpdateHandler(c)

	if code := c.Writer.Status(); code != http.StatusBadRequest {
		t.Fatalf("update bad status = %d, want 400", code)
	}
}

func TestVehicleUpdateEmpty(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "PATCH", "/vehicles/5", `{}`)
	c.AddParam("id", "5")

	vehiclesUpdateHandler(c)

	if code := c.Writer.Status(); code != http.StatusBadRequest {
		t.Fatalf("update empty = %d, want 400", code)
	}
}

func TestVehicleUpdateSuccess(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "PATCH", "/vehicles/5", `{"plate_number":"B-9","status":"maintenance"}`)
	c.AddParam("id", "5")

	m.ExpectExec("(?i)UPDATE vehicles SET plate_number = \\?, status = \\? WHERE id = \\? AND deleted_at IS NULL").
		WithArgs("B-9", "maintenance", uint64(5)).WillReturnResult(sqlmock.NewResult(0, 1))

	vehiclesUpdateHandler(c)

	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("update success = %d, want 200", code)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestVehicleUpdateNotFound(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "PATCH", "/vehicles/5", `{"make":"Kia"}`)
	c.AddParam("id", "5")

	m.ExpectExec("(?i)UPDATE vehicles SET make = \\? WHERE id = \\? AND deleted_at IS NULL").
		WithArgs("Kia", uint64(5)).WillReturnResult(sqlmock.NewResult(0, 0))

	vehiclesUpdateHandler(c)

	if code := c.Writer.Status(); code != http.StatusNotFound {
		t.Fatalf("update not found = %d, want 404", code)
	}
}

// ---------------------------------------------------------------------
// DELETE /vehicles/:id — vehiclesDeleteHandler
// ---------------------------------------------------------------------

func TestVehicleDeleteOperatorForbidden(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, false, "DELETE", "/vehicles/5", "")
	c.AddParam("id", "5")

	vehiclesDeleteHandler(c)

	if code := c.Writer.Status(); code != http.StatusForbidden {
		t.Fatalf("delete operator = %d, want 403", code)
	}
}

func TestVehicleDeleteSuccess(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "DELETE", "/vehicles/5", "")
	_, mm, _ := stubMasterDB(t)
	c.AddParam("id", "5")

	m.ExpectQuery("(?i)SELECT imei FROM vehicles WHERE id = \\? AND deleted_at IS NULL").
		WithArgs(uint64(5)).WillReturnRows(sqlmock.NewRows([]string{"imei"}).AddRow("1005"))
	m.ExpectExec("(?i)UPDATE vehicles SET deleted_at = NOW\\(\\), status='inactive' WHERE id = \\?").
		WithArgs(uint64(5)).WillReturnResult(sqlmock.NewResult(0, 1))
	mm.ExpectExec("(?i)DELETE FROM vehicle_imei_map WHERE imei = \\?").
		WithArgs("1005").WillReturnResult(sqlmock.NewResult(0, 1))

	vehiclesDeleteHandler(c)

	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("delete success = %d, want 200", code)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatalf("company expectations: %v", err)
	}
	if err := mm.ExpectationsWereMet(); err != nil {
		t.Fatalf("master expectations: %v", err)
	}
}

func TestVehicleDeleteNotFound2(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "DELETE", "/vehicles/404", "")
	c.AddParam("id", "404")

	m.ExpectQuery("(?i)SELECT imei FROM vehicles WHERE id = \\? AND deleted_at IS NULL").
		WithArgs(uint64(404)).WillReturnError(errors.New("boom"))

	vehiclesDeleteHandler(c)

	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("delete db err = %d, want 500", code)
	}
}

// ---------------------------------------------------------------------
// GET /vehicles/:id/users — vehicleUsersListHandler
// ---------------------------------------------------------------------

func TestVehicleUsersList(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "GET", "/vehicles/5/users", "")
	c.AddParam("id", "5")

	m.ExpectQuery("(?i)SELECT uv\\.user_id, COALESCE\\(uca\\.role_override,''\\), uv\\.assigned_at").
		WithArgs(uint64(5)).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "role", "assigned_at"}).
			AddRow(uint64(7), "Driver", time.Now()).
			AddRow(uint64(9), "Operator", time.Now()))

	vehicleUsersListHandler(c)

	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("users list = %d, want 200", code)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestVehicleUsersListNonAdminDenied(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, false, "GET", "/vehicles/99/users", "")
	c.AddParam("id", "99")

	vehicleUsersListHandler(c)

	if code := c.Writer.Status(); code != http.StatusForbidden {
		t.Fatalf("users list non-admin = %d, want 403", code)
	}
}

// ---------------------------------------------------------------------
// POST /vehicles/:id/users — vehicleAssignUserHandler
// ---------------------------------------------------------------------

func TestVehicleAssignUserSuccess(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "POST", "/vehicles/5/users", `{"user_id":7}`)
	c.AddParam("id", "5")

	m.ExpectQuery("(?i)SELECT COUNT\\(\\*\\) FROM vehicles WHERE id=\\? AND deleted_at IS NULL").
		WithArgs(uint64(5)).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(1)))
	m.ExpectQuery("(?i)SELECT COUNT\\(\\*\\) FROM user_company_access WHERE user_id=\\? AND is_active=TRUE").
		WithArgs(uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(1)))
	m.ExpectExec("(?i)INSERT IGNORE INTO user_vehicles \\(user_id, vehicle_id\\) VALUES \\(\\?, \\?\\)").
		WithArgs(uint64(7), uint64(5)).WillReturnResult(sqlmock.NewResult(0, 1))

	vehicleAssignUserHandler(c)

	if code := c.Writer.Status(); code != http.StatusCreated {
		t.Fatalf("assign = %d, want 201", code)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestVehicleAssignUserNotInCompany(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "POST", "/vehicles/5/users", `{"user_id":7}`)
	c.AddParam("id", "5")

	m.ExpectQuery("(?i)SELECT COUNT\\(\\*\\) FROM vehicles WHERE id=\\? AND deleted_at IS NULL").
		WithArgs(uint64(5)).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(1)))
	m.ExpectQuery("(?i)SELECT COUNT\\(\\*\\) FROM user_company_access WHERE user_id=\\? AND is_active=TRUE").
		WithArgs(uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(0)))

	vehicleAssignUserHandler(c)

	if code := c.Writer.Status(); code != http.StatusUnprocessableEntity {
		t.Fatalf("assign not-in-company = %d, want 422", code)
	}
}

// ---------------------------------------------------------------------
// DELETE /vehicles/:id/users/:userId — vehicleUnassignUserHandler
// ---------------------------------------------------------------------

func TestVehicleUnassignUserSuccess(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "DELETE", "/vehicles/5/users/7", "")
	// companyCtx extracts trailing numeric segment as :id (7); override with correct params.
	c.Params = gin.Params{{Key: "id", Value: "5"}, {Key: "userId", Value: "7"}}

	m.ExpectExec("(?i)DELETE FROM user_vehicles WHERE vehicle_id = \\? AND user_id = \\?").
		WithArgs(uint64(5), uint64(7)).WillReturnResult(sqlmock.NewResult(0, 1))

	vehicleUnassignUserHandler(c)

	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("unassign = %d, want 200", code)
	}
}

func TestVehicleUnassignUserBadID(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "DELETE", "/vehicles/5/users/abc", "")
	c.AddParam("id", "5")
	c.AddParam("userId", "abc")

	vehicleUnassignUserHandler(c)

	if code := c.Writer.Status(); code != http.StatusBadRequest {
		t.Fatalf("unassign bad id = %d, want 400", code)
	}
}