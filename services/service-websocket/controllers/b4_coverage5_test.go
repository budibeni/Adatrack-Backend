package controllers

// b4_coverage5_test.go (B4 coverage 2026-09-04 — Stage C):
// reference handlers (countries/provinces/cities/districts/subdistricts)
// via masterDBFn indirection + sqlmock; routes handlers (list/create/detail/
// track/delete/update) via companyRead/companyDB + sqlmock; pure helpers
// fetchRouteByID/loadAssignments/routeToItem/routeAccessible/joinEq.

import (
	"database/sql"
	"database/sql/driver"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// fixture: reference routes use masterDBFn indirection (appRedis nil →
// refGetCache miss → all handlers hit the DB path).
// ---------------------------------------------------------------------------

func refFixture(t *testing.T) (sqlmock.Sqlmock, func()) {
	t.Helper()
	db, m := mockDB(t)
	oldMaster := masterDBFn
	oldRedis := appRedis
	masterDBFn = func() *sql.DB { return db }
	appRedis = nil
	cleanup := func() {
		masterDBFn = oldMaster
		appRedis = oldRedis
	}
	t.Cleanup(cleanup)
	return m, cleanup
}

func refCtx(method, target string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, target, nil)
	return c, rec
}

// ---------------------------------------------------------------------------
// reference countries
// ---------------------------------------------------------------------------

func TestReferenceCountriesHandler_Success(t *testing.T) {
	m, _ := refFixture(t)
	c, rec := refCtx(http.MethodGet, "/api/v1/reference/countries")

	m.ExpectQuery(`SELECT id, iso_code, iso_code_3, name, phone_code, currency_code, is_active
		 FROM countries WHERE is_active = TRUE ORDER BY name`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "iso_code", "iso_code_3", "name", "phone_code", "currency_code", "is_active"}).
			AddRow(360, "ID", "IDN", "Indonesia", "+62", "IDR", true).
			AddRow(840, "US", "USA", "United States", "+1", "USD", true))

	referenceCountriesHandler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestReferenceCountriesHandler_Search(t *testing.T) {
	m, _ := refFixture(t)
	c, rec := refCtx(http.MethodGet, "/api/v1/reference/countries?q=ind")

	m.ExpectQuery(`SELECT id, iso_code, iso_code_3, name, phone_code, currency_code, is_active
		 FROM countries WHERE is_active = TRUE AND name LIKE \? ORDER BY name`).
		WithArgs("%ind%").
		WillReturnRows(sqlmock.NewRows([]string{"id", "iso_code", "iso_code_3", "name", "phone_code", "currency_code", "is_active"}).
			AddRow(360, "ID", "IDN", "Indonesia", "+62", "IDR", true))

	referenceCountriesHandler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestReferenceCountriesHandler_NilDB(t *testing.T) {
	oldMaster := masterDBFn
	masterDBFn = func() *sql.DB { return nil }
	defer func() { masterDBFn = oldMaster }()

	c, rec := refCtx(http.MethodGet, "/api/v1/reference/countries")
	referenceCountriesHandler(c)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

func TestReferenceCountryDetailHandler_Success(t *testing.T) {
	m, _ := refFixture(t)
	c, rec := refCtx(http.MethodGet, "/api/v1/reference/countries/360")
	c.Params = gin.Params{{Key: "id", Value: "360"}}

	m.ExpectQuery(`SELECT id, iso_code, iso_code_3, name, phone_code, currency_code, is_active
		 FROM countries WHERE id = \? AND is_active = TRUE`).
		WithArgs(uint64(360)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "iso_code", "iso_code_3", "name", "phone_code", "currency_code", "is_active"}).
			AddRow(360, "ID", "IDN", "Indonesia", "+62", "IDR", true))

	referenceCountryDetailHandler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestReferenceCountryDetailHandler_NotFound(t *testing.T) {
	m, _ := refFixture(t)
	c, rec := refCtx(http.MethodGet, "/api/v1/reference/countries/999")
	c.Params = gin.Params{{Key: "id", Value: "999"}}

	m.ExpectQuery(`SELECT id, iso_code, iso_code_3, name, phone_code, currency_code, is_active
		 FROM countries WHERE id = \? AND is_active = TRUE`).
		WithArgs(uint64(999)).WillReturnError(sql.ErrNoRows)

	referenceCountryDetailHandler(c)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestReferenceCountryDetailHandler_InvalidID(t *testing.T) {
	_, _ = refFixture(t)
	c, rec := refCtx(http.MethodGet, "/api/v1/reference/countries/abc")
	c.Params = gin.Params{{Key: "id", Value: "abc"}}
	referenceCountryDetailHandler(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// reference provinces
// ---------------------------------------------------------------------------

func TestReferenceProvincesHandler_Success(t *testing.T) {
	m, _ := refFixture(t)
	c, rec := refCtx(http.MethodGet, "/api/v1/reference/provinces")

	m.ExpectQuery(`SELECT id, country_id, code, name, latitude, longitude FROM provinces WHERE 1=1 ORDER BY name`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "country_id", "code", "name", "latitude", "longitude"}).
			AddRow(31, 360, "31", "Jakarta", "-6.2", "106.8"))

	referenceProvincesHandler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestReferenceProvincesHandler_FilterAndSearch(t *testing.T) {
	m, _ := refFixture(t)
	c, rec := refCtx(http.MethodGet, "/api/v1/reference/provinces?country_id=360&q=j")

	m.ExpectQuery(`SELECT id, country_id, code, name, latitude, longitude FROM provinces WHERE 1=1 AND country_id = \? AND name LIKE \? ORDER BY name`).
		WithArgs(360, "%j%").
		WillReturnRows(sqlmock.NewRows([]string{"id", "country_id", "code", "name", "latitude", "longitude"}))

	referenceProvincesHandler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestReferenceProvinceDetailHandler_Success(t *testing.T) {
	m, _ := refFixture(t)
	c, rec := refCtx(http.MethodGet, "/api/v1/reference/provinces/31")
	c.Params = gin.Params{{Key: "id", Value: "31"}}

	m.ExpectQuery(`SELECT id, country_id, code, name, latitude, longitude FROM provinces WHERE id = \?`).
		WithArgs(uint64(31)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "country_id", "code", "name", "latitude", "longitude"}).
			AddRow(31, 360, "31", "Jakarta", "-6.2", "106.8"))

	referenceProvinceDetailHandler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestReferenceProvinceDetailHandler_NotFound(t *testing.T) {
	m, _ := refFixture(t)
	c, rec := refCtx(http.MethodGet, "/api/v1/reference/provinces/99")
	c.Params = gin.Params{{Key: "id", Value: "99"}}

	m.ExpectQuery(`SELECT id, country_id, code, name, latitude, longitude FROM provinces WHERE id = \?`).
		WithArgs(uint64(99)).WillReturnError(sql.ErrNoRows)

	referenceProvinceDetailHandler(c)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// reference cities
// ---------------------------------------------------------------------------

func TestReferenceCitiesHandler_Success(t *testing.T) {
	m, _ := refFixture(t)
	c, rec := refCtx(http.MethodGet, "/api/v1/reference/cities?province_id=31")

	m.ExpectQuery(`SELECT id, country_id, province_id, code, name, latitude, longitude FROM cities WHERE 1=1 AND province_id = \? ORDER BY name`).
		WithArgs(31).
		WillReturnRows(sqlmock.NewRows([]string{"id", "country_id", "province_id", "code", "name", "latitude", "longitude"}).
			AddRow(3171, 360, 31, "3171", "Kota Jakarta Pusat", "-6.18", "106.83"))

	referenceCitiesHandler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestReferenceCityDetailHandler_Success(t *testing.T) {
	m, _ := refFixture(t)
	c, rec := refCtx(http.MethodGet, "/api/v1/reference/cities/3171")
	c.Params = gin.Params{{Key: "id", Value: "3171"}}

	m.ExpectQuery(`SELECT id, country_id, province_id, code, name, latitude, longitude FROM cities WHERE id = \?`).
		WithArgs(uint64(3171)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "country_id", "province_id", "code", "name", "latitude", "longitude"}).
			AddRow(3171, 360, 31, "3171", "Kota Jakarta Pusat", "-6.18", "106.83"))

	referenceCityDetailHandler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestReferenceCityDetailHandler_NotFound(t *testing.T) {
	m, _ := refFixture(t)
	c, rec := refCtx(http.MethodGet, "/api/v1/reference/cities/999")
	c.Params = gin.Params{{Key: "id", Value: "999"}}

	m.ExpectQuery(`SELECT id, country_id, province_id, code, name, latitude, longitude FROM cities WHERE id = \?`).
		WithArgs(uint64(999)).WillReturnError(sql.ErrNoRows)

	referenceCityDetailHandler(c)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// reference districts + subdistricts
// ---------------------------------------------------------------------------

func TestReferenceDistrictsHandler_Success(t *testing.T) {
	m, _ := refFixture(t)
	c, rec := refCtx(http.MethodGet, "/api/v1/reference/districts?city_id=3171")

	m.ExpectQuery(`SELECT id, city_id, code, name, postal_code, latitude, longitude FROM districts WHERE 1=1 AND city_id = \? ORDER BY name`).
		WithArgs(3171).
		WillReturnRows(sqlmock.NewRows([]string{"id", "city_id", "code", "name", "postal_code", "latitude", "longitude"}).
			AddRow(3171010, 3171, "3171010", "Menteng", "10310", "-6.19", "106.82"))

	referenceDistrictsHandler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestReferenceDistrictDetailHandler_Success(t *testing.T) {
	m, _ := refFixture(t)
	c, rec := refCtx(http.MethodGet, "/api/v1/reference/districts/3171010")
	c.Params = gin.Params{{Key: "id", Value: "3171010"}}

	m.ExpectQuery(`SELECT id, city_id, code, name, postal_code, latitude, longitude FROM districts WHERE id = \?`).
		WithArgs(uint64(3171010)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "city_id", "code", "name", "postal_code", "latitude", "longitude"}).
			AddRow(3171010, 3171, "3171010", "Menteng", "10310", "-6.19", "106.82"))

	referenceDistrictDetailHandler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestReferenceDistrictDetailHandler_NotFound(t *testing.T) {
	m, _ := refFixture(t)
	c, rec := refCtx(http.MethodGet, "/api/v1/reference/districts/999")
	c.Params = gin.Params{{Key: "id", Value: "999"}}

	m.ExpectQuery(`SELECT id, city_id, code, name, postal_code, latitude, longitude FROM districts WHERE id = \?`).
		WithArgs(uint64(999)).WillReturnError(sql.ErrNoRows)

	referenceDistrictDetailHandler(c)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestReferenceSubdistrictsHandler_Success(t *testing.T) {
	m, _ := refFixture(t)
	c, rec := refCtx(http.MethodGet, "/api/v1/reference/subdistricts?district_id=3171010&page=1&limit=10")

	m.ExpectQuery(`SELECT COUNT\(\*\) FROM subdistricts WHERE 1=1 AND district_id = \?`).
		WithArgs(3171010).
		WillReturnRows(sqlmock.NewRows([]string{"COUNT(*)"}).AddRow(int64(2)))
	m.ExpectQuery(`SELECT id, district_id, code, name, postal_code, latitude, longitude FROM subdistricts WHERE 1=1 AND district_id = \? ORDER BY name LIMIT \? OFFSET \?`).
		WithArgs(3171010, 10, 0).
		WillReturnRows(sqlmock.NewRows([]string{"id", "district_id", "code", "name", "postal_code", "latitude", "longitude"}).
			AddRow(3171010001, 3171010, "3171010001", "Cikini", "10310", "-6.19", "106.83"))

	referenceSubdistrictsHandler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestReferenceSubdistrictDetailHandler_Success(t *testing.T) {
	m, _ := refFixture(t)
	c, rec := refCtx(http.MethodGet, "/api/v1/reference/subdistricts/3171010001")
	c.Params = gin.Params{{Key: "id", Value: "3171010001"}}

	m.ExpectQuery(`SELECT id, district_id, code, name, postal_code, latitude, longitude FROM subdistricts WHERE id = \?`).
		WithArgs(uint64(3171010001)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "district_id", "code", "name", "postal_code", "latitude", "longitude"}).
			AddRow(3171010001, 3171010, "3171010001", "Cikini", "10310", "-6.19", "106.83"))

	referenceSubdistrictDetailHandler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestReferenceSubdistrictDetailHandler_NotFound(t *testing.T) {
	m, _ := refFixture(t)
	c, rec := refCtx(http.MethodGet, "/api/v1/reference/subdistricts/999")
	c.Params = gin.Params{{Key: "id", Value: "999"}}

	m.ExpectQuery(`SELECT id, district_id, code, name, postal_code, latitude, longitude FROM subdistricts WHERE id = \?`).
		WithArgs(uint64(999)).WillReturnError(sql.ErrNoRows)

	referenceSubdistrictDetailHandler(c)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// pure helpers: fetchRouteByID / loadAssignments / routeToItem / joinEq
// ---------------------------------------------------------------------------

func TestFetchRouteByID_Success(t *testing.T) {
	db, m := mockDB(t)
	m.ExpectQuery(`SELECT id, name, waypoints, estimated_duration_sec, created_by, is_active, created_at, updated_at
FROM routes WHERE id = \? AND is_active = TRUE`).
		WithArgs(uint64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "waypoints", "estimated_duration_sec", "created_by", "is_active", "created_at", "updated_at"}).
			AddRow(uint64(1), "R1", []byte(`[{"lat":-6.2,"lon":106.8},{"lat":-6.3,"lon":106.9}]`), int64(120), uint64(9), true, time.Now(), time.Now()))

	r, err := fetchRouteByID(db, 1)
	if err != nil {
		t.Fatalf("fetchRouteByID: %v", err)
	}
	if r.ID != 1 || r.Name != "R1" {
		t.Errorf("unexpected route: %+v", r)
	}
}

func TestFetchRouteByID_ErrNoRows(t *testing.T) {
	db, m := mockDB(t)
	m.ExpectQuery(`SELECT id, name, waypoints, estimated_duration_sec, created_by, is_active, created_at, updated_at
FROM routes WHERE id = \? AND is_active = TRUE`).
		WithArgs(uint64(2)).WillReturnError(sql.ErrNoRows)
	if _, err := fetchRouteByID(db, 2); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestLoadAssignments_Success(t *testing.T) {
	db, m := mockDB(t)
	now := time.Now()
	m.ExpectQuery(`SELECT ra.id, ra.route_id, ra.vehicle_id, ra.driver_user_id, ra.status,
ra.started_at, ra.completed_at, ra.deviation_meters, COALESCE\(v.imei,''\)`).
		WithArgs(uint64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "route_id", "vehicle_id", "driver_user_id", "status", "started_at", "completed_at", "deviation_meters", "imei"}).
			AddRow(uint64(11), uint64(1), uint64(42), int64(9), "in_progress", now, nil, 13.5, "864000000041234"))

	items := loadAssignments(db, 1)
	if len(items) != 1 || items[0].Status != "in_progress" || items[0].IMEI != "864000000041234" {
		t.Errorf("unexpected assignments: %+v", items)
	}
}

func TestLoadAssignments_QueryError(t *testing.T) {
	db, m := mockDB(t)
	m.ExpectQuery(`SELECT ra.id, ra.route_id, ra.vehicle_id, ra.driver_user_id, ra.status,
ra.started_at, ra.completed_at, ra.deviation_meters, COALESCE\(v.imei,''\)`).
		WithArgs(uint64(9)).WillReturnError(sql.ErrNoRows)
	if items := loadAssignments(db, 9); len(items) != 0 {
		t.Errorf("expected empty, got %+v", items)
	}
}

func TestRouteToItem_Waypoints(t *testing.T) {
	r := &routeRow{
		ID: 1, Name: "R", Waypoints: []byte(`[{"lat":-6.2,"lon":106.8},{"lat":-6.3,"lon":106.9}]`),
		EstimatedDurationSec: sql.NullInt64{Int64: 60, Valid: true},
		IsActive:             true,
	}
	item := routeToItem(nil, r) // db nil → no assignments query
	if len(item.Waypoints) != 2 || item.EstimatedDurationSec == nil || *item.EstimatedDurationSec != 60 {
		t.Errorf("unexpected item: %+v", item)
	}
}

func TestJoinEq(t *testing.T) {
	if got := joinEq([]string{"a = ?", "b = ?"}); got != "a = ?, b = ?" {
		t.Errorf("joinEq = %q", got)
	}
	if got := joinEq(nil); got != "" {
		t.Errorf("joinEq(nil) = %q", got)
	}
}

// ---------------------------------------------------------------------------
// routesListHandler
// ---------------------------------------------------------------------------

type driverValue = driver.Value

const routeListCols = "id,name,waypoints,estimated_duration_sec,created_by,is_active,created_at,updated_at"

func routeRowVals(id uint64, name string) []driverValue {
	return []driverValue{id, name, []byte(`[{"lat":-6.2,"lon":106.8}]`), int64(120), uint64(9), true, time.Now(), time.Now()}
}

func TestRoutesListHandler_Admin(t *testing.T) {
	db, m := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodGet, "/api/v1/routes", db, true, nil)

	m.ExpectQuery(`SELECT r.id, r.name, r.waypoints, r.estimated_duration_sec, r.created_by,
r.is_active, r.created_at, r.updated_at
FROM routes r WHERE r.is_active = TRUE ORDER BY r.id DESC`).
		WillReturnRows(sqlmock.NewRows(strings.Split(routeListCols, ",")).
			AddRow(routeRowVals(1, "R1")...))
	m.ExpectQuery(`SELECT ra.id, ra.route_id, ra.vehicle_id, ra.driver_user_id, ra.status,
ra.started_at, ra.completed_at, ra.deviation_meters, COALESCE\(v.imei,''\)`).
		WithArgs(uint64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "route_id", "vehicle_id", "driver_user_id", "status", "started_at", "completed_at", "deviation_meters", "imei"}))

	routesListHandler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRoutesListHandler_OperatorNoVehicles(t *testing.T) {
	db, _ := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodGet, "/api/v1/routes", db, false, map[uint64]struct{}{})
	routesListHandler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestRoutesListHandler_OperatorWithVehicles(t *testing.T) {
	db, m := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodGet, "/api/v1/routes", db, false, map[uint64]struct{}{42: {}})

	m.ExpectQuery(`SELECT r.id, r.name, r.waypoints, r.estimated_duration_sec, r.created_by,
r.is_active, r.created_at, r.updated_at
FROM routes r WHERE r.is_active = TRUE AND r\.id IN \(SELECT ra\.route_id FROM route_assignments ra
WHERE ra\.vehicle_id IN \(\?\) OR ra\.driver_user_id = \?\) ORDER BY r\.id DESC`).
		WithArgs(uint64(42), uint64(9)).
		WillReturnRows(sqlmock.NewRows(strings.Split(routeListCols, ",")).
			AddRow(routeRowVals(2, "R2")...))
	m.ExpectQuery(`SELECT ra.id, ra.route_id, ra.vehicle_id, ra.driver_user_id, ra.status,
ra.started_at, ra.completed_at, ra.deviation_meters, COALESCE\(v.imei,''\)`).
		WithArgs(uint64(2)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "route_id", "vehicle_id", "driver_user_id", "status", "started_at", "completed_at", "deviation_meters", "imei"}))

	routesListHandler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// routeDetailHandler + routeTrackHandler + routeDeleteHandler
