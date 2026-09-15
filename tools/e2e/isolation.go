package main

import (
	"context"
	"fmt"
	"time"
)

// checkTenantIsolation asserts there is NO cross-tenant leakage: rows for the
// device IMEI must exist only in its own tenant schema (B1 acceptance
// "isolasi antar tenant schema terverifikasi (0 leakage)").
func checkTenantIsolation(ctx context.Context, opt options, imei string, since time.Time) checkResult {
	r := checkResult{Name: "tenant.isolation"}

	// The platform tenant (adatrack_gps_default) must never receive this device's
	// telemetry, even though it lives in the same physical database.
	platform := pgConfig{
		host: opt.pg.host, port: opt.pg.port, user: opt.pg.user,
		password: opt.pg.password, db: opt.pg.db,
	}
	db, err := openPG(platform, "adatrack_gps_default")
	if err != nil {
		r.Err = fmt.Errorf("open platform schema: %w", err)
		return r
	}
	defer func() { _ = db.Close() }()

	leaked, err := countTelemetrySince(ctx, db, imei, since)
	if err != nil {
		r.Err = err
		return r
	}
	if leaked != 0 {
		r.Err = fmt.Errorf("%d row(s) leaked into the platform schema (want 0)", leaked)
		return r
	}
	r.Detail = fmt.Sprintf("0 rows in adatrack_gps_default for %s (tenant-scoped)", imei)
	return r
}
