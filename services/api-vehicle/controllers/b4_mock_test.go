package controllers

// b4_mock_test.go (B4 coverage api-vehicle 2026-09-07):
// helper bersama untuk test berbasis sqlmock: company gin context (companyDB/
// companyRead), stub masterDBFn / companyDBByCodeFn indirection, dan
// fake RedisCmd untuk tokenauth (tanpa butuh Redis/miniredis hidup.

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"ajb_gps/api-vehicle/models"
	"ajb_gps/internal"
	"ajb_gps/internal/tokenauth"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
)

func init() {
	gin.SetMode(gin.TestMode)
	// Metrik ter-init HANYA lewat RegisterMetrics (internal/metrics.go), bukan
	// package init. Dipanggil sekalipun di rbac_test2_test.go; panggil lagi aman
	// karena RegisterMetrics membuat instance metric global baru.
	_ = internal.RegisterMetrics(prometheus.NewRegistry())
}

func mockDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, mock
}

// companyCtx membangun gin context lengkap dengan company DB (primary+RO) pada
// ctxCompanyDBKey/ctxCompanyROKey sehingga companyDB()/companyRead() resolve.

// Non-admin diberi akses vehicle 1 dan vehicle 2; admin melihat semua.
func companyCtx(t *testing.T, admin bool, method, target, body string) (*gin.Context, *httptest.ResponseRecorder, sqlmock.Sqlmock) {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	c.Request = httptest.NewRequest(method, target, rdr)
	if body != "" {
		c.Request.Header.Set("Content-Type", "application/json")
	}
	c.Request.RemoteAddr = "192.0.2.1:1234"

	// Extract trailing numeric segment as :id param (e.g. /fuel-configs/5 → id=5)
	if parts := strings.Split(strings.Trim(target, "/"), "/"); len(parts) > 0 {
		last := parts[len(parts)-1]
		if n, err := strconv.Atoi(last); err == nil {
			c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(n)}}
		}
	}

	db, m := mockDB(t)
	c.Set(ctxCompanyDBKey, db)
	c.Set(ctxCompanyROKey, db)
	c.Set(ctxCompanyCodeKey, "DEV001")

	role := "Admin"
	if !admin {
		role = "Operator"
	}
	c.Set(ctxUserKey, models.AuthUser{ID: 7, CompanyCode: "DEV001", CompanyID: 1, Email: "u@dev001.io", Role: role, CompanyUserID: 7})
	c.Set(ctxAdminKey, admin)
	c.Set(ctxRoleKey, role)
	if admin {
		c.Set(ctxAllowedKey, map[uint64]struct{}{})
	} else {
		c.Set(ctxAllowedKey, map[uint64]struct{}{1: {}, 2: {}})
	}
	return c, rec, m
}

// stubMasterDB menimpa masterDBFn (silakan isi expectation pada mock), dan
// mengembalikan *sql.DB + sqlmock agar masterDB().* sync dites tanpa infra.

func stubMasterDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, m := mockDB(t)
	old := masterDBFn
	masterDBFn = func() *sql.DB { return db }
	cleanup := func() {
		masterDBFn = old
		_ = db.Close()
	}
	t.Cleanup(cleanup)
	return db, m, cleanup
}

// stubDBByCode menimpa companyDBByCodeFn/companyReadByCodeFn agar resolve pool
// tenant memakai mock (dipakai authorize/installAuthContext tanpa appTenant).
func stubDBByCode(t *testing.T, db *sql.DB, err error) {
	t.Helper()
	oldByCode := companyDBByCodeFn
	oldReadByCode := companyReadByCodeFn
	companyDBByCodeFn = func(string) (*sql.DB, error) { return db, err }
	companyReadByCodeFn = func(string) (*sql.DB, error) { return db, err }
	t.Cleanup(func() {
		companyDBByCodeFn = oldByCode
		companyReadByCodeFn = oldReadByCode
	})
}

// avFakeRedisCmd — implementasi in-memory tokenauth.RedisCmd (pola
// internal/tokenauth/tokenauth_test.go) agar refresh/revocation dites tanpa Redis.

type avFakeRedisCmd struct {
	mu    sync.Mutex
	store map[string]avFakeVal
}

type avFakeVal struct {
	val      string
	expireAt time.Time
	hasTTL   bool
}

func newAVFakeRedisCmd() *avFakeRedisCmd {
	return &avFakeRedisCmd{store: map[string]avFakeVal{}}
}

func (f *avFakeRedisCmd) get(key string) (avFakeVal, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.store[key]
	if !ok {
		return avFakeVal{}, false
	}
	if v.hasTTL && time.Now().After(v.expireAt) {
		delete(f.store, key)
		return avFakeVal{}, false
	}
	return v, true
}

func (f *avFakeRedisCmd) Set(_ context.Context, key string, value interface{}, expiration time.Duration) *redis.StatusCmd {
	cmd := redis.NewStatusCmd(context.Background())
	f.mu.Lock()
	defer f.mu.Unlock()
	v := avFakeVal{val: avToString(value)}
	if expiration > 0 {
		v.hasTTL = true
		v.expireAt = time.Now().Add(expiration)
	}
	f.store[key] = v
	cmd.SetVal("OK")
	return cmd
}

