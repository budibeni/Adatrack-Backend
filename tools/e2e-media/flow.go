package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// jpegPayload is a byte-exact (if minimal) JPEG so the round trips can be
// compared verbatim — the FR-8.5 "byte-persis" contract.
func jpegPayload(n int, seed byte) []byte {
	data := make([]byte, 0, n+4)
	data = append(data, 0xFF, 0xD8, 0xFF, 0xE0)
	for i := 0; i < n; i++ {
		data = append(data, seed+byte(i%251))
	}
	data = append(data, 0xFF, 0xD9)
	return data
}

// upper/lower helpers keep the tenant normalisation explicit.
func upper(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }
func lower(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// runChecks executes the whole B5b flow.
func runChecks(ctx context.Context, opt options) []checkResult {
	var results []checkResult
	add := func(r checkResult) { results = append(results, r) }

	cli := newClient(opt.timeout)

	// --- 1. auth + readiness -----------------------------------------------
	token, err := cli.login(opt.wsURL, opt.adminEmail, opt.adminPass)
	if err != nil {
		add(fail("auth.login", "tenant admin session is required", err))
		return results
	}
	add(pass("auth.login", "tenant admin session established"))
	add(checkHealthz(ctx, cli, opt))

	// --- 2. fixtures: media config + vehicle mapping ------------------------
	master, err := openPG(opt.pg, envOr("MASTER_DB_NAME", "adatrack_gps_master"))
	if err != nil {
		add(fail("db.master", "PostgreSQL master unreachable", err))
		return results
	}
	defer func() { _ = master.Close() }()

	vehicleID, companyFromMap, err := vehicleIDForIMEI(ctx, master, opt.imei)
	if err != nil {
		add(fail("device.registered", "IMEI must be registered in tm_vehicle_imei_map", err))
		return results
	}
	add(pass("device.registered", fmt.Sprintf("imei=%s vehicle=%d company=%s", opt.imei, vehicleID, companyFromMap)))
	company := upper(opt.company)
	if company == "" {
		company = companyFromMap
	}

	cfg, err := loadMediaConfig(ctx, master, company)
	if err != nil {
		add(fail("media.config", "tm_company_media_config row is required", err))
		return results
	}
	secret := cfg.hmacSecret
	if secret == "" {
		secret = opt.hmacSecret
	}
	if secret == "" {
		add(fail("media.hmac_secret", "no per-company HMAC secret and no MEDIA_HMAC_SECRET fallback", nil))
		return results
	}
	add(pass("media.config", fmt.Sprintf("bucket=%s retention_days=%d max_file_mb=%d", cfg.bucket, cfg.retentionDays, cfg.maxFileMB)))

	tenant, err := openPG(opt.pg, envOr("COMPANY_DB_PREFIX", "adatrack_gps_")+lower(company))
	if err != nil {
		add(fail("db.tenant", "tenant schema unreachable", err))
		return results
	}
	defer func() { _ = tenant.Close() }()

	// --- 3. WebSocket session (MEDIA_EVENT fan-out) -------------------------
	conn, werr := dialWS(opt, token, opt.timeout)
	if werr != nil {
		add(fail("ws.connect", "WebSocket session is required for the MEDIA_EVENT fan-out", werr))
		return results
	}
	defer func() { _ = conn.Close() }()
	if serr := wsSubscribe(conn, []int64{vehicleID}, opt.timeout); serr != nil {
		add(fail("ws.subscribe", "subscribe to the tested vehicle failed", serr))
		return results
	}
	add(pass("ws.subscribe", fmt.Sprintf("subscribed to vehicle %d", vehicleID)))

	// --- 4. multipart ingest → MinIO → catalog → presigned byte-exact -------
	startedAt := time.Now().UTC().Add(-time.Second)
	multipartData := jpegPayload(512, 0x21)
	body, contentType, merr := multipartBody(map[string]string{
		"imei": opt.imei, "event_type": "sos",
	}, "event.jpg", "image/jpeg", multipartData)
	if merr != nil {
		add(fail("ingest.multipart", "could not build the multipart body", merr))
		return results
	}
	signature := hmacHex(secret, []byte(opt.imei+"\nsos\n"+string(multipartData)))
	headers := map[string]string{
		"X-Company-Code": company,
		"X-Signature":    signature,
		"X-Timestamp":    nowRFC3339(),
		"Content-Type":   contentType,
	}
	status, raw, err := cli.request(http.MethodPost, mediaURL(opt.mediaURL, "/api/v1/media/events"), body, headers)
	if err != nil {
		add(fail("ingest.multipart", "request failed", err))
		return results
	}
	if status != http.StatusCreated {
		add(fail("ingest.multipart", fmt.Sprintf("status %d (%s)", status, trim(raw, 200)), nil))
		return results
	}
	var multipartRow mediaRow
	if derr := decodeEnvelope(raw, &multipartRow); derr != nil {
		add(fail("ingest.multipart", "could not decode the catalog row", derr))
		return results
	}
	add(pass("ingest.multipart", fmt.Sprintf("media_id=%d key=%s status=%s", multipartRow.ID, multipartRow.ObjectKey, multipartRow.Status)))
	add(checkMultipartRow(multipartRow, opt, company, vehicleID, len(multipartData)))
	add(checkPresignedRoundTrip(ctx, cli, opt, token, multipartRow.ID, multipartData, "multipart"))
	add(checkWSEvent(conn, opt, multipartRow.ID, "multipart"))

	// --- 5. JSON + presigned PUT → complete → presigned byte-exact ----------
	jsonData := jpegPayload(384, 0x57)
	jsonBody, _ := json.Marshal(map[string]any{
		"imei": opt.imei, "event_type": "alarm", "mime_type": "image/jpeg",
		"file_size": len(jsonData), "content_sha256": sha256Hex(jsonData),
	})
	jsonHeaders := map[string]string{
		"X-Company-Code": company,
		"X-Signature":    hmacHex(secret, jsonBody),
		"X-Timestamp":    nowRFC3339(),
		"Content-Type":   "application/json",
	}
	status, raw, err = cli.request(http.MethodPost, mediaURL(opt.mediaURL, "/api/v1/media/events"), jsonBody, jsonHeaders)
	if err != nil || status != http.StatusCreated {
		add(fail("ingest.json_ticket", fmt.Sprintf("status %d (%s)", status, trim(raw, 200)), err))
		return results
	}
	var ticket uploadTicket
	if derr := decodeEnvelope(raw, &ticket); derr != nil {
		add(fail("ingest.json_ticket", "could not decode the upload ticket", derr))
		return results
	}
	add(pass("ingest.json_ticket", fmt.Sprintf("media_id=%d status=%s", ticket.ID, ticket.Status)))
	add(checkPresignedPut(ctx, cli, ticket, jsonData))
	add(checkComplete(ctx, cli, opt, ticket, company, secret, jsonData))
	add(checkPresignedRoundTrip(ctx, cli, opt, token, ticket.ID, jsonData, "json"))
	add(checkWSEvent(conn, opt, ticket.ID, "json"))

	// --- 6. negatives, audit, metrics, retention ---------------------------
	add(checkNegativePaths(ctx, cli, opt, master, tenant, company, secret, contentType))
	add(checkAuditTrail(ctx, master, startedAt))
	add(checkMetrics(ctx, cli, opt))
	add(checkRetention(ctx, cli, opt, tenant, master, multipartRow.ID))

	return results
}

// checkMultipartRow asserts the FR-8.2/FR-8.3 catalog contract.
func checkMultipartRow(row mediaRow, opt options, company string, vehicleID int64, size int) checkResult {
	switch {
	case row.Status != "complete":
		return fail("catalog.multipart_row", "status "+row.Status, nil)
	case row.UploadSource != "multipart":
		return fail("catalog.multipart_row", "upload_source "+row.UploadSource, nil)
	case row.MimeType != "image/jpeg":
		return fail("catalog.multipart_row", "mime_type "+row.MimeType, nil)
	case row.FileSize != int64(size):
		return fail("catalog.multipart_row", fmt.Sprintf("file_size %d want %d", row.FileSize, size), nil)
	case row.VehicleID != vehicleID:
		return fail("catalog.multipart_row", fmt.Sprintf("vehicle_id %d want %d", row.VehicleID, vehicleID), nil)
	case !strings.HasPrefix(row.ObjectKey, lower(company)+"/"+fmt.Sprint(vehicleID)+"/"):
		return fail("catalog.multipart_row", "object_key "+row.ObjectKey+" does not match {company}/{vehicle}/{yyyyMM}/{uuid}", nil)
	}
	return pass("catalog.multipart_row", "lifecycle + key layout + size verified")
}

// checkPresignedPut uploads through the presigned PUT URL (FR-8.1 JSON flow).
func checkPresignedPut(ctx context.Context, cli *client, ticket uploadTicket, data []byte) checkResult {
	if ticket.UploadURL == "" || ticket.Status != "pending" {
		return fail("ingest.presigned_put", fmt.Sprintf("ticket=%+v", ticket), nil)
	}
	status, raw, err := cli.request(http.MethodPut, ticket.UploadURL, data,
		map[string]string{"Content-Type": "image/jpeg"})
	if err != nil {
		return fail("ingest.presigned_put", "PUT to the presigned URL failed", err)
	}
	if status != http.StatusOK {
		return fail("ingest.presigned_put", fmt.Sprintf("status %d (%s)", status, trim(raw, 120)), nil)
	}
	return pass("ingest.presigned_put", fmt.Sprintf("uploaded %d bytes through the presigned PUT URL", len(data)))
}

// checkComplete finalises the JSON flow and asserts the lifecycle move.
func checkComplete(ctx context.Context, cli *client, opt options, ticket uploadTicket, company, secret string, data []byte) checkResult {
	path := "/api/v1/media/events/" + fmt.Sprint(ticket.ID) + "/complete"
	headers := map[string]string{
		"X-Company-Code": company,
		"X-Signature":    hmacHex(secret, nil),
		"X-Timestamp":    nowRFC3339(),
		"Content-Type":   "application/json",
	}
	status, raw, err := cli.request(http.MethodPost, mediaURL(opt.mediaURL, path), nil, headers)
	if err != nil {
		return fail("ingest.complete", "complete request failed", err)
	}
	if status != http.StatusOK {
		return fail("ingest.complete", fmt.Sprintf("status %d (%s)", status, trim(raw, 200)), nil)
	}
	var row mediaRow
	if derr := decodeEnvelope(raw, &row); derr != nil {
		return fail("ingest.complete", "could not decode the completed row", derr)
	}
	if row.Status != "complete" || row.ExpiresAt == "" {
		return fail("ingest.complete", fmt.Sprintf("row=%+v", row), nil)
	}
	return pass("ingest.complete", fmt.Sprintf("media_id=%d complete, expires_at set, %d bytes", row.ID, row.FileSize))
}
