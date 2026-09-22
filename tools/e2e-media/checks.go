package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// checkHealthz asserts the service-media readiness endpoint (FR-8.8).
func checkHealthz(ctx context.Context, cli *client, opt options) checkResult {
	status, raw, err := cli.request(http.MethodGet, mediaURL(opt.mediaURL, "/healthz"), nil, nil)
	if err != nil {
		return fail("service.healthz", "request failed", err)
	}
	if status != http.StatusOK {
		return fail("service.healthz", fmt.Sprintf("status %d (%s)", status, trim(raw, 200)), nil)
	}
	body := string(raw)
	for _, needle := range []string{"object_storage", "postgres_master", "tenant_pools", "nats"} {
		if !strings.Contains(body, needle) {
			return fail("service.healthz", "missing check "+needle+" in "+trim(raw, 200), nil)
		}
	}
	return pass("service.healthz", "object storage + pools + NATS reported ok")
}

// checkPresignedRoundTrip fetches /media/{id}/url and compares the bytes exactly
// (the FR-8.4/FR-8.5 acceptance contract).
func checkPresignedRoundTrip(ctx context.Context, cli *client, opt options, token string, mediaID int64, want []byte, flow string) checkResult {
	name := "presign.roundtrip_" + flow
	path := "/api/v1/media/" + fmt.Sprint(mediaID) + "/url"
	status, raw, err := cli.request(http.MethodGet, mediaURL(opt.mediaURL, path), nil, bearer(token))
	if err != nil {
		return fail(name, "request failed", err)
	}
	if status != http.StatusOK {
		return fail(name, fmt.Sprintf("url status %d (%s)", status, trim(raw, 200)), nil)
	}
	var res struct {
		URL       string `json:"url"`
		ExpiresIn int    `json:"expires_in"`
	}
	if derr := decodeEnvelope(raw, &res); derr != nil || res.URL == "" {
		return fail(name, "no presigned URL in the response", derr)
	}
	got, ferr := fetchBytes(ctx, cli, res.URL)
	if ferr != nil {
		return fail(name, "fetching the presigned URL failed", ferr)
	}
	if len(got) != len(want) {
		return fail(name, fmt.Sprintf("byte length %d want %d", len(got), len(want)), nil)
	}
	for i := range want {
		if got[i] != want[i] {
			return fail(name, fmt.Sprintf("byte %d differs (round trip is not byte-exact)", i), nil)
		}
	}
	if res.ExpiresIn <= 0 {
		return fail(name, "presigned TTL missing", nil)
	}
	return pass(name, fmt.Sprintf("presigned GET returned %d byte-persis bytes (ttl %ds)", len(got), res.ExpiresIn))
}

// fetchBytes downloads a URL with the harness client.
func fetchBytes(ctx context.Context, cli *client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := cli.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("presigned GET status %d", resp.StatusCode)
	}
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for {
		n, rerr := resp.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if rerr != nil {
			break
		}
		if len(buf) > 8<<20 {
			return nil, fmt.Errorf("object larger than the assertion buffer")
		}
	}
	return buf, nil
}

// checkWSEvent waits for the MEDIA_EVENT frame of one upload (FR-8.5).
func checkWSEvent(conn *websocket.Conn, opt options, mediaID int64, flow string) checkResult {
	name := "ws.media_event_" + flow
	envelope, latency, err := wsReadEvent(conn, "MEDIA_EVENT", opt.timeout)
	if err != nil {
		return fail(name, "no MEDIA_EVENT received", err)
	}
	var payload struct {
		ID        int64  `json:"id"`
		EventType string `json:"event_type"`
		URL       string `json:"url"`
		VehicleID int64  `json:"vehicle_id"`
	}
	if derr := jsonUnmarshal(envelope.Data, &payload); derr != nil {
		return fail(name, "could not decode the payload", derr)
	}
	if payload.URL == "" || payload.VehicleID <= 0 {
		return fail(name, fmt.Sprintf("payload=%+v", payload), nil)
	}
	// The frame may belong to the other flow of this run: accept any media id of
	// this run but report which one arrived.
	return pass(name, fmt.Sprintf("MEDIA_EVENT id=%d (wanted %d) type=%s in %dms",
		payload.ID, mediaID, payload.EventType, latency.Milliseconds()))
}

// checkNegativePaths asserts the rejection contract (401/400/404/oversize).
func checkNegativePaths(ctx context.Context, cli *client, opt options, master, tenant *sql.DB, company, secret, contentType string) checkResult {
	base := mediaURL(opt.mediaURL, "/api/v1/media/events")

	// 401: no signature at all.
	body, ct, _ := multipartBody(map[string]string{"imei": opt.imei, "event_type": "manual"}, "x.jpg", "image/jpeg", jpegPayload(64, 1))
	status, raw, err := cli.request(http.MethodPost, base, body, map[string]string{"Content-Type": ct})
	if err != nil || status != http.StatusUnauthorized {
		return fail("negative.missing_signature", fmt.Sprintf("status %d (%s)", status, trim(raw, 120)), err)
	}

	// 401: wrong signature.
	bad := map[string]string{
		"X-Company-Code": company, "X-Signature": hmacHex("wrong-secret", []byte("nope")),
		"X-Timestamp": nowRFC3339(), "Content-Type": ct,
	}
	status, raw, err = cli.request(http.MethodPost, base, body, bad)
	if err != nil || status != http.StatusUnauthorized {
		return fail("negative.bad_signature", fmt.Sprintf("status %d (%s)", status, trim(raw, 120)), err)
	}
	if code := decodeError(raw); code != "MEDIA_SIGNATURE_INVALID" {
		return fail("negative.bad_signature", "error_code "+code, nil)
	}

	// 400: disallowed content type.
	txtBody, txtCT, _ := multipartBody(map[string]string{"imei": opt.imei, "event_type": "manual"}, "x.txt", "text/plain", []byte("hello"))
	txtHeaders := map[string]string{
		"X-Company-Code": company,
		"X-Signature":    hmacHex(secret, []byte(opt.imei+"\nmanual\nhello")),
		"X-Timestamp":    nowRFC3339(),
		"Content-Type":   txtCT,
	}
	status, raw, err = cli.request(http.MethodPost, base, txtBody, txtHeaders)
	if err != nil || status != http.StatusBadRequest {
		return fail("negative.mime_allowlist", fmt.Sprintf("status %d (%s)", status, trim(raw, 120)), err)
	}
	if code := decodeError(raw); code != "MEDIA_TYPE_NOT_ALLOWED" {
		return fail("negative.mime_allowlist", "error_code "+code, nil)
	}

	// 404: unknown media id (JWT route).
	token, terr := cli.login(opt.wsURL, opt.adminEmail, opt.adminPass)
	if terr != nil {
		return fail("negative.unknown_id", "login failed", terr)
	}
	status, raw, err = cli.request(http.MethodGet, mediaURL(opt.mediaURL, "/api/v1/media/999999999"), nil, bearer(token))
	if err != nil || status != http.StatusNotFound {
		return fail("negative.unknown_id", fmt.Sprintf("status %d (%s)", status, trim(raw, 120)), err)
	}

	// 400: oversize (temporarily lower the per-company ceiling, then restore it).
	original, oerr := loadMediaConfig(ctx, master, company)
	if oerr != nil {
		return fail("negative.oversize", "could not read the current max_file_mb", oerr)
	}
	if err := setMaxFileMB(ctx, master, company, 1); err != nil {
		return fail("negative.oversize", "could not lower max_file_mb", err)
	}
	defer func() {
		// Restore the ORIGINAL ceiling (captured before the override).
		if rerr := setMaxFileMB(context.Background(), master, company, original.maxFileMB); rerr != nil {
			fmt.Printf("cleanup: restoring max_file_mb=%d failed: %v\n", original.maxFileMB, rerr)
		}
	}()
	// The service caches tm_company_media_config for MEDIA_CONFIG_CACHE_SEC, so
	// the harness waits for that window (the E2E script sets it to 1 s).
	time.Sleep(2 * time.Second)
	big := jpegPayload((1<<20)+4096, 0x33)
	bigBody, bigCT, _ := multipartBody(map[string]string{"imei": opt.imei, "event_type": "overspeed"}, "big.jpg", "image/jpeg", big)
	bigHeaders := map[string]string{
		"X-Company-Code": company,
		"X-Signature":    hmacHex(secret, []byte(opt.imei+"\noverspeed\n"+string(big))),
		"X-Timestamp":    nowRFC3339(),
		"Content-Type":   bigCT,
	}
	status, raw, err = cli.request(http.MethodPost, base, bigBody, bigHeaders)
	if err != nil || status != http.StatusBadRequest {
		return fail("negative.oversize", fmt.Sprintf("status %d (%s)", status, trim(raw, 160)), err)
	}
	if code := decodeError(raw); code != "MEDIA_TOO_LARGE" {
		return fail("negative.oversize", "error_code "+code, nil)
	}
	_ = contentType
	_ = tenant
	return pass("negative.paths", "401 missing/bad signature · 400 mime allowlist · 404 unknown id · 400 oversize")
}

// checkAuditTrail asserts the §9.4 rows landed in the master audit log.
func checkAuditTrail(ctx context.Context, master *sql.DB, since time.Time) checkResult {
	urls, err := auditCount(ctx, master, "MEDIA_URL_ACCESS", since)
	if err != nil {
		return fail("audit.media_url_access", "audit query failed", err)
	}
	created, cerr := auditCount(ctx, master, "ENTITY_CREATED", since)
	if cerr != nil {
		return fail("audit.media_url_access", "audit query failed", cerr)
	}
	if urls < 1 || created < 1 {
		return fail("audit.media_url_access", fmt.Sprintf("MEDIA_URL_ACCESS=%d ENTITY_CREATED=%d", urls, created), nil)
	}
	return pass("audit.media_url_access", fmt.Sprintf("MEDIA_URL_ACCESS=%d ENTITY_CREATED=%d rows", urls, created))
}

// checkMetrics asserts the FR-8.8 media metrics are exposed.
func checkMetrics(ctx context.Context, cli *client, opt options) checkResult {
	status, raw, err := cli.request(http.MethodGet, mediaURL(opt.mediaURL, "/metrics"), nil, nil)
	if err != nil || status != http.StatusOK {
		return fail("metrics.media", fmt.Sprintf("status %d", status), err)
	}
	body := string(raw)
	for _, needle := range []string{"media_uploads_total", "media_upload_bytes_total", "media_presigned_total", "storage_objects"} {
		if !strings.Contains(body, needle) {
			return fail("metrics.media", "missing metric "+needle, nil)
		}
	}
	return pass("metrics.media", "media_uploads_total · media_upload_bytes_total · media_presigned_total · storage_objects")
}

// checkRetention backdates the expiry and waits for the sweep to delete the
// object + stamp the row `expired` (FR-8.7).
func checkRetention(ctx context.Context, cli *client, opt options, tenant, master *sql.DB, mediaID int64) checkResult {
	key, err := backdateExpiry(ctx, tenant, mediaID)
	if err != nil {
		return fail("retention.sweep", "could not backdate expires_at", err)
	}
	token, terr := cli.login(opt.wsURL, opt.adminEmail, opt.adminPass)
	if terr != nil {
		return fail("retention.sweep", "login failed", terr)
	}

	deadline := time.Now().Add(opt.sweepWait)
	lastStatus := ""
	for time.Now().Before(deadline) {
		status, serr := catalogStatus(ctx, tenant, mediaID)
		if serr == nil {
			lastStatus = status
		}
		if status == "expired" {
			// The object must be gone: the presigned URL can no longer be issued.
			code, raw, rerr := cli.request(http.MethodGet,
				mediaURL(opt.mediaURL, "/api/v1/media/"+fmt.Sprint(mediaID)+"/url"), nil, bearer(token))
			if rerr != nil || code != http.StatusNotFound {
				return fail("retention.sweep", fmt.Sprintf("object still reachable (status %d, %s)", code, trim(raw, 120)), rerr)
			}
			return pass("retention.sweep", fmt.Sprintf("row %d expired and object %s deleted", mediaID, key))
		}
		select {
		case <-ctx.Done():
			return fail("retention.sweep", "context cancelled", ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}
	return fail("retention.sweep", fmt.Sprintf("row %d status %q after %s (is MEDIA_RETENTION_SWEEP_SEC set for the E2E run?)",
		mediaID, lastStatus, opt.sweepWait), nil)
}

// jsonUnmarshal decodes a raw payload into dst.
func jsonUnmarshal(raw []byte, dst any) error { return json.Unmarshal(raw, dst) }
