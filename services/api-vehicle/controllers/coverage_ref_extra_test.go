package controllers

// coverage_ref_extra_test.go (B4 coverage api-vehicle 2026-09-09): menutup
// cabang reference handlers yang belum tercakup — success/error paths &
// filter query — memakai pola masterCtx \\ sqlmock dari
// coverage_reference_test.go / b4_mock_test.go.

import (
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

var refCountryCols = []string{"id", "iso_code", "iso_code_3", "name", "phone_code", "currency_code", "is_active"}
var refProvinceCols = []string{"id", "country_id", "code", "name", "latitude", "longitude"}
var refCityCols = []string{"id", "country_id", "province_id", "code", "name", "latitude", "longitude"}
var refDistrictCols = []string{"id", "city_id", "code", "name", "postal_code", "latitude", "longitude"}
var refSubdistrictCols = []string{"id", "district_id", "code", "name", "postal_code", "latitude", "longitude"}

// ---------------------------------------------------------------------
// countries list — query error
// ---------------------------------------------------------------------

func TestRefCountriesListQueryError(t *testing.T) {
	t.Helper()
	c, m := masterCtx(t)
	m.ExpectQuery("(?i)FROM countries WHERE is_active = TRUE").WillReturnError(errors.New("boom"))

	referenceCountriesHandler(c)

	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("countries query err = %d, want 500", code)
	}
}

// ---------------------------------------------------------------------
// countries detail — success \\ db error
// ---------------------------------------------------------------------

func TestRefCountryDetailSuccess(t *testing.T) {
	t.Helper()
	c, m := masterCtx(t)
	c.AddParam("id", "360")

	m.ExpectQuery("(?i)FROM countries WHERE id = \\? AND is_active = TRUE").
		WithArgs(uint64(360)).
		WillReturnRows(sqlmock.NewRows(refCountryCols).
			AddRow(uint64(360), "ID", "IDN", "Indonesia", "62", "IDR", true))

	referenceCountryDetailHandler(c)

	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("country detail = %d, want 200", code)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRefCountryDetailDBError(t *testing.T) {
	t.Helper()
	c, m := masterCtx(t)
	c.AddParam("id", "101")

	m.ExpectQuery("(?i)FROM countries WHERE id = \\? AND is_active = TRUE").
		WithArgs(uint64(101)).WillReturnError(errors.New("boom"))

	referenceCountryDetailHandler(c)

	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("country detail db err = %d, want 500", code)
	}
}

// ---------------------------------------------------------------------
// provinces
// ---------------------------------------------------------------------

func TestRefProvincesListFiltered(t *testing.T) {
	t.Helper()
	c, m := masterCtx(t)
	c.Request = httptest.NewRequest("GET", "/?country_id=1&q=ja", nil)

	m.ExpectQuery("(?i)FROM provinces WHERE 1=1 AND country_id = \\? AND name LIKE \\? ORDER BY name").
		WithArgs(1, "%ja%").
		WillReturnRows(sqlmock.NewRows(refProvinceCols).
			AddRow(uint64(1), uint64(1), "32", "Jawa Barat", "lat", "lon"))

	referenceProvincesHandler(c)

	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("provinces filtered = %d, want 200", code)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRefProvincesListQueryError(t *testing.T) {
	t.Helper()
	c, m := masterCtx(t)
	m.ExpectQuery("(?i)FROM provinces WHERE 1=1").WillReturnError(errors.New("boom"))

	referenceProvincesHandler(c)

	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("provinces query err = %d, want 500", code)
	}
}

func TestRefProvinceDetailSuccess(t *testing.T) {
	t.Helper()
	c, m := masterCtx(t)
	c.AddParam("id", "11")

	m.ExpectQuery("(?i)FROM provinces WHERE id = \\?").
		WithArgs(uint64(11)).
		WillReturnRows(sqlmock.NewRows(refProvinceCols).
			AddRow(uint64(11), uint64(1), "11", "Aceh", nil, nil))

	referenceProvinceDetailHandler(c)

	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("province detail = %d, want 200", code)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRefProvinceDetailDBError(t *testing.T) {
	t.Helper()
	c, m := masterCtx(t)
	c.AddParam("id", "11")
	m.ExpectQuery("(?i)FROM provinces WHERE id = \\?").
		WithArgs(uint64(11)).WillReturnError(errors.New("boom"))

	referenceProvinceDetailHandler(c)

	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("province db err = %d, want 500", code)
	}
}

// ---------------------------------------------------------------------
// cities
// ---------------------------------------------------------------------

func TestRefCitiesListFiltered(t *testing.T) {
	t.Helper()
	c, m := masterCtx(t)
	c.Request = httptest.NewRequest("GET", "/?province_id=3&q=kab", nil)

	m.ExpectQuery("(?i)FROM cities WHERE 1=1 AND province_id = \\? AND name LIKE \\? ORDER BY name").
		WithArgs(3, "%kab%").
		WillReturnRows(sqlmock.NewRows(refCityCols).
			AddRow(uint64(1), uint64(1), uint64(32), "3201", "Kab Bogor", nil, nil))

	referenceCitiesHandler(c)

	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("cities filtered = %d, want 200", code)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRefCitiesListQueryError(t *testing.T) {
	t.Helper()
	c, m := masterCtx(t)
	m.ExpectQuery("(?i)FROM cities WHERE 1=1").WillReturnError(errors.New("boom"))

	referenceCitiesHandler(c)

	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("cities query err = %d, want 500", code)
	}
}

