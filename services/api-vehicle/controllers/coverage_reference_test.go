package controllers

// coverage_reference_test.go (B4 2026-09-08): master reference data handlers
// (countries/provinces/cities/districts/subdistricts) based on sqlmock.

import (
	"net/http"
	"net/http/httptest"
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

// masterCtx builds a plain gin context (no company DB) + stub master DB mock.
func masterCtx(t *testing.T) (*gin.Context, sqlmock.Sqlmock) {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("GET", "/", nil)
	c.Request.RemoteAddr = "192.0.2.1:1234"
	_, mm, _ := stubMasterDB(t)
	return c, mm
}

// ---------------------------------------------------------------------
// countries
// ---------------------------------------------------------------------

func TestRefCountriesList(t *testing.T) {
	t.Helper()
	c, m := masterCtx(t)

	m.ExpectQuery("(?i)FROM countries WHERE is_active = TRUE ORDER BY name").
		WillReturnRows(sqlmock.NewRows([]string{"id", "iso_code", "iso_code_3", "name", "phone_code", "currency_code", "is_active"}).
			AddRow(uint64(1), "ID", "IDN", "Indonesia", "62", "IDR", true))

	referenceCountriesHandler(c)

	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("countries list = %d, want 200", code)
	}
}

func TestRefCountriesListSearch(t *testing.T) {
	t.Helper()
	c, m := masterCtx(t)
	c.Request = httptest.NewRequest("GET", "/?q=indo", nil)
	c.Request.RemoteAddr = "192.0.2.1:1234"

	m.ExpectQuery("(?i)FROM countries WHERE is_active = TRUE AND name LIKE \\? ORDER BY name").
		WithArgs("%indo%").WillReturnRows(sqlmock.NewRows([]string{"id", "iso_code", "iso_code_3", "name", "phone_code", "currency_code", "is_active"}))

	referenceCountriesHandler(c)

	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("countries search = %d, want 200", code)
	}
}

func TestRefCountriesDetailNotFound(t *testing.T) {
	t.Helper()
	c, m := masterCtx(t)
	c.AddParam("id", "404")

	m.ExpectQuery("(?i)FROM countries WHERE id = \\? AND is_active = TRUE").
		WithArgs(uint64(404)).WillReturnRows(sqlmock.NewRows([]string{"id", "iso_code", "iso_code_3", "name", "phone_code", "currency_code", "is_active"}))

	referenceCountryDetailHandler(c)

	if code := c.Writer.Status(); code != http.StatusNotFound {
		t.Fatalf("country 404 = %d, want 404", code)
	}
}

func TestRefCountriesDetailBadID(t *testing.T) {
	t.Helper()
	c, _ := masterCtx(t)
	c.AddParam("id", "abc")

	referenceCountryDetailHandler(c)

	if code := c.Writer.Status(); code != http.StatusBadRequest {
		t.Fatalf("country bad id = %d, want 400", code)
	}
}

// ---------------------------------------------------------------------
// provinces
// ---------------------------------------------------------------------

func TestRefProvincesList(t *testing.T) {
	t.Helper()
	c, m := masterCtx(t)

	m.ExpectQuery("(?i)FROM provinces WHERE 1=1 ORDER BY name").
		WillReturnRows(sqlmock.NewRows([]string{"id", "country_id", "code", "name", "latitude", "longitude"}).
			AddRow(uint64(1), uint64(1), "11", "DKI Jakarta", nil, nil))

	referenceProvincesHandler(c)

	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("provinces list = %d, want 200", code)
	}
}

func TestRefProvincesDetailNotFound2(t *testing.T) {
	t.Helper()
	c, m := masterCtx(t)
	c.AddParam("id", "404")

	m.ExpectQuery("(?i)FROM provinces WHERE id = \\?").
		WithArgs(uint64(404)).WillReturnRows(sqlmock.NewRows([]string{"id", "country_id", "code", "name", "latitude", "longitude"}))

	referenceProvinceDetailHandler(c)

	if code := c.Writer.Status(); code != http.StatusNotFound {
		t.Fatalf("province 404 = %d, want 404", code)
	}
}

// ---------------------------------------------------------------------
// cities / districts / subdistricts
// ---------------------------------------------------------------------

