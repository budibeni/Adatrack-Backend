package main

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// checkLiveStatusAfterIngest drives a device frame through ingestion-tcp and
// asserts the REST live enrichment picks it up (FR-5.1/FR-5.2).
func checkLiveStatusAfterIngest(ctx context.Context, opt options, vehicleID int64) checkResult {
	const name = "rest.live_enrichment"
	api := newHTTPClient(opt)
	admin, resp := api.login(ctx, opt.adminEmail, opt.adminPassword)
	if admin == nil || resp.RequestErr != nil {
		return fail(name, "login failed: "+statusDetail(resp), resp.RequestErr)
	}

	// Distinct coordinates per run so a stale live state cannot satisfy the check.
	lat := -6.2088 + float64(time.Now().Unix()%100)/100000
	lon, speed := 106.8456, 42.5
	if err := drive(opt.tcpAddr, opt.imei, lat, lon, speed, true, opt.timeout); err != nil {
		return fail(name, "device frame failed", err)
	}

	deadline := time.Now().Add(opt.timeout)
	var lastSpeed float64
	for time.Now().Before(deadline) {
		detail := api.get(ctx, fmt.Sprintf("/api/v1/vehicles/%d", vehicleID), admin.AccessToken)
		if detail.Status == http.StatusOK {
			live, _ := detail.data()["live"].(map[string]any)
			if live != nil {
				if v, ok := live["speed"].(float64); ok {
					lastSpeed = v
				}
				if lastSpeed > 0 {
					return pass(name, fmt.Sprintf("live.speed=%.1f (device sent %.1f km/h)", lastSpeed, speed))
				}
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fail(name, fmt.Sprintf("live state not enriched within %s (last speed %.1f)", opt.timeout, lastSpeed), nil)
}

// checkHistoryPagination asserts the history endpoint contract (PRD §8.2).
func checkHistoryPagination(ctx context.Context, api *httpClient, admin *session, vehicleID int64) checkResult {
	const name = "rest.history_pagination"
	resp := api.get(ctx, fmt.Sprintf("/api/v1/vehicles/%d/history?page=1&limit=2", vehicleID), admin.AccessToken)
	if resp.RequestErr != nil {
		return fail(name, "request failed", resp.RequestErr)
	}
	if resp.Status != http.StatusOK {
		return fail(name, statusDetail(resp), nil)
	}
	block := resp.pagination()
	if block == nil {
		return fail(name, "pagination block missing", nil)
	}
	if len(resp.dataList()) > 2 {
		return fail(name, fmt.Sprintf("limit=2 returned %d rows", len(resp.dataList())), nil)
	}
	total, _ := block["total"].(float64)

	// An inverted range must be rejected (PRD §8.5 rule 4).
	bad := api.get(ctx, fmt.Sprintf(
		"/api/v1/vehicles/%d/history?from=2026-09-15T10:00:00Z&to=2026-09-14T10:00:00Z", vehicleID),
		admin.AccessToken)
	if bad.Status != http.StatusBadRequest {
		return fail(name, "inverted range accepted: "+statusDetail(bad), nil)
	}
	return pass(name, fmt.Sprintf("total=%.0f rows<=2; inverted range → 400", total))
}
