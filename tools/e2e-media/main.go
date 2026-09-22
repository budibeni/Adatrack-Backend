// Command e2e-media is the end-to-end verification harness of phase B5b
// (service-media, PRD Module 8 / Scope A). It drives the REAL service through
// its public interfaces only:
//
// device/edge → HMAC ingest (multipart | JSON+presigned PUT) → MinIO →
// th_media_events catalog → presigned GET (byte-exact) → WS MEDIA_EVENT →
// audit MEDIA_URL_ACCESS → retention sweep (FR-8.7)
//
// and asserts the B5b acceptance criteria, including the negative paths
// (401/400/404/oversize) and that the media audit + metrics are recorded.
//
// Usage: scripts/e2e-media.sh [flags]
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

func main() {
	opt := parseFlags()
	ctx, cancel := context.WithTimeout(context.Background(), opt.timeout*14)
	defer cancel()

	report(runChecks(ctx, opt))
}

// checkResult is one E2E assertion.
type checkResult struct {
	Name   string
	Detail string
	Err    error
}

// pass builds a successful assertion.
func pass(name, detail string) checkResult { return checkResult{Name: name, Detail: detail} }

// fail builds a failed assertion (a nil error is replaced so FAIL is never
// reported as PASS).
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
		fmt.Printf("[%s] %-32s %s", status, r.Name, r.Detail)
		if r.Err != nil {
			fmt.Printf("  error=%v", r.Err)
		}
		fmt.Println()
	}
	fmt.Printf("\nB5b media E2E summary: %d/%d checks passed\n", len(results)-failed, len(results))
	if failed > 0 {
		fmt.Fprintln(os.Stderr, "B5b media E2E FAILED")
		os.Exit(1)
	}
	fmt.Println("B5b media E2E PASSED")
}

// envelope decodes the PRD §8.1 success envelope.
type envelope struct {
	Status string          `json:"status"`
	Data   json.RawMessage `json:"data"`
}

// errorEnvelope decodes the PRD §8.1 error envelope.
type errorEnvelope struct {
	Status    string `json:"status"`
	ErrorCode string `json:"error_code"`
	Message   string `json:"message"`
}

// mediaRow mirrors the catalog row fields the harness asserts on.
type mediaRow struct {
	ID           int64  `json:"id"`
	VehicleID    int64  `json:"vehicle_id"`
	IMEI         string `json:"imei"`
	EventType    string `json:"event_type"`
	ObjectKey    string `json:"object_key"`
	FileSize     int64  `json:"file_size"`
	MimeType     string `json:"mime_type"`
	Status       string `json:"status"`
	UploadSource string `json:"upload_source"`
	ExpiresAt    string `json:"expires_at,omitempty"`
}

// uploadTicket is the JSON-flow answer.
type uploadTicket struct {
	ID              int64  `json:"id"`
	ObjectKey       string `json:"object_key"`
	UploadURL       string `json:"upload_url"`
	UploadExpiresIn int    `json:"upload_expires_in"`
	Status          string `json:"status"`
	CompleteURL     string `json:"complete_url"`
}

// sha256Hex is the content hash used for the assertions.
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// nowRFC3339 is the request timestamp (anti-replay window).
func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }
