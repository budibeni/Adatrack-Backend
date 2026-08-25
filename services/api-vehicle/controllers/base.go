package controllers

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"time"

	"ajb_gps/api-vehicle/models"
	"ajb_gps/internal"
	"ajb_gps/internal/tenant"

	"github.com/gin-gonic/gin"
)

// Package-level app state injected once by main via Init.
var (
	appCfg    *internal.Config
	appRedis  *internal.RedisClient
	appTenant *tenant.Manager // master + per-company DB pools (PRD §6)
	metricsHTTP http.Handler
)

// Init wires the package globals used by handlers (called once from main).
func Init(cfg *internal.Config, redis *internal.RedisClient, tm *tenant.Manager, metrics http.Handler) {
	appCfg = cfg
	appRedis = redis
	appTenant = tm
	metricsHTTP = metrics
}

// Shutdown closes the tenant pools (graceful shutdown).
func Shutdown() {
	if appTenant != nil {
		appTenant.Close()
	}
}

// companyDB resolves the pre-warmed company pool of the caller
// (installed by requireAuth from the JWT company_code claim).
func companyDB(c *gin.Context) (*sql.DB, error) {
	v, ok := c.Get(ctxCompanyDBKey)
	if !ok {
		return nil, fmt.Errorf("no company database context")
	}
	db, ok := v.(*sql.DB)
	if !ok || db == nil {
		return nil, fmt.Errorf("invalid company database context")
	}
	return db, nil
}

// companyRead resolves the READ-preferred pool untuk caller (B4 HA read/write
// split): replica ketika tersedia & sehat, fallback primary. HANYA untuk
// handler murni membaca (GET); endpoint tulis wajib companyDB().
func companyRead(c *gin.Context) (*sql.DB, error) {
	if v, ok := c.Get(ctxCompanyROKey); ok {
		if db, ok2 := v.(*sql.DB); ok2 && db != nil {
			return db, nil
		}
	}
	return companyDB(c)
}

// masterDB returns the master pool (global auth authority).
func masterDB() *sql.DB {
	return appTenant.Master()
}

// auditDB returns the master pool for audit writes, or nil bila tenant manager
// belum siap (mis. unit test tanpa infra) — LogAudit aman utk nil db.
func auditDB() *sql.DB {
	if appTenant == nil {
		return nil
	}
	return appTenant.Master()
}

// writeSuccess writes the standard success envelope { status, data, pagination? }.
func writeSuccess(c *gin.Context, status int, data interface{}, page ...*models.PaginationInfo) {
	resp := models.OkResponse{Status: "success", Data: data}
	if len(page) == 1 {
		resp.Pagination = page[0]
	}
	c.JSON(status, resp)
}

// writeError writes the GAP #3 error envelope.
func writeError(c *gin.Context, status int, code, msg string) {
	c.JSON(status, models.ApiErrorResponse{
		Status:    "error",
		ErrorCode: code,
		Message:   msg,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
	c.Abort()
}

// healthzHandler is the readiness probe (PRD §8.2): tenant pools + Redis.
func healthzHandler(c *gin.Context) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ok := true
	var checks []string

	if err := appTenant.Health(ctx); err != nil {
		ok = false
		checks = append(checks, "mysql:"+err.Error())
	} else {
		checks = append(checks, "mysql:ok")
	}
	if appRedis != nil && appRedis.Ping(ctx) == nil {
		checks = append(checks, "redis:ok")
	} else {
		ok = false
		checks = append(checks, "redis:down")
	}

	body := "ok " + joinChecks(checks)
	code := http.StatusOK
	if !ok {
		body = "degraded " + joinChecks(checks)
		code = http.StatusServiceUnavailable
	}
	c.String(code, body)
}

// joinChecks is a tiny local helper for the health line.
func joinChecks(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ","
		}
		out += p
	}
	return out
}

// ---------------------------------------------------------------------------
// Small shared helpers used by all handlers.
// ---------------------------------------------------------------------------

// placeholders builds "?,?,?" for n args.
func placeholders(n int) string {
	s := ""
	for i := 0; i < n; i++ {
		if i > 0 {
			s += ","
		}
		s += "?"
	}
	return s
}

// mapKeys converts an allowed-vehicle set into args order-stable enough for SQL.
func mapKeys(m map[uint64]struct{}) []interface{} {
	ids := make([]uint64, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sortUint64(ids)
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return args
}

func sortUint64(ids []uint64) {
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j] < ids[j-1]; j-- {
			ids[j], ids[j-1] = ids[j-1], ids[j]
		}
	}
}

// nullableStr maps "" → NULL for optional string columns.
func nullableStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// nullableStrPtr maps nil pointer → NULL.
func nullableStrPtr(v *string) interface{} {
	if v == nil {
		return nil
	}
	return *v
}

// nullableUint maps nil/0 pointer → NULL.
func nullableUint(v *uint64) interface{} {
	if v == nil || *v == 0 {
		return nil
	}
	return *v
}

// nullableTimeP converts sql.NullTime into a *time.Time DTO field.
func nullableTimeP(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	tt := t.Time
	return &tt
}

// nullableFloat converts sql.NullFloat64 into a *float64 DTO field.
func nullableFloat(f sql.NullFloat64) *float64 {
	if !f.Valid {
		return nil
	}
	v := f.Float64
	return &v
}

// nullableStrP converts sql.NullString into a *string DTO field.
func nullableStrP(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	v := s.String
	return &v
}

// nullableUint64 converts sql.NullInt64 into a *uint64 DTO field.
func nullableUint64(v sql.NullInt64) *uint64 {
	if !v.Valid {
		return nil
	}
	u := uint64(v.Int64)
	return &u
}

// paginationParams reads ?page=&limit= with sane bounds.
func paginationParams(c *gin.Context) (int, int) {
	page := atoiDefault(c.Query("page"), 1)
	limit := atoiDefault(c.Query("limit"), 100)
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 500 {
		limit = 100
	}
	return page, limit
}

func atoiDefault(s string, def int) int {
	n := 0
	if s == "" {
		return def
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return def
		}
		n = n*10 + int(s[i]-'0')
	}
	if n == 0 && s != "0" {
		return def
	}
	return n
}