// ---------------------------------------------------------------------------

func TestRouteDetailHandler_Found(t *testing.T) {
	db, m := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodGet, "/api/v1/routes/1", db, true, nil)
	c.Params = gin.Params{{Key: "id", Value: "1"}}

	m.ExpectQuery(`SELECT id, name, waypoints, estimated_duration_sec, created_by, is_active, created_at, updated_at
FROM routes WHERE id = \? AND is_active = TRUE`).WithArgs(uint64(1)).
		WillReturnRows(sqlmock.NewRows(strings.Split(routeListCols, ",")).
			AddRow(routeRowVals(1, "R1")...))
	m.ExpectQuery(`SELECT ra.id, ra.route_id, ra.vehicle_id, ra.driver_user_id, ra.status,
ra.started_at, ra.completed_at, ra.deviation_meters, COALESCE\(v.imei,''\)`).
		WithArgs(uint64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "route_id", "vehicle_id", "driver_user_id", "status", "started_at", "completed_at", "deviation_meters", "imei"}))

	routeDetailHandler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRouteDetailHandler_NotFound(t *testing.T) {
	db, m := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodGet, "/api/v1/routes/99", db, true, nil)
	c.Params = gin.Params{{Key: "id", Value: "99"}}

	m.ExpectQuery(`SELECT id, name, waypoints, estimated_duration_sec, created_by, is_active, created_at, updated_at
FROM routes WHERE id = \? AND is_active = TRUE`).WithArgs(uint64(99)).
		WillReturnError(sql.ErrNoRows)

	routeDetailHandler(c)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestRouteTrackHandler(t *testing.T) {
	db, m := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodGet, "/api/v1/routes/1/track", db, true, nil)
	c.Params = gin.Params{{Key: "id", Value: "1"}}

	m.ExpectQuery(`SELECT id, name, waypoints, estimated_duration_sec, created_by, is_active, created_at, updated_at
FROM routes WHERE id = \? AND is_active = TRUE`).WithArgs(uint64(1)).
		WillReturnRows(sqlmock.NewRows(strings.Split(routeListCols, ",")).
			AddRow(routeRowVals(1, "R1")...))
	m.ExpectQuery(`SELECT ra.id, ra.route_id, ra.vehicle_id, ra.driver_user_id, ra.status,
ra.started_at, ra.completed_at, ra.deviation_meters, COALESCE\(v.imei,''\)`).
		WithArgs(uint64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "route_id", "vehicle_id", "driver_user_id", "status", "started_at", "completed_at", "deviation_meters", "imei"}))

	routeTrackHandler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRouteDeleteHandler_Success(t *testing.T) {
	db, m := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodDelete, "/api/v1/routes/3", db, true, nil)
	c.Params = gin.Params{{Key: "id", Value: "3"}}

	m.ExpectExec(`UPDATE routes SET is_active = FALSE WHERE id = \? AND is_active = TRUE`).
		WithArgs(uint64(3)).WillReturnResult(sqlmock.NewResult(0, 1))

	routeDeleteHandler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRouteDeleteHandler_NotFound(t *testing.T) {
	db, m := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodDelete, "/api/v1/routes/88", db, true, nil)
	c.Params = gin.Params{{Key: "id", Value: "88"}}

	m.ExpectExec(`UPDATE routes SET is_active = FALSE WHERE id = \? AND is_active = TRUE`).
		WithArgs(uint64(88)).WillReturnResult(sqlmock.NewResult(0, 0))

	routeDeleteHandler(c)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestRouteDeleteHandler_NonAdmin(t *testing.T) {
	db, _ := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodDelete, "/api/v1/routes/3", db, false, nil)
	c.Params = gin.Params{{Key: "id", Value: "3"}}

	routeDeleteHandler(c)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// routesCreateHandler + routesUpdateHandler + routeAccessible
