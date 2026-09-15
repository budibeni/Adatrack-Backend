package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/crypto/bcrypt"

	"ajb_gps/internal"
	"ajb_gps/service-websocket/models"
)

// bcryptHashForTest produces a real (but cheap) bcrypt hash for the fake users.
// Tests only need a verifiable hash; cost 4 keeps the suite fast.
func bcryptHashForTest(password string) string {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 4)
	if err != nil {
		panic(err)
	}
	return string(hash)
}

// fakeLive is an in-memory LiveStateStore.
type fakeLive struct {
	states     map[string]models.LiveState
	callCount  int
	companyHit []string
}

func newFakeLive() *fakeLive { return &fakeLive{states: map[string]models.LiveState{}} }

// set registers a live state for an IMEI.
func (f *fakeLive) set(imei string, state models.LiveState) {
	state.IMEI = imei
	f.states[imei] = state
}

// LiveStates implements LiveStateStore.
func (f *fakeLive) LiveStates(_ context.Context, companyCode string, imeis []string) map[string]models.LiveState {
	f.callCount++
	f.companyHit = append(f.companyHit, companyCode)
	out := map[string]models.LiveState{}
	for _, imei := range imeis {
		if st, ok := f.states[imei]; ok {
			out[imei] = st
		}
	}
	return out
}

// testHarness bundles a Service with its fakes and an httptest server.
type testHarness struct {
	service *Service
	store   *fakeStore
	kv      *MemoryKV
	live    *fakeLive
	server  *httptest.Server
	engine  *gin.Engine
}

// sharedRegistry is built exactly ONCE per test binary, mirroring production
// where GetRegistry() is called a single time at boot. Re-creating it inside every
// test would reassign the package-level metric vars that in-flight request
// goroutines of a previous test may still be reading (a data race).
var (
	sharedRegistry     *prometheus.Registry
	sharedRegistryOnce sync.Once
)

// testRegistry returns the process-wide test registry.
func testRegistry() *prometheus.Registry {
	sharedRegistryOnce.Do(func() { sharedRegistry = internal.GetRegistry() })
	return sharedRegistry
}

// newHarness builds the service under test with hermetic collaborators.
func newHarness(t *testing.T) *testHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)

	store := newTestStore()
	kv := NewMemoryKV()
	live := newFakeLive()

	// The bcrypt cost is intentionally low in tests (the production default is 12
	// and is asserted in the config tests).
	settings := Settings{
		HTTPAddr:              ":0",
		JWTSecret:             "test-secret-key-at-least-32-characters-long",
		JWTIssuer:             "adatrack-test",
		AccessExpiry:          24 * time.Hour,
		RefreshExpiry:         168 * time.Hour,
		RevocationEnabled:     true,
		ClockSkew:             30 * time.Second,
		RefreshPrefix:         "test:refresh:",
		DenylistPrefix:        "test:denylist:",
		BcryptCost:            4,
		LoginRateLimit:        5,
		LoginRateWindow:       15 * time.Minute,
		LoginLockoutThreshold: 5,
		LoginLockoutDuration:  15 * time.Minute,
		APIRateLimit:          100,
		APIRateWindow:         time.Minute,
		DefaultPageSize:       100,
		MaxPageSize:           1000,
		HistoryMaxRangeDays:   90,
		MaxBodyBytes:          1 << 20,
		WSMaxConnections:      50,
		WSSendBufferSize:      256 * 1024,
		WSMaxQueueSize:        16,
		WSPingInterval:        time.Second,
		WSWriteTimeout:        5 * time.Second,
		WSPongTimeout:         30 * time.Second,
		WSReadLimit:           4096,
		WSSubscribeMax:        100,
		AllowEmptyOrigin:      true,
		AllowedOrigins:        []string{"http://localhost:3000"},
		DefaultAdminPassword:  "Admin@123",
		AdminEmailDomain:      "local",
		AuditEnabled:          true,
		AuditQueueSize:        256,
		AuditBatchSize:        10,
		AuditFlushEvery:       10 * time.Millisecond,
	}

	svc := NewService(Deps{
		Settings: settings,
		Store:    store,
		KV:       kv,
		Live:     live,
		Registry: testRegistry(),
	})
	svc.Start()
	t.Cleanup(svc.Stop)

	server := httptest.NewServer(svc.Handler())
	t.Cleanup(server.Close)

	return &testHarness{service: svc, store: store, kv: kv, live: live, server: server, engine: svc.Handler()}
}

// do issues one JSON request against the harness server.
func (h *testHarness) do(t *testing.T, method, path, token string, body any) (*http.Response, map[string]any) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(payload)
	} else {
		reader = bytes.NewReader(nil)
	}

	req, err := http.NewRequest(method, h.server.URL+path, reader)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := h.server.Client().Do(req)
	if err != nil {
		t.Fatalf("request %s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	decoded := map[string]any{}
	if resp.StatusCode != http.StatusNoContent {
		_ = json.NewDecoder(resp.Body).Decode(&decoded)
	}
	return resp, decoded
}

// login authenticates a fixture user and returns the token pair.
func (h *testHarness) login(t *testing.T, email, password string) (access, refresh string) {
	t.Helper()
	resp, body := h.do(t, http.MethodPost, "/api/v1/auth/login", "", models.LoginRequest{
		Email: email, Password: password,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login %s: status %d body %v", email, resp.StatusCode, body)
	}
	data, ok := body["data"].(map[string]any)
	if !ok {
		t.Fatalf("login %s: unexpected envelope %v", email, body)
	}
	access, _ = data["access_token"].(string)
	refresh, _ = data["refresh_token"].(string)
	if access == "" || refresh == "" {
		t.Fatalf("login %s: missing tokens %v", email, data)
	}
	return access, refresh
}

// errorCode extracts `error_code` from an error envelope.
func errorCode(t *testing.T, body map[string]any) string {
	t.Helper()
	code, _ := body["error_code"].(string)
	return code
}
