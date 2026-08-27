package controllers

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"ajb_gps/internal"
	"ajb_gps/internal/tenant"
	"ajb_gps/service-websocket/models"

	"github.com/gin-gonic/gin"
	nats "github.com/nats-io/nats.go"
)

// Package-level app state injected once by main via Setup.
var (
	appCfg       *internal.Config
	appRedis     *internal.RedisClient
	appNATS      *internal.NATSClient
	appTenant    *tenant.Manager // master + per-company DB pools (PRD §6)
	appHub       *hub
	vehReg       *vehicleRegistry
	metricsHTTP  http.Handler
	appSub       *nats.Subscription
	appNotifySub *nats.Subscription
	appMediaSub  *nats.Subscription
)

// Setup wires package globals and starts the NATS→hub bridge, including the
// B3 notifikasi consumer (subject notify.alert.<vehicle_id>, queue "websocket").
// Returns an unsubscribe func for graceful shutdown.
func Setup(cfg *internal.Config, redis *internal.RedisClient, natsClient *internal.NATSClient,
	metrics http.Handler, tm *tenant.Manager) (func(), error) {
	appCfg = cfg
	appRedis = redis
	appNATS = natsClient
	metricsHTTP = metrics
	appTenant = tm

	if appTenant == nil {
		return nil, fmt.Errorf("tenant manager is required for service-websocket")
	}
	slog.Info("tenant manager ready", "companies", len(appTenant.Companies()))

	vehReg = newVehicleRegistry()
	appHub = newHub(cfg.WebSocket.MaxConnections, cfg.WebSocket.MaxQueue)

	// Live telemetry bridge (FR-5.2) — queue group "websocket".
	rawSub, err := appNATS.Subscribe(appNATS.Subject("raw", ">"), "websocket", bridgeHandle)
	if err != nil {
		return nil, err
	}
	appSub = rawSub

	// Alert notification bridge (B3 notifikasi): worker-alert publishes to
	// notify.alert.<vehicleID>; we fan out to authorised WS clients via the hub.
	notifySub, err := appNATS.Subscribe(appNATS.SubjectPlain("notify", "alert", "*"), "websocket", notifyHandle)
	if err != nil {
		appNATS.Unsubscribe(appSub)
		appSub = nil
		return nil, err
	}
	appNotifySub = notifySub

	// Media event bridge (B5b, Module 8 FR-8.5): service-media publishes to
	// media.event.<company>; we fan out as MEDIA_EVENT with RBAC/tenant filter.
	mediaSub, err := appNATS.Subscribe(appNATS.SubjectPlain("media", "event", ">"), "websocket", mediaHandle)
	if err != nil {
		appNATS.Unsubscribe(appSub)
		appSub = nil
		appNATS.Unsubscribe(appNotifySub)
		appNotifySub = nil
		return nil, err
	}
	appMediaSub = mediaSub

	unsubscribe := func() {
		if appSub != nil {
			appNATS.Unsubscribe(appSub)
			appSub = nil
		}
		if appNotifySub != nil {
			appNATS.Unsubscribe(appNotifySub)
			appNotifySub = nil
		}
		if appMediaSub != nil {
			appNATS.Unsubscribe(appMediaSub)
			appMediaSub = nil
		}
	}
	return unsubscribe, nil
}

// Shutdown closes WebSocket connections + tenant pools (graceful drain).
func Shutdown() {
	if appHub != nil {
		appHub.closeAll()
	}
	if appTenant != nil {
		appTenant.Close()
	}
}

// companyDB resolves the pre-warmed company pool of the caller (from gin ctx).
// It returns the pool stored by requireAuth / websocketHandler.
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
// split): replica ketika tersedia & sehat, fallback primary. HANYA dipakai
// handler yang murni membaca (GET); endpoint tulis wajib companyDB().
func companyRead(c *gin.Context) (*sql.DB, error) {
	if v, ok := c.Get(ctxCompanyROKey); ok {
		if db, ok2 := v.(*sql.DB); ok2 && db != nil {
			return db, nil
		}
	}
	return companyDB(c)
}

// companyDBByCode resolves a company pool from the tenant manager.
func companyDBByCode(companyCode string) (*sql.DB, error) {
	if appTenant == nil {
		return nil, fmt.Errorf("tenant manager not initialized")
	}
	return appTenant.DB(companyCode)
}

// companyReadByCode resolves a READ-preferred company pool dari tenant
// manager (B4 HA read/write split): replica ketika tersedia & sehat,
// fallback primary. Dipakai loader baca non-handler (mis. vehicle registry);
// jalur auth tetap companyDBByCode (primary demi konsistensi login).
func companyReadByCode(companyCode string) (*sql.DB, error) {
	if appTenant == nil {
		return nil, fmt.Errorf("tenant manager not initialized")
	}
	if ro, err := appTenant.ReadPool(companyCode); err == nil {
		return ro, nil
	}
	return appTenant.DB(companyCode)
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

// redisVehicleStateKey builds the Redis live-state key EXACTLY as worker-live
// writes it (PRD §7 / Key Decision 7): adatrack_gps:{company_code}:vehicle:state:<IMEI>.
func redisVehicleStateKey(companyCode, imei string) string {
	prefix := os.Getenv("REDIS_KEY_PREFIX")
	if strings.TrimSpace(prefix) == "" {
		prefix = "adatrack_gps:"
	}
	code := strings.ToLower(strings.TrimSpace(companyCode))
	if code == "" {
		code = "default"
	}
	return prefix + code + ":vehicle:state:" + imei
}

// writeSuccess writes the standard success envelope { status, data, pagination? }.
func writeSuccess(c *gin.Context, status int, data interface{}, page ...*models.PaginationInfo) {
	resp := models.OkResponse{Status: "success", Data: data}
	if len(page) == 1 {
		resp.Pagination = page[0]
	}
	c.JSON(status, resp)
}

// writeSuccessWithTotal writes a success envelope with a top-level total_records
// (GAP #1 — history: data = array of points, total_records sibling).
func writeSuccessWithTotal(c *gin.Context, status int, data interface{}, total int64) {
	c.JSON(status, models.OkResponse{Status: "success", Data: data, TotalRecords: total})
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

// healthHandler is the readiness probe (PRD §8.2): master + company pools,
// Redis + NATS.
func healthHandler(w http.ResponseWriter, _ *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ok := true
	checks := []string{}

	if err := appTenant.Health(ctx); err != nil {
		ok = false
		checks = append(checks, "mysql:"+err.Error())
	} else {
		checks = append(checks, "mysql:ok")
	}

	if appRedis != nil && appRedis.Client().Ping(ctx).Err() == nil {
		checks = append(checks, "redis:ok")
	} else {
		ok = false
		checks = append(checks, "redis:down")
	}

	if appNATS != nil && appNATS.IsConnected() {
		checks = append(checks, "nats:ok")
	} else {
		ok = false
		checks = append(checks, "nats:down")
	}

	hstatus := http.StatusOK
	body := "ok " + joinStrings(checks)
	if !ok {
		hstatus = http.StatusServiceUnavailable
		body = "degraded " + joinStrings(checks)
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(hstatus)
	_, _ = w.Write([]byte(body))
}

// joinStrings is a tiny local helper.
func joinStrings(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ","
		}
		out += p
	}
	return out
}

// Router returns the configured http.Handler for service-websocket (used by main).
func Router() http.Handler {
	return setupRouter()
}
