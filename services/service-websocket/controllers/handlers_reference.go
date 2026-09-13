package controllers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"ajb_gps/service-websocket/models"

	"github.com/gin-gonic/gin"
)

// Cache TTL untuk reference data (data statis, cache lama).
const refCacheTTL = 1 * time.Hour

// refCacheKey builds a Redis key for reference data cache.
func refCacheKey(prefix string, params string) string {
	return "adatrack_gps:ref:" + prefix + ":" + params
}

// refGetCache attempts to get cached reference data from Redis.
func refGetCache(ctx context.Context, key string) ([]byte, bool) {
	if appRedis == nil {
		return nil, false
	}
	val, err := appRedis.Get(ctx, key)
	if err != nil {
		return nil, false
	}
	return []byte(val), true
}

// refSetCache stores reference data in Redis with TTL.
func refSetCache(ctx context.Context, key string, data []byte) {
	if appRedis == nil {
		return
	}
	if err := appRedis.Set(ctx, key, data, refCacheTTL); err != nil {
		slog.Warn("ref cache set failed", "key", key, "error", err)
	}
}

// GET /api/v1/reference/countries?q= — daftar negara (ISO 3166-1), optional search by name.
func referenceCountriesHandler(c *gin.Context) {
	db := masterDBFn()
	if db == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	ctx := c.Request.Context()
	search := c.Query("q")

	cacheKey := refCacheKey("countries", search)
	if cached, ok := refGetCache(ctx, cacheKey); ok {
		c.Data(http.StatusOK, "application/json", cached)
		return
	}

	query := `SELECT id, iso_code, iso_code_3, name, phone_code, currency_code, is_active
		 FROM countries WHERE is_active = TRUE`
	args := []interface{}{}

	if search != "" {
		query += " AND name LIKE ?"
		args = append(args, "%"+search+"%")
	}
	query += " ORDER BY name"

	rows, err := db.Query(query, args...)
	if err != nil {
		slog.Error("reference countries query failed", "error", err)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	defer rows.Close()

	type countryJSON struct {
		ID           int    `json:"id"`
		IsoCode      string `json:"iso_code"`
		IsoCode3     string `json:"iso_code_3"`
		Name         string `json:"name"`
		PhoneCode    string `json:"phone_code"`
		CurrencyCode string `json:"currency_code"`
		IsActive     bool   `json:"is_active"`
	}

	countries := []countryJSON{}
	for rows.Next() {
		var ct countryJSON
		var iso3, phone, currency sql.NullString
		if err := rows.Scan(&ct.ID, &ct.IsoCode, &iso3, &ct.Name, &phone, &currency, &ct.IsActive); err == nil {
			ct.IsoCode3 = iso3.String
			ct.PhoneCode = phone.String
			ct.CurrencyCode = currency.String
			countries = append(countries, ct)
		}
	}

	if data, err := json.Marshal(countries); err == nil {
		refSetCache(ctx, cacheKey, data)
	}

	writeSuccess(c, http.StatusOK, countries)
}

// GET /api/v1/reference/countries/:id — detail satu negara.
func referenceCountryDetailHandler(c *gin.Context) {
	db := masterDBFn()
	if db == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "country id must be a number")
		return
	}

	ctx := c.Request.Context()
	cacheKey := refCacheKey("country", fmt.Sprintf("%d", id))

	if cached, ok := refGetCache(ctx, cacheKey); ok {
		c.Data(http.StatusOK, "application/json", cached)
		return
	}

	type countryJSON struct {
		ID           int    `json:"id"`
		IsoCode      string `json:"iso_code"`
		IsoCode3     string `json:"iso_code_3"`
		Name         string `json:"name"`
		PhoneCode    string `json:"phone_code"`
		CurrencyCode string `json:"currency_code"`
		IsActive     bool   `json:"is_active"`
	}

	var ct countryJSON
	var iso3, phone, currency sql.NullString
	err = db.QueryRow(
		`SELECT id, iso_code, iso_code_3, name, phone_code, currency_code, is_active
		 FROM countries WHERE id = ? AND is_active = TRUE`, id,
	).Scan(&ct.ID, &ct.IsoCode, &iso3, &ct.Name, &phone, &currency, &ct.IsActive)

	if err == sql.ErrNoRows {
		writeError(c, http.StatusNotFound, "NOT_FOUND", "country not found")
		return
	}
	if err != nil {
		slog.Error("reference country detail failed", "error", err)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	ct.IsoCode3 = iso3.String
	ct.PhoneCode = phone.String
	ct.CurrencyCode = currency.String

	if data, err := json.Marshal(ct); err == nil {
		refSetCache(ctx, cacheKey, data)
	}

	writeSuccess(c, http.StatusOK, ct)
}

