package main

import (
	"context"
	"fmt"
	"net/http"
)

// checkRowLevelDriver asserts the tm_user_vehicles filter (PRD §9.2): the driver
// sees a strictly smaller set than the tenant admin.
func checkRowLevelDriver(ctx context.Context, api *httpClient, admin, driver *session) checkResult {
	const name = "rbac.row_level_filter"

	adminResp := api.get(ctx, "/api/v1/vehicles?limit=1000", admin.AccessToken)
	if adminResp.RequestErr != nil || adminResp.Status != http.StatusOK {
		return fail(name, "admin list failed: "+statusDetail(adminResp), adminResp.RequestErr)
	}
	driverResp := api.get(ctx, "/api/v1/vehicles?limit=1000", driver.AccessToken)
	if driverResp.RequestErr != nil || driverResp.Status != http.StatusOK {
		return fail(name, "driver list failed: "+statusDetail(driverResp), driverResp.RequestErr)
	}

	adminIDs := collectVehicleIDs(adminResp.dataList())
	driverIDs := collectVehicleIDs(driverResp.dataList())
	if len(adminIDs) == 0 {
		return fail(name, "admin sees no vehicles (fixtures missing?)", nil)
	}
	if len(driverIDs) >= len(adminIDs) {
		return fail(name, fmt.Sprintf("driver sees %d of %d admin vehicles (expected fewer)",
			len(driverIDs), len(adminIDs)), nil)
	}
	for id := range driverIDs {
		if !adminIDs[id] {
			return fail(name, fmt.Sprintf("driver vehicle %d is outside the tenant view", id), nil)
		}
	}

	var unassigned int64
	for id := range adminIDs {
		if !driverIDs[id] {
			unassigned = id
			break
		}
	}
	if unassigned == 0 {
		return fail(name, "no unassigned vehicle found to probe", nil)
	}
	probe := api.get(ctx, fmt.Sprintf("/api/v1/vehicles/%d", unassigned), driver.AccessToken)
	if probe.Status != http.StatusForbidden {
		return fail(name, fmt.Sprintf("unassigned vehicle %d: %s", unassigned, statusDetail(probe)), nil)
	}
	if code := probe.errorCode(); code != "UNAUTHORIZED_VEHICLE" {
		return fail(name, "error_code="+code+" (want UNAUTHORIZED_VEHICLE)", nil)
	}
	return pass(name, fmt.Sprintf("admin=%d driver=%d; unassigned %d → 403 UNAUTHORIZED_VEHICLE",
		len(adminIDs), len(driverIDs), unassigned))
}

// checkPaginationContract asserts the PRD §8.1 envelope + §8.2 pagination.
func checkPaginationContract(ctx context.Context, api *httpClient, admin *session) checkResult {
	const name = "rest.pagination"
	resp := api.get(ctx, "/api/v1/vehicles?page=1&limit=1", admin.AccessToken)
	if resp.RequestErr != nil {
		return fail(name, "request failed", resp.RequestErr)
	}
	if resp.Status != http.StatusOK {
		return fail(name, statusDetail(resp), nil)
	}
	if status, _ := resp.Body["status"].(string); status != "success" {
		return fail(name, "status field="+status, nil)
	}
	block := resp.pagination()
	if block == nil {
		return fail(name, "pagination block missing", nil)
	}
	if len(resp.dataList()) > 1 {
		return fail(name, fmt.Sprintf("limit=1 returned %d rows", len(resp.dataList())), nil)
	}
	total, _ := block["total"].(float64)
	return pass(name, fmt.Sprintf("page=1 limit=1 total=%.0f", total))
}

// checkValidationErrors asserts PRD §8.5 bounds (limit cap + enum + path param).
func checkValidationErrors(ctx context.Context, api *httpClient, admin *session) checkResult {
	const name = "rest.validation"
	resp := api.get(ctx, "/api/v1/vehicles?limit=99999", admin.AccessToken)
	if resp.RequestErr != nil {
		return fail(name, "request failed", resp.RequestErr)
	}
	if resp.Status != http.StatusBadRequest || resp.errorCode() != "VALIDATION_ERROR" {
		return fail(name, "limit cap not enforced: "+statusDetail(resp), nil)
	}
	status := api.get(ctx, "/api/v1/vehicles?status=bogus", admin.AccessToken)
	if status.Status != http.StatusBadRequest {
		return fail(name, "status whitelist not enforced: "+statusDetail(status), nil)
	}
	bad := api.get(ctx, "/api/v1/vehicles/not-a-number", admin.AccessToken)
	if bad.Status != http.StatusBadRequest {
		return fail(name, "path param not validated: "+statusDetail(bad), nil)
	}
	return pass(name, "400 VALIDATION_ERROR on limit/status/path param")
}

// checkVehicleNotFound asserts 404 for an unknown id.
func checkVehicleNotFound(ctx context.Context, api *httpClient, admin *session) checkResult {
	const name = "rest.vehicle_not_found"
	resp := api.get(ctx, "/api/v1/vehicles/999999", admin.AccessToken)
	if resp.RequestErr != nil {
		return fail(name, "request failed", resp.RequestErr)
	}
	if resp.Status != http.StatusNotFound || resp.errorCode() != "VEHICLE_NOT_FOUND" {
		return fail(name, statusDetail(resp), nil)
	}
	return pass(name, "404 VEHICLE_NOT_FOUND")
}

// resolveVehicleID finds the vehicle id of the configured IMEI.
func resolveVehicleID(ctx context.Context, api *httpClient, admin *session, opt options) (int64, checkResult) {
	const name = "rest.vehicle_lookup"
	resp := api.get(ctx, "/api/v1/vehicles?limit=1000", admin.AccessToken)
	if resp.RequestErr != nil {
		return 0, fail(name, "request failed", resp.RequestErr)
	}
	if resp.Status != http.StatusOK {
		return 0, fail(name, statusDetail(resp), nil)
	}
	for _, raw := range resp.dataList() {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if imei, _ := item["imei"].(string); imei == opt.imei {
			id, _ := item["id"].(float64)
			return int64(id), pass(name, fmt.Sprintf("imei=%s id=%.0f", opt.imei, id))
		}
	}
	return 0, fail(name, "vehicle for imei "+opt.imei+" not found in the tenant list", nil)
}

// collectVehicleIDs extracts the ids of a vehicle list payload.
func collectVehicleIDs(list []any) map[int64]bool {
	ids := make(map[int64]bool, len(list))
	for _, raw := range list {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if id, ok := item["id"].(float64); ok {
			ids[int64(id)] = true
		}
	}
	return ids
}
