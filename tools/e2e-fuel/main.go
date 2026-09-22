// Command e2e-fuel is the end-to-end verification harness of phase B5a
// (fuel sensor, PRD Module 7). It drives the REAL pipeline:
//
// device frame (GT06 0x94/0x0D `!AIOIL`) → ingestion-tcp → worker-live (Redis)
// → worker-persistence (td_fuel_logs) → worker-alert (FUEL_DROP) →
// alert.fuel.<company> + notify.alert.<vehicle_id> → service-websocket → WS
//
// plus the FR-7.7 history endpoint. This is the B5a acceptance that needed live
// infra (see .agent/03-backend-phases.md).
//
// Usage: scripts/e2e-fuel.sh [flags]
package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

func main() {
	opt := parseFlags()
	ctx, cancel := context.WithTimeout(context.Background(), opt.timeout*16)
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
		fmt.Printf("[%s] %-30s %s", status, r.Name, r.Detail)
		if r.Err != nil {
			fmt.Printf("  error=%v", r.Err)
		}
		fmt.Println()
	}
	fmt.Printf("\nB5a fuel E2E summary: %d/%d checks passed\n", len(results)-failed, len(results))
	if failed > 0 {
		fmt.Fprintln(os.Stderr, "B5a fuel E2E FAILED")
		os.Exit(1)
	}
	fmt.Println("B5a fuel E2E PASSED")
}

// envelope decodes the PRD §8.1 success envelope.
type envelope struct {
	Status string          `json:"status"`
	Data   json.RawMessage `json:"data"`
}

// hmacHex is unused by the fuel flow but kept symmetrical with the media tool.
func hmacHex(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

// nowRFC3339 formats a timestamp for HTTP requests.
func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }
