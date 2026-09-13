package controllers

// coverage_handlers_extra2_test.go (B4 coverage api-vehicle 2026-09-09):
// loadMasterUserByEmail nullable fields, authLoginHandler cabang tersisa,
// dan error paths: vehicles list/detail, routes update, route assign,
// speed/fuel configs create/update/delete.

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"golang.org/x/crypto/bcrypt"
)

// ---------------------------------------------------------------------
// loadMasterUserByEmail — nullable field branches
// ---------------------------------------------------------------------

func avMasterUserRowFull(hash, role, status, companyCode string) *sqlmock.Rows {
	return sqlmock.NewRows(avMasterUserCols).
		AddRow(
			uint64(7), int64(1), companyCode, "u@dev001.io", hash, "Full Name",
			"uname", "First", "Last", "+62811", true,
			true, true, "id", "http://avatar", 3,
			time.Now(), time.Now(), time.Now(), time.Now(),
			uint64(1), uint64(2), role, status,
		)
}

func TestLoadMasterUserByEmailWithValues(t *testing.T) {
	t.Helper()
	db, m := mockDB(t)
	m.ExpectQuery("SELECT id, company_id, company_code, email, password_hash").
		WillReturnRows(avMasterUserRowFull("hash", "Admin", "active", "DEV001"))

	u, err := loadMasterUserByEmail(db, "u@dev001.io")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if u.PasswordChangedAt == nil || u.LockedUntil == nil || u.DeletedAt == nil {
		t.Fatal("expected non-nil time pointers")
	}
	if u.CreatedBy == nil || *u.CreatedBy != 1 {
		t.Fatalf("CreatedBy = %v", u.CreatedBy)
	}
	if u.UpdatedBy == nil || *u.UpdatedBy != 2 {
		t.Fatalf("UpdatedBy = %v", u.UpdatedBy)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// ---------------------------------------------------------------------
// authLoginHandler cabang tersisa
// ---------------------------------------------------------------------

func TestLoginBadPassword(t *testing.T) {
	t.Helper()
	freshLoginLimiter()
	hash := genBcryptHash("right-pw", t)
	c, _, _ := companyCtx(t, true, "POST", "/auth/login", `{"email":"u@dev001.io","password":"wrong-pw"}`)
	_, mm, _ := stubMasterDB(t)
	mm.ExpectQuery("SELECT id, company_id, company_code, email, password_hash").
		WithArgs("u@dev001.io").
		WillReturnRows(avMasterUserRow(hash, "Admin", "active", "DEV001", nil))
	cdb, cm := mockDB(t)
	stubDBByCode(t, cdb, nil)
	cm.ExpectQuery("SELECT id, user_id, role_override, is_active FROM user_company_access").
		WithArgs(uint64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "role_override", "is_active"}).
			AddRow(1, 7, "", true))

	authLoginHandler(c)

	if code := c.Writer.Status(); code != http.StatusUnauthorized {
		t.Fatalf("login bad pw = %d, want 401", code)
	}
}

func TestLoginPlatformRoleRejected(t *testing.T) {
	t.Helper()
	freshLoginLimiter()
	hash := genBcryptHash("pw", t)
	c, _, _ := companyCtx(t, true, "POST", "/auth/login", `{"email":"p@x.io","password":"pw"}`)
	_, mm, _ := stubMasterDB(t)
	mm.ExpectQuery("SELECT id, company_id, company_code, email, password_hash").
		WithArgs("p@x.io").
		WillReturnRows(avMasterUserRow(hash, "Admin", "active", "default", nil))

	authLoginHandler(c)

	if code := c.Writer.Status(); code != http.StatusForbidden {
		t.Fatalf("login platform rejected = %d, want 403", code)
	}
}

func TestLoginCompanyDBUnavailable(t *testing.T) {
	t.Helper()
	freshLoginLimiter()
	hash := genBcryptHash("pw", t)
	c, _, _ := companyCtx(t, true, "POST", "/auth/login", `{"email":"u@dev001.io","password":"pw"}`)
	_, mm, _ := stubMasterDB(t)
	mm.ExpectQuery("SELECT id, company_id, company_code, email, password_hash").
		WithArgs("u@dev001.io").
		WillReturnRows(avMasterUserRow(hash, "Admin", "active", "DEV001", nil))
	stubDBByCode(t, nil, errors.New("no pool"))

	authLoginHandler(c)

	if code := c.Writer.Status(); code != http.StatusForbidden {
		t.Fatalf("login company db unavailable = %d, want 403", code)
	}
}

func TestLoginVehicleIDsError(t *testing.T) {
	t.Helper()
	freshLoginLimiter()
	_ = installFakeTokenManager(t)
	hash := genBcryptHash("pw", t)
	c, _, _ := companyCtx(t, true, "POST", "/auth/login", `{"email":"u@dev001.io","password":"pw"}`)
	_, mm, _ := stubMasterDB(t)
	mm.ExpectQuery("SELECT id, company_id, company_code, email, password_hash").
		WithArgs("u@dev001.io").
		WillReturnRows(avMasterUserRow(hash, "Operator", "active", "DEV001", nil))
	cdb, cm := mockDB(t)
	stubDBByCode(t, cdb, nil)
	cm.ExpectQuery("SELECT id, user_id, role_override, is_active FROM user_company_access").
		WithArgs(uint64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "role_override", "is_active"}).
			AddRow(1, 7, "", true))
	cm.ExpectQuery("SELECT vehicle_id FROM user_vehicles WHERE user_id = \\?").
		WithArgs(uint64(7)).WillReturnError(errors.New("boom"))

	authLoginHandler(c)

	if code := c.Writer.Status(); code != http.StatusServiceUnavailable {
		t.Fatalf("login vehicle ids err = %d, want 503", code)
	}
}

