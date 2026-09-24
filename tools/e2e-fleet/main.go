// Command e2e-fleet is the end-to-end verification harness of phases B7.1/B7.2
// (fleet management core) and, through the REST API, B7.3/B7.4:
//
//	device frame (GT06 0x22 position) → ingestion-tcp → worker-live
//	  → Redis live state + odometer/engine-hour accumulators (FR-2.5)
//	  → trip/stop state machine → th_vehicle_trips + td_vehicle_stops (FR-2.6)
//	  → worker-persistence → th_telemetry_logs
//	  → service-websocket: /vehicles/{id}/playback (RDP + address) + /geocode/reverse
//
// It asserts the REAL counters of `tm_vehicles` before/after a known drive, so a
// broken accumulator cannot pass unnoticed, and it restores the fixture counters
// afterwards (an E2E must be repeatable).
//
// Usage: scripts/e2e-fleet.sh [flags]
package main

import (
	"context"
	"fmt"
	"os"
	"time"
)

func main() {
	opt := parseFlags()
	ctx, cancel := context.WithTimeout(context.Background(), opt.timeout*12)
	defer cancel()

	report(runChecks(ctx, opt))
}

// checkResult is one E2E assertion.
type checkResult struct {
	Name   string
	Detail string
	Err    error
}

func pass(name, detail string) checkResult { return checkResult{Name: name, Detail: detail} }

func fail(name, detail string, err error) checkResult {
	if err == nil {
		err = fmt.Errorf("%s", detail)
	}
	return checkResult{Name: name, Detail: detail, Err: err}
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
		fmt.Printf("[%s] %-34s %s", status, r.Name, r.Detail)
		if r.Err != nil {
			fmt.Printf("  error=%v", r.Err)
		}
		fmt.Println()
	}
	fmt.Printf("\nB7 fleet E2E summary: %d/%d checks passed\n", len(results)-failed, len(results))
	if failed > 0 {
		fmt.Fprintln(os.Stderr, "B7 fleet E2E FAILED")
		os.Exit(1)
	}
	fmt.Println("B7 fleet E2E PASSED")
}

// nowRFC3339 formats a timestamp for HTTP requests.
func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }
