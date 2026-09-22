package controllers

// settings_test.go — konfigurasi + whitelist Origin service-media (hermetik).
//
// settings.go (LoadSettings/Validate/OriginAllowed/splitList) sebelumnya 0 %:
// validasi konfigurasi (§10.2) dan whitelist Origin CORS (§9.3) tidak pernah
// diuji, padahal keduanya penentu fail-closed saat deploy salah konfigurasi.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"adatrack_gps/internal"
	"adatrack_gps/internal/storage"
	"adatrack_gps/service-media/models"
)

// TestSplitList pins the comma-list parser used by WS_ALLOWED_ORIGINS.
func TestSplitList(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want []string
	}{
		{name: "empty yields empty", raw: "", want: []string{}},
		{name: "only separators yields empty", raw: " , ,", want: []string{}},
		{name: "trims whitespace and drops blanks", raw: " https://a.example , https://b.example ,", want: []string{"https://a.example", "https://b.example"}},
		{name: "single value", raw: "https://a.example", want: []string{"https://a.example"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := splitList(tc.raw)
			if len(got) != len(tc.want) {
				t.Fatalf("splitList(%q) = %v, want %v", tc.raw, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("splitList(%q)[%d] = %q, want %q", tc.raw, i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestOriginAllowed covers the CORS allowlist (PRD §9.3): exact match is
// case-insensitive, whitespace is tolerated, and anything else is denied.
func TestOriginAllowed(t *testing.T) {
	s := Settings{AllowedOrigins: []string{"https://fleet.example", "http://localhost:3000"}, AllowEmptyOrigin: true}

	if !s.OriginAllowed("  https://fleet.example ") {
		t.Fatal("origin terdaftar (dengan spasi) ditolak")
	}
	if !s.OriginAllowed("HTTPS://FLEET.EXAMPLE") {
		t.Fatal("perbandingan origin harus case-insensitive")
	}
	if s.OriginAllowed("https://evil.example") {
		t.Fatal("origin tak terdaftar diterima")
	}
	if s.OriginAllowed("https://fleet.example.evil") {
		t.Fatal("suffix-matching liar diterima (harus exact match)")
	}
	if !s.OriginAllowed("") {
		t.Fatal("origin kosong ditolak padahal AllowEmptyOrigin=true")
	}

	strict := s
	strict.AllowEmptyOrigin = false
	if strict.OriginAllowed("") {
		t.Fatal("origin kosong diterima padahal AllowEmptyOrigin=false")
	}

	empty := Settings{}
	if empty.OriginAllowed("https://fleet.example") {
		t.Fatal("allowlist kosong menerima origin apa pun")
	}
}

// TestSettingsValidate fails closed on a configuration that cannot serve traffic.
func TestSettingsValidate(t *testing.T) {
	valid := Settings{
		HTTPAddr: ":8095", JWTSecret: strings.Repeat("s", MinJWTSecretLen), JWTIssuer: "adatrack",
		MaxFileMB: 128, PresignTTL: 2 * time.Minute, RetentionDays: 30, CleanupCron: "0 3 * * *",
		HMACSecret: "0123456789abcdef",
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate(valid) = %v, want nil", err)
	}

	broken := map[string]func(*Settings){
		"MEDIA_HTTP_ADDR empty":  func(s *Settings) { s.HTTPAddr = " " },
		"JWT_SECRET too short":   func(s *Settings) { s.JWTSecret = strings.Repeat("s", MinJWTSecretLen-1) },
		"JWT_ISSUER empty":       func(s *Settings) { s.JWTIssuer = "" },
		"MEDIA_MAX_FILE_MB <= 0": func(s *Settings) { s.MaxFileMB = 0 },
		"MEDIA_PRESIGN_TTL <= 0": func(s *Settings) { s.PresignTTL = 0 },
		"MEDIA_RETENTION_DAYS":   func(s *Settings) { s.RetentionDays = 0 },
		"bad cleanup cron":       func(s *Settings) { s.CleanupCron = "not a cron" },
		"short HMAC secret":      func(s *Settings) { s.HMACSecret = "short" },
	}
	for name, mutate := range broken {
		t.Run(name, func(t *testing.T) {
			s := valid
			mutate(&s)
			if err := s.Validate(); err == nil {
				t.Fatalf("Validate(%s) = nil, want error", name)
			}
		})
	}

	// A dense sweep override replaces the cron schedule, so a broken cron string
	// must NOT block startup in that mode (dev/E2E path).
	override := valid
	override.CleanupCron = "not a cron"
	override.SweepOverrideSeconds = 5
	if err := override.Validate(); err != nil {
		t.Fatalf("Validate(dengan SweepOverrideSeconds) = %v, want nil", err)
	}

	// An empty HMAC secret is allowed (per-company secrets come from the master
	// config); a short one is not.
	noSecret := valid
	noSecret.HMACSecret = ""
	if err := noSecret.Validate(); err != nil {
		t.Fatalf("Validate(tanpa HMAC secret) = %v, want nil", err)
	}
}

// TestLoadSettings pins the defaults (dev-safe) and the env/Media plumbing, so a
// renamed env key or a dropped field is caught here instead of at deploy time.
func TestLoadSettingsDefaults(t *testing.T) {
	// Explicitly cleared: EnvOr/EnvIntDefault/EnvBoolDefault fall back to the
	// default when the value is empty, so this is deterministic even when the
	// measurement harness has sourced .env.<variant>.
	for _, k := range []string{"MEDIA_HTTP_ADDR", "MEDIA_METRICS_ADDR", "MEDIA_CLEANUP_CRON",
		"MEDIA_RETENTION_SWEEP_SEC", "MEDIA_PENDING_TTL_HOURS", "MEDIA_CONFIG_CACHE_SEC",
		"JWT_ISSUER", "JWT_CLOCK_SKEW_SEC", "AUTH_DENYLIST_PREFIX", "MEDIA_HMAC_MAX_SKEW_SEC",
		"WS_ALLOWED_ORIGINS"} {
		t.Setenv(k, "")
	}

	cfg := &internal.Config{}
	cfg.Media.S3Bucket = "bucket-from-cfg"
	cfg.Media.MaxFileMB = 64
	cfg.Media.PresignTTL = 3 * time.Minute
	cfg.Media.RetentionDays = 45
	cfg.Media.HMACSecret = "cfg-secret-16chars"

	s := LoadSettings(cfg)

	if s.HTTPAddr != ":8095" || s.MetricsAddr != ":8096" {
		t.Fatalf("addr default = %q/%q, want :8095/:8096", s.HTTPAddr, s.MetricsAddr)
	}
	if s.CleanupCron != "0 3 * * *" {
		t.Fatalf("CleanupCron default = %q, want 0 3 * * *", s.CleanupCron)
	}
	if s.SweepOverrideSeconds != 0 || s.PendingTTLHours != 24 {
		t.Fatalf("retention default = override %d / pending %d jam, want 0/24", s.SweepOverrideSeconds, s.PendingTTLHours)
	}
	if s.ConfigCacheTTL != time.Minute {
		t.Fatalf("ConfigCacheTTL default = %v, want 1m", s.ConfigCacheTTL)
	}
	if s.JWTIssuer != "adatrack" || s.JWTClockSkew != 30*time.Second {
		t.Fatalf("jwt default = %q/%v, want adatrack/30s", s.JWTIssuer, s.JWTClockSkew)
	}
	if s.HMACMaxSkew != 300*time.Second {
		t.Fatalf("HMACMaxSkew default = %v, want 5m", s.HMACMaxSkew)
	}
	if len(s.AllowedOrigins) != 2 || s.AllowedOrigins[0] != "http://localhost:3000" {
		t.Fatalf("AllowedOrigins default = %v", s.AllowedOrigins)
	}

	// Storage/storage-behaviour values come from cfg.Media (single source of truth).
	if s.Bucket != "bucket-from-cfg" || s.MaxFileMB != 64 || s.RetentionDays != 45 {
		t.Fatalf("cfg.Media tidak terpetakan: %+v", s)
	}
	if s.PresignTTL != 3*time.Minute || s.HMACSecret != "cfg-secret-16chars" {
		t.Fatalf("presign/HMAC cfg.Media tidak terpetakan: %v/%q", s.PresignTTL, s.HMACSecret)
	}
}

// TestLoadSettingsOverrides asserts the documented env keys really win.
func TestLoadSettingsOverrides(t *testing.T) {
	t.Setenv("MEDIA_HTTP_ADDR", ":9999")
	t.Setenv("MEDIA_METRICS_ADDR", ":9998")
	t.Setenv("MEDIA_CLEANUP_CRON", "15 4 * * 1")
	t.Setenv("MEDIA_RETENTION_SWEEP_SEC", "5")
	t.Setenv("MEDIA_PENDING_TTL_HOURS", "7")
	t.Setenv("MEDIA_HMAC_MAX_SKEW_SEC", "60")
	t.Setenv("WS_ALLOWED_ORIGINS", "https://fleet.example, https://ops.example")
	t.Setenv("WS_ALLOW_EMPTY_ORIGIN", "false")
	t.Setenv("JWT_REVOCATION_ENABLED", "false")
	t.Setenv("MEDIA_INGEST_RATE_LIMIT", "42")

	s := LoadSettings(&internal.Config{})

	if s.HTTPAddr != ":9999" || s.MetricsAddr != ":9998" {
		t.Fatalf("addr override = %q/%q", s.HTTPAddr, s.MetricsAddr)
	}
	if s.CleanupCron != "15 4 * * 1" || s.SweepOverrideSeconds != 5 || s.PendingTTLHours != 7 {
		t.Fatalf("retention override tidak berlaku: %q/%d/%d", s.CleanupCron, s.SweepOverrideSeconds, s.PendingTTLHours)
	}
	if s.HMACMaxSkew != time.Minute {
		t.Fatalf("HMACMaxSkew override = %v, want 1m", s.HMACMaxSkew)
	}
	if len(s.AllowedOrigins) != 2 || s.AllowedOrigins[1] != "https://ops.example" {
		t.Fatalf("AllowedOrigins override = %v", s.AllowedOrigins)
	}
	if s.AllowEmptyOrigin || s.RevocationEnabled {
		t.Fatalf("bool override tidak berlaku: empty=%v revocation=%v", s.AllowEmptyOrigin, s.RevocationEnabled)
	}
	if s.IngestRateLimit != 42 {
		t.Fatalf("IngestRateLimit override = %d, want 42", s.IngestRateLimit)
	}
}

// TestMaxFileBytes covers the MB -> bytes conversion (0/negative = unlimited).
func TestMaxFileBytes(t *testing.T) {
	if got := MaxFileBytes(0); got != 0 {
		t.Fatalf("MaxFileBytes(0) = %d, want 0", got)
	}
	if got := MaxFileBytes(-5); got != 0 {
		t.Fatalf("MaxFileBytes(-5) = %d, want 0", got)
	}
	if got := MaxFileBytes(2); got != 2<<20 {
		t.Fatalf("MaxFileBytes(2) = %d, want %d", got, 2<<20)
	}
}

// TestValidStatus pins the lifecycle whitelist used by the catalog filters (§8.5).
func TestValidStatus(t *testing.T) {
	for _, ok := range []string{models.StatusPending, models.StatusComplete, models.StatusExpired, models.StatusDeleted} {
		if !validStatus(ok) {
			t.Fatalf("validStatus(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", "PENDING", "unknown", "complete ", "sos"} {
		if validStatus(bad) {
			t.Fatalf("validStatus(%q) = true, want false", bad)
		}
	}
}

// failingStorage implements storage.Store with a broken Health: it proves /healthz
// fails closed (503 + status="degraded") instead of reporting ready (PRD §10.2).
// Only Health is exercised, so the embedded nil interface is never called.
type failingStorage struct{ storage.Store }

func (failingStorage) Health(context.Context) error { return errors.New("minio down") }

// TestHealthzAndAccessors covers the readiness endpoint + the service accessors the
// router/handlers use. Previously handleHealthz was 0 %: a broken object store or
// tenant pool would have gone unnoticed by the acceptance path.
func TestHealthzAndAccessors(t *testing.T) {
	svc, mem := newTestService(t, newFakeStore())

	if svc.Storage() != storage.Store(mem) {
		t.Fatal("Storage() tidak mengembalikan store yang disuntikkan")
	}
	if svc.Auditor() == nil {
		t.Fatal("Auditor() = nil")
	}

	// /livez is independent of dependencies.
	if live := doRequest(t, svc, http.MethodGet, "/livez", nil, nil); live.Code != http.StatusOK {
		t.Fatalf("GET /livez = %d, want 200", live.Code)
	}

	// Fail closed: the fake store's master pool is a zero value, so readiness must
	// report 503/degraded instead of claiming the service is ready (PRD §10.2).
	rec := doRequest(t, svc, http.MethodGet, "/healthz", nil, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /healthz (pool belum siap) = %d, want 503", rec.Code)
	}
	body := rec.Body.Bytes()
	var payload struct {
		Status string            `json:"status"`
		Checks map[string]string `json:"checks"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("healthz body bukan JSON valid: %v (%s)", err, string(body))
	}
	if payload.Status != "degraded" {
		t.Fatalf("status = %q, want degraded", payload.Status)
	}
	if payload.Checks["object_storage"] != "ok" || payload.Checks["tenant_pools"] != "ok" {
		t.Fatalf("checks sehat tidak dilaporkan ok: %v", payload.Checks)
	}
	if !strings.HasPrefix(payload.Checks["postgres_master"], "error") {
		t.Fatalf("postgres_master = %q, want error (pool belum siap harus fail-closed)", payload.Checks["postgres_master"])
	}

	// Fail closed: a storage outage must degrade the readiness probe.
	svc.storage = failingStorage{}
	degraded := doRequest(t, svc, http.MethodGet, "/healthz", nil, nil)
	if degraded.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /healthz (storage mati) = %d, want 503", degraded.Code)
	}
	if !strings.Contains(degraded.Body.String(), "degraded") || !strings.Contains(degraded.Body.String(), "minio down") {
		t.Fatalf("body degraded tidak informatif: %s", degraded.Body.String())
	}
}