func TestRefCityDetailSuccess(t *testing.T) {
	t.Helper()
	c, m := masterCtx(t)
	c.AddParam("id", "3201")

	m.ExpectQuery("(?i)FROM cities WHERE id = \\?").
		WithArgs(uint64(3201)).
		WillReturnRows(sqlmock.NewRows(refCityCols).
			AddRow(uint64(3201), uint64(1), uint64(32), "3201", "Kab Bogor", nil, nil))

	referenceCityDetailHandler(c)

	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("city detail = %d, want 200", code)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// ---------------------------------------------------------------------
// districts
// ---------------------------------------------------------------------

func TestRefDistrictsListFiltered(t *testing.T) {
	t.Helper()
	c, m := masterCtx(t)
	c.Request = httptest.NewRequest("GET", "/?city_id=2001&q=kec", nil)

	m.ExpectQuery("(?i)FROM districts WHERE 1=1 AND city_id = \\? AND name LIKE \\? ORDER BY name").
		WithArgs(2001, "%kec%").
		WillReturnRows(sqlmock.NewRows(refDistrictCols).
			AddRow(uint64(1), uint64(2001), "01", "Kec A", nil, nil, nil))

	referenceDistrictsHandler(c)

	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("districts filtered = %d, want 200", code)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRefDistrictsListQueryError(t *testing.T) {
	t.Helper()
	c, m := masterCtx(t)
	m.ExpectQuery("(?i)FROM districts WHERE 1=1").WillReturnError(errors.New("boom"))

	referenceDistrictsHandler(c)

	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("districts query err = %d, want 500", code)
	}
}

func TestRefDistrictDetailSuccess(t *testing.T) {
	t.Helper()
	c, m := masterCtx(t)
	c.AddParam("id", "201")

	m.ExpectQuery("(?i)FROM districts WHERE id = \\?").
		WithArgs(uint64(201)).
		WillReturnRows(sqlmock.NewRows(refDistrictCols).
			AddRow(uint64(201), uint64(2001), "201", "Kec A", "16110", nil, nil))

	referenceDistrictDetailHandler(c)

	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("district detail = %d, want 200", code)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRefDistrictDetailDBError(t *testing.T) {
	t.Helper()
	c, m := masterCtx(t)
	c.AddParam("id", "201")
	m.ExpectQuery("(?i)FROM districts WHERE id = \\?").
		WithArgs(uint64(201)).WillReturnError(errors.New("boom"))

	referenceDistrictDetailHandler(c)

	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("district db err = %d, want 500", code)
	}
}

// ---------------------------------------------------------------------
// subdistricts
// ---------------------------------------------------------------------

func TestRefSubdistrictDetailSuccess(t *testing.T) {
	t.Helper()
	c, m := masterCtx(t)
	c.AddParam("id", "320101")

	m.ExpectQuery("(?i)FROM subdistricts WHERE id = \\?").
		WithArgs(uint64(320101)).
		WillReturnRows(sqlmock.NewRows(refSubdistrictCols).
			AddRow(uint64(320101), uint64(201), "001", "Desa C", nil, nil, nil))

	referenceSubdistrictDetailHandler(c)

	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("subdistrict detail = %d, want 200", code)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRefSubdistrictDetailDBError(t *testing.T) {
	t.Helper()
	c, m := masterCtx(t)
	c.AddParam("id", "320101")
	m.ExpectQuery("(?i)FROM subdistricts WHERE id = \\?").
		WithArgs(uint64(320101)).WillReturnError(errors.New("boom"))

	referenceSubdistrictDetailHandler(c)

	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("subdistrict db err = %d, want 500", code)
	}
}

func TestRefSubdistrictsCountError(t *testing.T) {
	t.Helper()
	c, m := masterCtx(t)
	m.ExpectQuery("(?i)COUNT\\(\\*\\) FROM subdistricts WHERE 1=1").WillReturnError(errors.New("boom"))

	referenceSubdistrictsHandler(c)

	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("subdistricts count err = %d, want 500", code)
	}
}

func TestRefSubdistrictsListQueryError(t *testing.T) {
	t.Helper()
	c, m := masterCtx(t)
	m.ExpectQuery("(?i)COUNT\\(\\*\\) FROM subdistricts WHERE 1=1").
		WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(0)))
	m.ExpectQuery("(?i)FROM subdistricts WHERE 1=1 ORDER BY name").
		WillReturnError(errors.New("boom"))

	referenceSubdistrictsHandler(c)

	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("subdistricts query err = %d, want 500", code)
	}
}

// ---------------------------------------------------------------------
// nullable helpers (handlers_reference.go: nullableStrP2 / nullableIntPtr)
// ---------------------------------------------------------------------

func TestNullableHelpersRef(t *testing.T) {
	t.Helper()
	nsOK := sql.NullString{String: "x", Valid: true}
	if v := nullableStrP2(nsOK); v == nil || *v != "x" {
		t.Fatal("nullableStrP2 valid mismatch")
	}
	if v := nullableStrP2(sql.NullString{}); v != nil {
		t.Fatal("nullableStrP2 invalid mismatch")
	}
	niOK := sql.NullInt64{Int64: 5, Valid: true}
	if v := nullableIntPtr(niOK); v == nil || *v != 5 {
		t.Fatal("nullableIntPtr valid mismatch")
	}
	if v := nullableIntPtr(sql.NullInt64{}); v != nil {
		t.Fatal("nullableIntPtr invalid mismatch")
	}
}