// ---------------------------------------------------------------------------

func TestRoutesCreateHandler_Success(t *testing.T) {
	db, m := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodPost, "/api/v1/routes", db, true, nil)

	body := `{"name":"R-New","waypoints":[{"lat":-6.2,"lon":106.8},{"lat":-6.3,"lon":106.9}],"vehicle_ids":[42]}`
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/routes", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	m.ExpectBegin()
	m.ExpectExec(`INSERT INTO routes \(name, waypoints, estimated_duration_sec, created_by\)
VALUES \(\?, \?, \?, \?\)`).
		WithArgs("R-New", []byte(`[{"lat":-6.2,"lon":106.8},{"lat":-6.3,"lon":106.9}]`), nil, uint64(9)).
		WillReturnResult(sqlmock.NewResult(9, 1))
	m.ExpectExec(`INSERT INTO route_assignments \(route_id, vehicle_id, driver_user_id\)
VALUES \(\?, \?, \?\)`).WithArgs(int64(9), uint64(42), nil).
		WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectCommit()
	m.ExpectQuery(`SELECT id, name, waypoints, estimated_duration_sec, created_by, is_active, created_at, updated_at
FROM routes WHERE id = \? AND is_active = TRUE`).WithArgs(uint64(9)).
		WillReturnRows(sqlmock.NewRows(strings.Split(routeListCols, ",")).
			AddRow(routeRowVals(9, "R-New")...))
	m.ExpectQuery(`SELECT ra.id, ra.route_id, ra.vehicle_id, ra.driver_user_id, ra.status,
ra.started_at, ra.completed_at, ra.deviation_meters, COALESCE\(v.imei,''\)`).
		WithArgs(uint64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "route_id", "vehicle_id", "driver_user_id", "status", "started_at", "completed_at", "deviation_meters", "imei"}))

	routesCreateHandler(c)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	if err := m.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestRoutesCreateHandler_Validation(t *testing.T) {
	db, _ := mockDB(t)

	// Missing name.
	c, rec := ginCtxWithDB(http.MethodPost, "/api/v1/routes", db, true, nil)
	body := `{"waypoints":[{"lat":-6.2,"lon":106.8},{"lat":-6.3,"lon":106.9}]}`
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/routes", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	routesCreateHandler(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 (no name), got %d", rec.Code)
	}

	// Single waypoint.
	c2, rec2 := ginCtxWithDB(http.MethodPost, "/api/v1/routes", db, true, nil)
	body2 := `{"name":"R","waypoints":[{"lat":-6.2,"lon":106.8}]}`
	c2.Request = httptest.NewRequest(http.MethodPost, "/api/v1/routes", strings.NewReader(body2))
	c2.Request.Header.Set("Content-Type", "application/json")
	routesCreateHandler(c2)
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 (<2 waypoints), got %d", rec2.Code)
	}
}