// ---------------------------------------------------------------------
// vehicles list/detail error paths
// ---------------------------------------------------------------------

func TestVehiclesListCountError(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "GET", "/vehicles", "")
	m.ExpectQuery("(?i)COUNT\\(\\*\\) FROM vehicles WHERE deleted_at IS NULL").
		WillReturnError(errors.New("boom"))

	vehiclesListHandler(c)

	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("vehicles list count err = %d, want 500", code)
	}
}

func TestVehiclesListQueryError(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "GET", "/vehicles", "")
	m.ExpectQuery("(?i)COUNT\\(\\*\\) FROM vehicles WHERE deleted_at IS NULL").
		WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(0)))
	m.ExpectQuery("(?i)FROM vehicles WHERE deleted_at IS NULL ORDER BY id").
		WillReturnError(errors.New("boom"))

	vehiclesListHandler(c)

	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("vehicles list query err = %d, want 500", code)
	}
}

func TestVehicleDetailQueryError(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "GET", "/vehicles/5", "")
	c.AddParam("id", "5")
	m.ExpectQuery("(?i)FROM vehicles WHERE id = \\? AND deleted_at IS NULL").
		WithArgs(uint64(5)).WillReturnError(errors.New("boom"))

	vehicleDetailHandler(c)

	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("vehicle detail query err = %d, want 500", code)
	}
}

func TestVehicleDetailScanError(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "GET", "/vehicles/5", "")
	c.AddParam("id", "5")
	m.ExpectQuery("(?i)FROM vehicles WHERE id = \\? AND deleted_at IS NULL").
		WithArgs(uint64(5)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "imei"}).AddRow(uint64(5), "1005"))

	vehicleDetailHandler(c)

	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("vehicle detail scan err = %d, want 500", code)
	}
}

// ---------------------------------------------------------------------
// routes update / assign error paths
// ---------------------------------------------------------------------

func TestRoutesUpdateNotFound(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "PATCH", "/routes/99", `{"name":"X"}`)
	c.AddParam("id", "99")
	m.ExpectExec("(?i)UPDATE routes SET name = \\? WHERE id = \\?$").
		WithArgs("X", uint64(99)).WillReturnResult(sqlmock.NewResult(0, 0))

	routesUpdateHandler(c)

	if code := c.Writer.Status(); code != http.StatusNotFound {
		t.Fatalf("routes update 404 = %d, want 404", code)
	}
}

func TestRouteAssignVehicleNotFound(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "POST", "/routes/1/assignments", `{"vehicle_id":99,"driver_user_id":7}`)
	c.AddParam("id", "1")
	m.ExpectQuery("(?i)SELECT COUNT\\(\\*\\) FROM routes WHERE id=\\? AND is_active=TRUE").
		WithArgs(uint64(1)).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(1)))
	m.ExpectQuery("(?i)SELECT COUNT\\(\\*\\) FROM vehicles WHERE id=\\? AND deleted_at IS NULL").
		WithArgs(uint64(99)).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(0)))

	routeAssignHandler(c)

	if code := c.Writer.Status(); code != http.StatusNotFound {
		t.Fatalf("route assign vehicle 404 = %d, want 404", code)
	}
}

// ---------------------------------------------------------------------
// speed/fuel configs error paths
// ---------------------------------------------------------------------

func TestSpeedConfigsCreateVehicleNotFound(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "POST", "/speed-configs", `{"vehicle_id":99,"speed_limit_kmh":80,"grace_margin_kmh":10}`)
	m.ExpectQuery("(?i)SELECT COUNT\\(\\*\\) FROM vehicles WHERE id=\\? AND deleted_at IS NULL").
		WithArgs(uint64(99)).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(0)))

	speedConfigsCreateHandler(c)

	if code := c.Writer.Status(); code != http.StatusNotFound {
		t.Fatalf("speed create vehicle 404 = %d, want 404", code)
	}
}

func TestSpeedConfigsUpdateNotFound(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "PATCH", "/speed-configs/99", `{"is_active":false}`)
	c.AddParam("id", "99")
	m.ExpectExec("(?i)UPDATE speed_configs SET is_active = \\? WHERE id = \\?$").
		WithArgs(false, uint64(99)).WillReturnResult(sqlmock.NewResult(0, 0))

	speedConfigsUpdateHandler(c)

	if code := c.Writer.Status(); code != http.StatusNotFound {
		t.Fatalf("speed update 404 = %d, want 404", code)
	}
}

func TestFuelConfigsDeleteNotFound(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "DELETE", "/fuel-configs/99", "")
	m.ExpectExec("(?i)DELETE FROM fuel_configs WHERE id = \\?$").
		WithArgs(uint64(99)).WillReturnResult(sqlmock.NewResult(0, 0))

	fuelConfigsDeleteHandler(c)

	// Handler selalu 200 walau 0 rows terhapus (idempotent delete).
	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("fuel delete 404-row = %d, want 200", code)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

var _ = bcrypt.MinCost
