// Command e2ews is the end-to-end verification harness for phase B2
// (service-websocket): auth → RBAC → REST → WebSocket push → audit.
//
// It drives the REAL service through its public interfaces only:
//
//	device frame → ingestion-tcp → NATS → worker-live → service-websocket → WS client
//
// and asserts the B2 acceptance criteria:
//   - 401/403 correctness (no token, cross-scope, unassigned vehicle)
//   - WS push end-to-end < 1 s from ingest; reconnect + resubscribe safe
//   - REST contract (pagination block, error_code, validation)
//   - audit rows land in master.tm_audit_logs
//
// Usage: scripts/e2e-websocket.sh [flags]
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"
)

func main() {
	opt := parseFlags()

	// The §16 load profile publishes wsMessages frames at wsRate msg/s, so the
	// run needs minutes rather than the seconds a functional pass needs.
	budget := opt.timeout * 12
	if opt.wsLoad && opt.wsRate > 0 {
		budget += time.Duration(opt.wsMessages/opt.wsRate+60) * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()

	results := runChecks(ctx, opt)
	report(results)
}

// report prints every assertion and exits non-zero when one failed.
func report(results []checkResult) {
	failed := 0
	fmt.Println()
	for _, r := range results {
		status := "PASS"
		if r.Err != nil {
			status = "FAIL"
			failed++
		}
		fmt.Printf("[%s] %-28s %s", status, r.Name, r.Detail)
		if r.Err != nil {
			fmt.Printf("  error=%v", r.Err)
		}
		fmt.Println()
	}
	fmt.Printf("\nB2 E2E summary: %d/%d checks passed\n", len(results)-failed, len(results))
	if failed > 0 {
		fmt.Fprintln(os.Stderr, "B2 E2E FAILED")
		os.Exit(1)
	}
	fmt.Println("B2 E2E PASSED")
}

// checkResult is one assertion.
type checkResult struct {
	Name   string
	Detail string
	Err    error
}

// pass builds a successful assertion.
func pass(name, detail string) checkResult {
	return checkResult{Name: name, Detail: detail}
}

// fail builds a failed assertion. A nil error is replaced by an error built from
// the detail string so a failing check can NEVER be mistaken for a pass (a
// verification harness that reports PASS on failure is worse than no harness).
func fail(name, detail string, err error) checkResult {
	if err == nil {
		err = errors.New(detail)
	}
	return checkResult{Name: name, Detail: detail, Err: err}
}

// runChecks executes the whole B2 flow, sharing one authenticated session set.
func runChecks(ctx context.Context, opt options) []checkResult {
	var results []checkResult
	add := func(r checkResult) { results = append(results, r) }

	api := newHTTPClient(opt)

	// Focused profile run (`--ws-only`, used by scripts/b4-verify.sh): one login,
	// resolve the vehicle, run the fan-out profile — nothing else, so the B4 gate
	// measures the profile instead of re-running the whole B2 functional flow.
	if opt.wsOnly {
		admin, r := checkLogin(ctx, api, opt.adminEmail, opt.adminPassword)
		add(r)
		if admin == nil {
			add(fail("ws.load", "tenant admin session required for the load profile", nil))
			return results
		}
		vehicleID, r := resolveVehicleID(ctx, api, admin, opt)
		add(r)
		if vehicleID <= 0 {
			add(fail("ws.load", "vehicle id unresolved — load profile not verified", nil))
			return results
		}
		add(runWSLoad(ctx, opt, admin, vehicleID))
		return results
	}

	// --- 1. readiness ------------------------------------------------------
	add(checkHealthz(ctx, api))

	// --- 2. authentication -------------------------------------------------
	admin, r := checkLogin(ctx, api, opt.adminEmail, opt.adminPassword)
	add(r)
	if admin == nil {
		// Without the tenant admin session the rest of the flow cannot run: report
		// an explicit failure so the harness never exits 0 on a partial run.
		add(fail("auth.session_required",
			"tenant admin session is required for the remaining checks (see auth.login failure)", nil))
		return results
	}

	add(checkLoginBadPassword(ctx, api, opt.adminEmail))
	add(checkNoToken(ctx, api))
	add(checkRefreshRotation(ctx, api, admin))

	platform, r := checkLogin(ctx, api, opt.platformEmail, opt.platformPassword)
	add(r)
	driver, r := checkLogin(ctx, api, opt.driverEmail, opt.driverPassword)
	add(r)

	// --- 3. RBAC -----------------------------------------------------------
	if driver != nil {
		add(checkRowLevelDriver(ctx, api, admin, driver))
		add(checkWSUnauthorizedVehicle(ctx, opt, driver))
	} else {
		add(fail("rbac.row_level_filter", "driver session unavailable — row-level RBAC not verified", nil))
		add(fail("ws.unauthorized_vehicle", "driver session unavailable — WS RBAC not verified", nil))
	}
	if platform != nil {
		add(checkPlatformScope(ctx, api, platform))
	} else {
		add(fail("rbac.platform_scope", "platform session unavailable — scope guard not verified", nil))
	}
	add(checkPlatformOnly(ctx, api, admin))

	// --- 4. REST contract --------------------------------------------------
	add(checkPaginationContract(ctx, api, admin))
	add(checkValidationErrors(ctx, api, admin))
	add(checkVehicleNotFound(ctx, api, admin))

	// --- 5. live path: device frame → live state → REST + WS ---------------
	vehicleID, r := resolveVehicleID(ctx, api, admin, opt)
	add(r)
	if vehicleID > 0 {
		add(checkLiveStatusAfterIngest(ctx, opt, vehicleID))
		add(checkWSPushUnderOneSecond(ctx, opt, admin, vehicleID))
		add(checkWSReconnectResubscribe(ctx, opt, admin, vehicleID))
		add(checkHistoryPagination(ctx, api, admin, vehicleID))
		if opt.wsLoad {
			// Opt-in (§16): 50 subscribers × 1200 frames fan-out profile.
			add(runWSLoad(ctx, opt, admin, vehicleID))
		}
	} else {
		add(fail("rest.live_enrichment", "vehicle id unresolved — live path not verified", nil))
		add(fail("ws.push_under_1s", "vehicle id unresolved — WS push not verified", nil))
		add(fail("ws.reconnect_resubscribe", "vehicle id unresolved — reconnect not verified", nil))
		add(fail("rest.history_pagination", "vehicle id unresolved — history not verified", nil))
		if opt.wsLoad {
			add(fail("ws.load", "vehicle id unresolved — WS load profile not verified", nil))
		}
	}

	// --- 6. revocation + audit --------------------------------------------
	add(checkLogoutAndRevocation(ctx, api, admin))
	add(checkAuditRows(ctx, opt))

	return results
}