func TestRoutesCreateHandler_NonAdmin(t *testing.T) {
	db, _ := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodPost, "/api/v1/routes", db, false, nil)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/routes", strings.NewReader(`{}`))
	c.Request.Header.Set("Content-Type", "application/json")
	routesCreateHandler(c)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestRoutesUpdateHandler_Success(t *testing.T) {
	db, m := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodPatch, "/api/v1/routes/1", db, true, nil)
	c.Params = gin.Params{{Key: "id", Value: "1"}}

	body := `{"name":"R1 Renamed"}`
	c.Request = httptest.NewRequest(http.MethodPatch, "/api/v1/routes/1", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	m.ExpectQuery(`SELECT id, name, waypoints, estimated_duration_sec, created_by, is_active, created_at, updated_at
FROM routes WHERE id = \? AND is_active = TRUE`).WithArgs(uint64(1)).
		WillReturnRows(sqlmock.NewRows(strings.Split(routeListCols, ",")).
			AddRow(routeRowVals(1, "R1")...))
	m.ExpectExec(`UPDATE routes SET name = \? WHERE id = \? AND is_active = TRUE`).
		WithArgs("R1 Renamed", uint64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectQuery(`SELECT id, name, waypoints, estimated_duration_sec, created_by, is_active, created_at, updated_at
FROM routes WHERE id = \? AND is_active = TRUE`).WithArgs(uint64(1)).
		WillReturnRows(sqlmock.NewRows(strings.Split(routeListCols, ",")).
			AddRow(routeRowVals(1, "R1 Renamed")...))
	m.ExpectQuery(`SELECT ra.id, ra.route_id, ra.vehicle_id, ra.driver_user_id, ra.status,
ra.started_at, ra.completed_at, ra.deviation_meters, COALESCE\(v.imei,''\)`).
		WithArgs(uint64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "route_id", "vehicle_id", "driver_user_id", "status", "started_at", "completed_at", "deviation_meters", "imei"}))

	routesUpdateHandler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRouteAccessibleDB(t *testing.T) {
	db, m := mockDB(t)
	r := &routeRow{ID: 1, Name: "R1"}

	c, rec := ginCtxWithDB(http.MethodGet, "/api/v1/routes/1", db, false, map[uint64]struct{}{42: {}})
	m.ExpectQuery(`SELECT COUNT\(\*\) FROM route_assignments ra WHERE ra.route_id = \? AND \(ra\.vehicle_id IN \(\?\) OR ra\.driver_user_id = \?\)`).
		WithArgs(uint64(1), uint64(42), uint64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"COUNT(*)"}).AddRow(int64(1)))
	if !routeAccessible(c, db, r) {
		t.Error("expected accessible")
	}
	_ = rec

	c2, _ := ginCtxWithDB(http.MethodGet, "/api/v1/routes/1", db, false, map[uint64]struct{}{42: {}})
	m.ExpectQuery(`SELECT COUNT\(\*\) FROM route_assignments ra WHERE ra.route_id = \? AND \(ra\.vehicle_id IN \(\?\) OR ra\.driver_user_id = \?\)`).
		WithArgs(uint64(1), uint64(42), uint64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"COUNT(*)"}).AddRow(int64(0)))
	if routeAccessible(c2, db, r) {
		t.Error("expected not accessible")
	}

	// Admin → always accessible without DB query.
	c3, _ := ginCtxWithDB(http.MethodGet, "/api/v1/routes/1", db, true, nil)
	if !routeAccessible(c3, db, r) {
		t.Error("admin should access any route")
	}
}