func (f *avFakeRedisCmd) Get(_ context.Context, key string) *redis.StringCmd {
	cmd := redis.NewStringCmd(context.Background())
	if v, ok := f.get(key); ok {
		cmd.SetVal(v.val)
	} else {
		cmd.SetErr(redis.Nil)
	}
	return cmd
}

func (f *avFakeRedisCmd) Del(_ context.Context, keys ...string) *redis.IntCmd {
	cmd := redis.NewIntCmd(context.Background())
	f.mu.Lock()
	defer f.mu.Unlock()
	n := int64(0)
	for _, k := range keys {
		if _, ok := f.store[k]; ok {
			delete(f.store, k)
			n++
		}
	}
	cmd.SetVal(n)
	return cmd
}

func (f *avFakeRedisCmd) Exists(_ context.Context, keys ...string) *redis.IntCmd {
	cmd := redis.NewIntCmd(context.Background())
	n := int64(0)
	for _, k := range keys {
		if _, ok := f.get(k); ok {
			n++
		}
	}
	cmd.SetVal(n)
	return cmd
}

func avToString(value interface{}) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}

// ---------------------------------------------------------------------------
// Row builders untuk scan handlers (vehicle, route, assignment, fuel).
// ---------------------------------------------------------------------------

// masterUserCols/Row — 24 kolom SELECT loadMasterUserByEmail (identik pola
// service-websocket b4_coverage4_test.go).
var avMasterUserCols = []string{
	"id", "company_id", "company_code", "email", "password_hash", "full_name",
	"username", "first_name", "last_name", "phone_number", "email_verified",
	"phone_verified", "mfa_enabled", "locale", "avatar_url", "failed_login_attempts",
	"password_changed_at", "locked_until", "deleted_at", "last_login",
	"created_by", "updated_by", "role", "status",
}

func avMasterUserRow(hash, role, status, companyCode string, lockedUntil interface{}) *sqlmock.Rows {
	return sqlmock.NewRows(avMasterUserCols).
		AddRow(
			uint64(7), int64(1), companyCode, "u@dev001.io", hash, "Full Name",
			"", "", "", "", false,
			false, false, "id", "", 0,
			nil, lockedUntil, nil, nil,
			nil, nil, role, status,
		)
}

// vehicleRowCols mendeskripsikan kolom vehicles list/detail (vehicleCols const).
var vehicleRowCols = []string{
	"id", "imei", "plate_number", "make", "model", "fuel_type",
	"vehicle_type_code", "driver_user_id", "device_model", "status", "created_at",
}

func vehicleRowVals(id uint64, imei, plate, make, model, fuel, vtype string, driverID interface{}) []driver.Value {
	return []driver.Value{
		id, imei, plate, make, model, fuel, vtype, driverID, "GT06", "active", time.Now(),
	}
}

func vehicleRow(id uint64, imei, plate, make, model, fuel, vtype string, driverID interface{}) *sqlmock.Rows {
	return sqlmock.NewRows(vehicleRowCols).
		AddRow(vehicleRowVals(id, imei, plate, make, model, fuel, vtype, driverID)...)
}

// avRouteCols/avAssignCols — kolom routes (routeCols const) + assignments (loadAssignments).
var avRouteCols = []string{"id", "name", "waypoints", "estimated_duration_sec", "created_by", "is_active", "created_at"}
var avAssignCols = []string{"id", "route_id", "vehicle_id", "driver_user_id", "status", "started_at", "completed_at", "deviation_meters", "created_at"}

func avRouteRow(id uint64, name string, wb []byte, est interface{}) []driver.Value {
	return []driver.Value{id, name, wb, est, uint64(7), true, time.Now()}
}

func avAssignRow(id, routeID, vehicleID, driverID uint64, status string) []driver.Value {
	return []driver.Value{id, routeID, vehicleID, driverID, status, nil, nil, nil, time.Now()}
}

// avFuelConfigCols — kolom fuel_configs list (fuelCols const).
var avFuelConfigCols = []string{"id", "vehicle_id", "drop_threshold", "refuel_threshold", "window_seconds", "enabled", "created_at"}

func avFuelConfigRow(id uint64, vid interface{}, drop, refuel float64, window int, enabled bool) []driver.Value {
	return []driver.Value{id, vid, drop, refuel, window, enabled, time.Now()}
}

// installFakeTokenManager memasang tokenMgr berbasis avFakeRedisCmd supaya
// issueTokenPair/refresh/logout/requireAuth revocation hidup tanpa Redis nyata.
func installFakeTokenManager(t *testing.T) *avFakeRedisCmd {
	t.Helper()
	fr := newAVFakeRedisCmd()
	old := tokenMgr
	oldCfg := appCfg
	appCfg = newTestCfg()
	appCfg.JWT.RefreshExpiry = 168 * time.Hour
	appCfg.JWT.RevocationEnabled = true
	tokenMgr = tokenauth.New(fr, "adatrack_gps:")
	t.Cleanup(func() {
		tokenMgr = old
		appCfg = oldCfg
	})
	return fr
}