func TestRefCitiesList(t *testing.T) {
	t.Helper()
	c, m := masterCtx(t)

	m.ExpectQuery("(?i)FROM cities WHERE 1=1 ORDER BY name").
		WillReturnRows(sqlmock.NewRows([]string{"id", "country_id", "province_id", "code", "name", "latitude", "longitude"}).
			AddRow(uint64(1), uint64(1), int64(1), "33", "Kota Jakarta", nil, nil))

	referenceCitiesHandler(c)

	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("cities list = %d, want 200", code)
	}
}

func TestRefCitiesDetailNotFound(t *testing.T) {
	t.Helper()
	c, m := masterCtx(t)
	c.AddParam("id", "404")

	m.ExpectQuery("(?i)FROM cities WHERE id = \\?").
		WithArgs(uint64(404)).WillReturnRows(sqlmock.NewRows([]string{"id", "country_id", "province_id", "code", "name", "latitude", "longitude"}))

	referenceCityDetailHandler(c)

	if code := c.Writer.Status(); code != http.StatusNotFound {
		t.Fatalf("city 404 = %d, want 404", code)
	}
}

func TestRefDistrictsList(t *testing.T) {
	t.Helper()
	c, m := masterCtx(t)

	m.ExpectQuery("(?i)FROM districts WHERE 1=1 ORDER BY name").
		WillReturnRows(sqlmock.NewRows([]string{"id", "city_id", "code", "name", "postal_code", "latitude", "longitude"}).
			AddRow(uint64(1), uint64(1), "01", "Kec A", nil, nil, nil))

	referenceDistrictsHandler(c)

	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("districts list = %d, want 200", code)
	}
}

func TestRefDistrictsDetailNotFound(t *testing.T) {
	t.Helper()
	c, m := masterCtx(t)
	c.AddParam("id", "404")

	m.ExpectQuery("(?i)FROM districts WHERE id = \\?").
		WithArgs(uint64(404)).WillReturnRows(sqlmock.NewRows([]string{"id", "city_id", "code", "name", "postal_code", "latitude", "longitude"}))

	referenceDistrictDetailHandler(c)

	if code := c.Writer.Status(); code != http.StatusNotFound {
		t.Fatalf("district 404 = %d, want 404", code)
	}
}

func TestRefSubdistrictsList(t *testing.T) {
	t.Helper()
	c, m := masterCtx(t)

	m.ExpectQuery("(?i)COUNT\\(\\*\\) FROM subdistricts WHERE 1=1$").
		WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(1)))
	m.ExpectQuery("(?i)FROM subdistricts WHERE 1=1 ORDER BY name LIMIT \\? OFFSET \\?$").
		WithArgs(100, 0).
		WillReturnRows(sqlmock.NewRows([]string{"id", "district_id", "code", "name", "postal_code", "latitude", "longitude"}).
			AddRow(uint64(1), uint64(1), "01", "Desa A", nil, nil, nil))

	referenceSubdistrictsHandler(c)

	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("subdistricts list = %d, want 200", code)
	}
}

func TestRefSubdistrictsDetailNotFound(t *testing.T) {
	t.Helper()
	c, m := masterCtx(t)
	c.AddParam("id", "404")

	m.ExpectQuery("(?i)FROM subdistricts WHERE id = \\?").
		WithArgs(uint64(404)).WillReturnRows(sqlmock.NewRows([]string{"id", "district_id", "code", "name", "postal_code", "latitude", "longitude"}))

	referenceSubdistrictDetailHandler(c)

	if code := c.Writer.Status(); code != http.StatusNotFound {
		t.Fatalf("subdistrict 404 = %d, want 404", code)
	}
}

func TestRefSubdistrictsListFiltered(t *testing.T) {
	t.Helper()
	c, m := masterCtx(t)
	c.Request = httptest.NewRequest("GET", "/?district_id=1&q=desa", nil)
	c.Request.RemoteAddr = "192.0.2.1:1234"

	m.ExpectQuery("(?i)COUNT\\(\\*\\) FROM subdistricts WHERE 1=1 AND district_id = \\? AND name LIKE \\?$").
		WithArgs(1, "%desa%").WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(0)))
	m.ExpectQuery("(?i)FROM subdistricts WHERE 1=1 AND district_id = \\? AND name LIKE \\? ORDER BY name LIMIT \\? OFFSET \\?$").
		WithArgs(1, "%desa%", 100, 0).
		WillReturnRows(sqlmock.NewRows([]string{"id", "district_id", "code", "name", "postal_code", "latitude", "longitude"}))

	referenceSubdistrictsHandler(c)

	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("subdistricts filtered = %d, want 200", code)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}