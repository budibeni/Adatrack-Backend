package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"

	"adatrack_gps/internal"
	"adatrack_gps/internal/tenant"
)

// checkResult is one wiring assertion.
type checkResult struct {
	Name   string
	Detail string
	Err    error
}

// reportChecks logs every result and reports whether all of them passed.
func reportChecks(results []checkResult) bool {
	ok := true
	for _, r := range results {
		status := "ok"
		if r.Err != nil {
			status = "FAILED"
			ok = false
		}
		slog.Info("wiring check", "check", r.Name, "status", status, "detail", r.Detail, "error", r.Err)
	}
	return ok
}

// runChecks performs the round trips that prove the foundation is wired
// (B0 acceptance: publish/consume NATS OK, PG + Redis reachable, tenant resolvable).
func runChecks(ctx context.Context, cfg *internal.Config, red *internal.RedisClient,
	nac *internal.NATSClient, tm *tenant.Manager) []checkResult {

	var results []checkResult
	add := func(name, detail string, err error) {
		results = append(results, checkResult{Name: name, Detail: detail, Err: err})
	}

	// 1. PostgreSQL master query.
	ctxDB, cancelDB := context.WithTimeout(ctx, 5*time.Second)
	defer cancelDB()
	var version string
	err := tm.Master().DB.QueryRowContext(ctxDB, "SELECT version()").Scan(&version)
	add("postgres.master_query", truncate(version, 60), err)

	// 2. Redis write/read/delete round trip.
	ctxRedis, cancelRedis := context.WithTimeout(ctx, 5*time.Second)
	defer cancelRedis()
	key := red.KeyPrefix() + "default:foundation:check"
	werr := red.Set(ctxRedis, key, "ok", 30*time.Second)
	var got string
	if werr == nil {
		got, werr = red.Get(ctxRedis, key)
	}
	if werr == nil && got != "ok" {
		werr = fmt.Errorf("round trip mismatch: got %q", got)
	}
	add("redis.set_get", key, werr)
	_ = red.Del(ctxRedis, key)

	// 3. NATS publish → consume round trip on the raw telemetry subject family.
	ctxNATS, cancelNATS := context.WithTimeout(ctx, 8*time.Second)
	defer cancelNATS()
	subject := cfg.Subject("raw", "foundation-check")
	add("nats.publish_consume", subject, natsRoundTrip(ctxNATS, nac, subject))

	// 4. Tenant registry + IMEI resolution (anti-spoofing path, FR-1.4).
	ctxTenant, cancelTenant := context.WithTimeout(ctx, 5*time.Second)
	defer cancelTenant()
	if _, ierr := tm.ResolveDeviceByIMEI(ctxTenant, "864201040512345"); ierr != nil {
		add("tenant.imei_resolve", "864201040512345 (dev fixture)", ierr)
	} else {
		add("tenant.imei_resolve", "864201040512345 → DEV001", nil)
	}

	return results
}

// natsRoundTrip subscribes (queue group), publishes and waits for the echoed
// message, proving both directions of the NATS wiring.
func natsRoundTrip(ctx context.Context, nac *internal.NATSClient, subject string) error {
	received := make(chan *nats.Msg, 1)

	sub, err := nac.Subscribe(subject, "foundation-check", func(msg *nats.Msg) error {
		select {
		case received <- msg:
		default:
		}
		return nil
	})
	if err != nil {
		return err
	}
	defer nac.Unsubscribe(sub)

	payload, err := json.Marshal(map[string]any{"check": "foundation", "timestamp": time.Now().Unix()})
	if err != nil {
		return err
	}
	if err := nac.Publish(subject, payload); err != nil {
		return err
	}
	if err := nac.Flush(3 * time.Second); err != nil {
		return err
	}

	select {
	case <-received:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("no message received on %s within timeout", subject)
	}
}

// healthChecks wires the readiness dependencies (PRD §10.2).
func healthChecks(cfg *internal.Config, pool *internal.DBPool, red *internal.RedisClient,
	nac *internal.NATSClient, tm *tenant.Manager) []internal.HealthCheck {

	return []internal.HealthCheck{
		{Name: "postgres_master", Critical: true, Fn: func(ctx context.Context) error { return pool.Ping(ctx) }},
		{Name: "redis", Critical: true, Fn: func(ctx context.Context) error { return red.Ping(ctx) }},
		{Name: "nats", Critical: true, Fn: func(_ context.Context) error {
			if !nac.IsConnected() {
				return fmt.Errorf("not connected")
			}
			return nil
		}},
		{Name: "tenant_pools", Critical: false, Fn: func(ctx context.Context) error { return tm.Health(ctx) }},
		{Name: "migration_ledger", Critical: true, Fn: func(ctx context.Context) error {
			applied, failures, err := internal.LedgerStatus(ctx, pool.DB, cfg.Migrate.MasterSchema, cfg.Migrate.LedgerTable)
			if err != nil {
				return err
			}
			if failures > 0 {
				return fmt.Errorf("%d failed migrations", failures)
			}
			if applied == 0 {
				return fmt.Errorf("ledger empty (run scripts/migrate.sh or set MIGRATE_ON_BOOT=true)")
			}
			return nil
		}},
	}
}

// truncate shortens a detail string for logs.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
