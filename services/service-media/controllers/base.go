package controllers

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"time"

	"ajb_gps/internal"
	"ajb_gps/internal/storage"
	"ajb_gps/internal/tenant"
	"ajb_gps/service-media/models"

	"github.com/gin-gonic/gin"
)

// Package-level app state injected once by main via Init.
var (
	appCfg      *internal.Config
	appRedis    *internal.RedisClient
	appTenant   *tenant.Manager // master + per-company DB pools (PRD §6)
	appStore    storage.Store   // S3-compatible object store (B5b, FR-8.2)
	appNATS     *internal.NATSClient
	metricsHTTP http.Handler
)

// Init wires the package globals used by handlers (called once from main).
func Init(cfg *internal.Config, redis *internal.RedisClient, tm *tenant.Manager,
	st storage.Store, nats *internal.NATSClient, metrics http.Handler) {
	appCfg = cfg
	appRedis = redis
	appTenant = tm
	appStore = st
	appNATS = nats
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

// companyRead resolves the READ-preferred pool utk caller (B4 HA read/write
// split): replica ketika tersedia & sehat, fallback primary. HANYA untuk
// handler murni membaca (GET).
func companyRead(c *gin.Context) (*sql.DB, error) {
	if v, ok := c.Get(ctxCompanyROKey); ok {
		if db, ok2 := v.(*sql.DB); ok2 && db != nil {
			return db, nil
		}
	}
	return companyDB(c)
}

// masterDB returns the master pool (global auth authority + company_media_config).
func masterDB() *sql.DB {
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

// healthzHandler is the readiness probe (FR-8.8): object store ping + tenant
// pools + Redis + NATS.
func healthzHandler(c *gin.Context) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ok := true
	var checks []string

	if appTenant != nil {
		if err := appTenant.Health(ctx); err != nil {
			ok = false
			checks = append(checks, "mysql:"+err.Error())
		} else {
			checks = append(checks, "mysql:ok")
		}
	} else {
		ok = false
		checks = append(checks, "mysql:uninit")
	}

	if appRedis != nil && appRedis.Ping(ctx) == nil {
		checks = append(checks, "redis:ok")
	} else {
		ok = false
		checks = append(checks, "redis:down")
	}

	if appStore != nil {
		if err := appStore.Ping(ctx); err != nil {
			ok = false
			checks = append(checks, "s3:"+err.Error())
		} else {
			checks = append(checks, "s3:ok")
		}
	} else {
		ok = false
		checks = append(checks, "s3:uninit")
	}

	if appNATS != nil && appNATS.IsConnected() {
		checks = append(checks, "nats:ok")
	} else {
		ok = false
		checks = append(checks, "nats:down")
	}

	body := "ok " + joinChecks(checks)
	code := http.StatusOK
	if !ok {
		body = "degraded " + joinChecks(checks)
		code = http.StatusServiceUnavailable
	}
	c.String(code, body)
}

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