// GET /api/v1/reference/provinces?country_id=&q= — daftar provinsi (filter by country, search by name).
func referenceProvincesHandler(c *gin.Context) {
	db := masterDBFn()
	if db == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	ctx := c.Request.Context()
	countryID := atoiDefault(c.Query("country_id"), 0)
	search := c.Query("q")

	cacheKey := refCacheKey("provinces", fmt.Sprintf("c=%d&q=%s", countryID, search))
	if cached, ok := refGetCache(ctx, cacheKey); ok {
		c.Data(http.StatusOK, "application/json", cached)
		return
	}

	query := `SELECT id, country_id, code, name, latitude, longitude FROM provinces WHERE 1=1`
	args := []interface{}{}

	if countryID > 0 {
		query += " AND country_id = ?"
		args = append(args, countryID)
	}
	if search != "" {
		query += " AND name LIKE ?"
		args = append(args, "%"+search+"%")
	}
	query += " ORDER BY name"

	rows, err := db.Query(query, args...)
	if err != nil {
		slog.Error("reference provinces query failed", "error", err)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	defer rows.Close()

	type provinceJSON struct {
		ID        int     `json:"id"`
		CountryID int     `json:"country_id"`
		Code      string  `json:"code"`
		Name      string  `json:"name"`
		Latitude  *string `json:"latitude,omitempty"`
		Longitude *string `json:"longitude,omitempty"`
	}

	provinces := []provinceJSON{}
	for rows.Next() {
		var p provinceJSON
		var lat, lon sql.NullString
		if err := rows.Scan(&p.ID, &p.CountryID, &p.Code, &p.Name, &lat, &lon); err == nil {
			p.Latitude = nullableStrP(lat)
			p.Longitude = nullableStrP(lon)
			provinces = append(provinces, p)
		}
	}

	if data, err := json.Marshal(provinces); err == nil {
		refSetCache(ctx, cacheKey, data)
	}

	writeSuccess(c, http.StatusOK, provinces)
}

// GET /api/v1/reference/provinces/:id — detail satu provinsi.
func referenceProvinceDetailHandler(c *gin.Context) {
	db := masterDBFn()
	if db == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "province id must be a number")
		return
	}

	ctx := c.Request.Context()
	cacheKey := refCacheKey("province", fmt.Sprintf("%d", id))

	if cached, ok := refGetCache(ctx, cacheKey); ok {
		c.Data(http.StatusOK, "application/json", cached)
		return
	}

	type provinceJSON struct {
		ID        int     `json:"id"`
		CountryID int     `json:"country_id"`
		Code      string  `json:"code"`
		Name      string  `json:"name"`
		Latitude  *string `json:"latitude,omitempty"`
		Longitude *string `json:"longitude,omitempty"`
	}

	var p provinceJSON
	var lat, lon sql.NullString
	err = db.QueryRow(
		`SELECT id, country_id, code, name, latitude, longitude FROM provinces WHERE id = ?`, id,
	).Scan(&p.ID, &p.CountryID, &p.Code, &p.Name, &lat, &lon)

	if err == sql.ErrNoRows {
		writeError(c, http.StatusNotFound, "NOT_FOUND", "province not found")
		return
	}
	if err != nil {
		slog.Error("reference province detail failed", "error", err)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	p.Latitude = nullableStrP(lat)
	p.Longitude = nullableStrP(lon)

	if data, err := json.Marshal(p); err == nil {
		refSetCache(ctx, cacheKey, data)
	}

	writeSuccess(c, http.StatusOK, p)
}

// GET /api/v1/reference/cities?province_id=&q= — daftar kabupaten/kota.
func referenceCitiesHandler(c *gin.Context) {
	db := masterDBFn()
	if db == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	ctx := c.Request.Context()
	provinceID := atoiDefault(c.Query("province_id"), 0)
	search := c.Query("q")

	cacheKey := refCacheKey("cities", fmt.Sprintf("p=%d&q=%s", provinceID, search))
	if cached, ok := refGetCache(ctx, cacheKey); ok {
		c.Data(http.StatusOK, "application/json", cached)
		return
	}

	query := `SELECT id, country_id, province_id, code, name, latitude, longitude FROM cities WHERE 1=1`
	args := []interface{}{}

	if provinceID > 0 {
		query += " AND province_id = ?"
		args = append(args, provinceID)
	}
	if search != "" {
		query += " AND name LIKE ?"
		args = append(args, "%"+search+"%")
	}
	query += " ORDER BY name"

	rows, err := db.Query(query, args...)
	if err != nil {
		slog.Error("reference cities query failed", "error", err)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	defer rows.Close()

	type cityJSON struct {
		ID         int     `json:"id"`
		CountryID  int     `json:"country_id"`
		ProvinceID *int    `json:"province_id,omitempty"`
		Code       string  `json:"code"`
		Name       string  `json:"name"`
		Latitude   *string `json:"latitude,omitempty"`
		Longitude  *string `json:"longitude,omitempty"`
	}

	cities := []cityJSON{}
	for rows.Next() {
		var ci cityJSON
		var provID sql.NullInt64
		var lat, lon sql.NullString
		if err := rows.Scan(&ci.ID, &ci.CountryID, &provID, &ci.Code, &ci.Name, &lat, &lon); err == nil {
			ci.ProvinceID = nullableIntPtr(provID)
			ci.Latitude = nullableStrP(lat)
			ci.Longitude = nullableStrP(lon)
			cities = append(cities, ci)
		}
	}

	if data, err := json.Marshal(cities); err == nil {
		refSetCache(ctx, cacheKey, data)
	}

	writeSuccess(c, http.StatusOK, cities)
}

// GET /api/v1/reference/cities/:id — detail satu kota/kabupaten.
func referenceCityDetailHandler(c *gin.Context) {
	db := masterDBFn()
	if db == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "city id must be a number")
		return
	}

	ctx := c.Request.Context()
	cacheKey := refCacheKey("city", fmt.Sprintf("%d", id))

	if cached, ok := refGetCache(ctx, cacheKey); ok {
		c.Data(http.StatusOK, "application/json", cached)
		return
	}

	type cityJSON struct {
		ID         int     `json:"id"`
		CountryID  int     `json:"country_id"`
		ProvinceID *int    `json:"province_id,omitempty"`
		Code       string  `json:"code"`
		Name       string  `json:"name"`
		Latitude   *string `json:"latitude,omitempty"`
		Longitude  *string `json:"longitude,omitempty"`
	}

	var ci cityJSON
	var provID sql.NullInt64
	var lat, lon sql.NullString
	err = db.QueryRow(
		`SELECT id, country_id, province_id, code, name, latitude, longitude FROM cities WHERE id = ?`, id,
	).Scan(&ci.ID, &ci.CountryID, &provID, &ci.Code, &ci.Name, &lat, &lon)

	if err == sql.ErrNoRows {
		writeError(c, http.StatusNotFound, "NOT_FOUND", "city not found")
		return
	}
	if err != nil {
		slog.Error("reference city detail failed", "error", err)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	ci.ProvinceID = nullableIntPtr(provID)
	ci.Latitude = nullableStrP(lat)
	ci.Longitude = nullableStrP(lon)

	if data, err := json.Marshal(ci); err == nil {
		refSetCache(ctx, cacheKey, data)
	}

	writeSuccess(c, http.StatusOK, ci)
}

// GET /api/v1/reference/districts?city_id=&q= — daftar kecamatan.
func referenceDistrictsHandler(c *gin.Context) {
	db := masterDBFn()
	if db == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	ctx := c.Request.Context()
	cityID := atoiDefault(c.Query("city_id"), 0)
	search := c.Query("q")

	cacheKey := refCacheKey("districts", fmt.Sprintf("c=%d&q=%s", cityID, search))
	if cached, ok := refGetCache(ctx, cacheKey); ok {
		c.Data(http.StatusOK, "application/json", cached)
		return
	}

	query := `SELECT id, city_id, code, name, postal_code, latitude, longitude FROM districts WHERE 1=1`
	args := []interface{}{}

	if cityID > 0 {
		query += " AND city_id = ?"
		args = append(args, cityID)
	}
	if search != "" {
		query += " AND name LIKE ?"
		args = append(args, "%"+search+"%")
	}
	query += " ORDER BY name"

	rows, err := db.Query(query, args...)
	if err != nil {
		slog.Error("reference districts query failed", "error", err)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	defer rows.Close()

	type districtJSON struct {
		ID         int     `json:"id"`
		CityID     int     `json:"city_id"`
		Code       string  `json:"code"`
		Name       string  `json:"name"`
		PostalCode *string `json:"postal_code,omitempty"`
		Latitude   *string `json:"latitude,omitempty"`
		Longitude  *string `json:"longitude,omitempty"`
	}

	districts := []districtJSON{}
	for rows.Next() {
		var d districtJSON
		var postal, lat, lon sql.NullString
		if err := rows.Scan(&d.ID, &d.CityID, &d.Code, &d.Name, &postal, &lat, &lon); err == nil {
			d.PostalCode = nullableStrP(postal)
			d.Latitude = nullableStrP(lat)
			d.Longitude = nullableStrP(lon)
			districts = append(districts, d)
		}
	}

	if data, err := json.Marshal(districts); err == nil {
		refSetCache(ctx, cacheKey, data)
	}

	writeSuccess(c, http.StatusOK, districts)
}

// GET /api/v1/reference/districts/:id — detail satu kecamatan.
func referenceDistrictDetailHandler(c *gin.Context) {
	db := masterDBFn()
	if db == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "district id must be a number")
		return
	}

	ctx := c.Request.Context()
	cacheKey := refCacheKey("district", fmt.Sprintf("%d", id))

	if cached, ok := refGetCache(ctx, cacheKey); ok {
		c.Data(http.StatusOK, "application/json", cached)
		return
	}

	type districtJSON struct {
		ID         int     `json:"id"`
		CityID     int     `json:"city_id"`
		Code       string  `json:"code"`
		Name       string  `json:"name"`
		PostalCode *string `json:"postal_code,omitempty"`
		Latitude   *string `json:"latitude,omitempty"`
		Longitude  *string `json:"longitude,omitempty"`
	}

	var d districtJSON
	var postal, lat, lon sql.NullString
	err = db.QueryRow(
		`SELECT id, city_id, code, name, postal_code, latitude, longitude FROM districts WHERE id = ?`, id,
	).Scan(&d.ID, &d.CityID, &d.Code, &d.Name, &postal, &lat, &lon)

	if err == sql.ErrNoRows {
		writeError(c, http.StatusNotFound, "NOT_FOUND", "district not found")
		return
	}
	if err != nil {
		slog.Error("reference district detail failed", "error", err)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	d.PostalCode = nullableStrP(postal)
	d.Latitude = nullableStrP(lat)
	d.Longitude = nullableStrP(lon)

	if data, err := json.Marshal(d); err == nil {
		refSetCache(ctx, cacheKey, data)
	}

	writeSuccess(c, http.StatusOK, d)
}

// GET /api/v1/reference/subdistricts?district_id=&q=&page=&limit= — daftar kelurahan/desa (pagination).
func referenceSubdistrictsHandler(c *gin.Context) {
	db := masterDBFn()
	if db == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	page, limit := paginationParams(c)
	offset := (page - 1) * limit

	districtID := atoiDefault(c.Query("district_id"), 0)
	search := c.Query("q")

	query := `SELECT id, district_id, code, name, postal_code, latitude, longitude FROM subdistricts WHERE 1=1`
	countQuery := `SELECT COUNT(*) FROM subdistricts WHERE 1=1`
	args := []interface{}{}

	if districtID > 0 {
		query += " AND district_id = ?"
		countQuery += " AND district_id = ?"
		args = append(args, districtID)
	}
	if search != "" {
		query += " AND name LIKE ?"
		countQuery += " AND name LIKE ?"
		args = append(args, "%"+search+"%")
	}

	var total int64
	if err := db.QueryRow(countQuery, args...).Scan(&total); err != nil {
		slog.Error("reference subdistricts count failed", "error", err)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	query += " ORDER BY name LIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	rows, err := db.Query(query, args...)
	if err != nil {
		slog.Error("reference subdistricts query failed", "error", err)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	defer rows.Close()

	type subdistrictJSON struct {
		ID         int     `json:"id"`
		DistrictID int     `json:"district_id"`
		Code       string  `json:"code"`
		Name       string  `json:"name"`
		PostalCode *string `json:"postal_code,omitempty"`
		Latitude   *string `json:"latitude,omitempty"`
		Longitude  *string `json:"longitude,omitempty"`
	}

	subdistricts := []subdistrictJSON{}
	for rows.Next() {
		var s subdistrictJSON
		var postal, lat, lon sql.NullString
		if err := rows.Scan(&s.ID, &s.DistrictID, &s.Code, &s.Name, &postal, &lat, &lon); err == nil {
			s.PostalCode = nullableStrP(postal)
			s.Latitude = nullableStrP(lat)
			s.Longitude = nullableStrP(lon)
			subdistricts = append(subdistricts, s)
		}
	}

	pagination := &models.PaginationInfo{Page: page, Limit: limit, Total: total}
	writeSuccess(c, http.StatusOK, subdistricts, pagination)
}

// GET /api/v1/reference/subdistricts/:id — detail satu kelurahan/desa.
func referenceSubdistrictDetailHandler(c *gin.Context) {
	db := masterDBFn()
	if db == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "subdistrict id must be a number")
		return
	}

	ctx := c.Request.Context()
	cacheKey := refCacheKey("subdistrict", fmt.Sprintf("%d", id))

	if cached, ok := refGetCache(ctx, cacheKey); ok {
		c.Data(http.StatusOK, "application/json", cached)
		return
	}

	type subdistrictJSON struct {
		ID         int     `json:"id"`
		DistrictID int     `json:"district_id"`
		Code       string  `json:"code"`
		Name       string  `json:"name"`
		PostalCode *string `json:"postal_code,omitempty"`
		Latitude   *string `json:"latitude,omitempty"`
		Longitude  *string `json:"longitude,omitempty"`
	}

	var s subdistrictJSON
	var postal, lat, lon sql.NullString
	err = db.QueryRow(
		`SELECT id, district_id, code, name, postal_code, latitude, longitude FROM subdistricts WHERE id = ?`, id,
	).Scan(&s.ID, &s.DistrictID, &s.Code, &s.Name, &postal, &lat, &lon)

	if err == sql.ErrNoRows {
		writeError(c, http.StatusNotFound, "NOT_FOUND", "subdistrict not found")
		return
	}
	if err != nil {
		slog.Error("reference subdistrict detail failed", "error", err)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	s.PostalCode = nullableStrP(postal)
	s.Latitude = nullableStrP(lat)
	s.Longitude = nullableStrP(lon)

	if data, err := json.Marshal(s); err == nil {
		refSetCache(ctx, cacheKey, data)
	}

	writeSuccess(c, http.StatusOK, s)
}
