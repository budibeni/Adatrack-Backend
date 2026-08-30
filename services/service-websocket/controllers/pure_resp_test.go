package controllers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"ajb_gps/service-websocket/models"

	"github.com/gin-gonic/gin"
)

// setupGinCtx membuat gin context + recorder; balikan recorder untuk membaca
// body response.
func setupGinCtx(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	return c, w
}

// ---------------------------------------------------------------------
// writeSuccess / writeSuccessWithTotal — envelope seragam (GAP #3).
// ---------------------------------------------------------------------

func TestWriteSuccessEnvelope(t *testing.T) {
	c, w := setupGinCtx(t)
	writeSuccess(c, http.StatusOK, map[string]interface{}{"k": "v"})

	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "success" {
		t.Errorf("status = %v, want success", body["status"])
	}
	if body["pagination"] != nil {
		t.Errorf("tanpa pagination harus nil, dapat %v", body["pagination"])
	}
	if w.Header().Get("Content-Type") == "" {
		t.Error("content-type harus di-set")
	}
}

func TestWriteSuccessWithPagination(t *testing.T) {
	c, w := setupGinCtx(t)
	page := &models.PaginationInfo{Page: 2, Limit: 25, Total: 100}
	writeSuccess(c, http.StatusOK, []int{1}, page)

	var body map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	p, ok := body["pagination"].(map[string]interface{})
	if !ok {
		t.Fatalf("pagination harus objek, dapat %#v", body["pagination"])
	}
	if p["page"] != float64(2) {
		t.Errorf("pagination.page = %v, want 2", p["page"])
	}
	if p["total"] != float64(100) {
		t.Errorf("pagination.total = %v, want 100", p["total"])
	}
}

func TestWriteSuccessWithTotal(t *testing.T) {
	c, w := setupGinCtx(t)
	writeSuccessWithTotal(c, http.StatusOK, []int{1}, 42)

	var body map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["total_records"] != float64(42) {
		t.Errorf("total_records = %v, want 42", body["total_records"])
	}
}

// ---------------------------------------------------------------------
// registryKey — scope per-tenant.
// ---------------------------------------------------------------------

func TestRegistryKey(t *testing.T) {
	if got := registryKey("DEV001", "123"); got != "DEV001:123" {
		t.Errorf("registryKey = %q", got)
	}
	if got := registryKey("ADATrack", ""); got != "ADATrack:" {
		t.Errorf("registryKey empty imei = %q", got)
	}
}

// ---------------------------------------------------------------------
// mustJSON — JSON-safe + fallback error.
// ---------------------------------------------------------------------

func TestMustJSONMarshalCapable(t *testing.T) {
	b := mustJSON(map[string]interface{}{"event": "EVT"})
	if !json.Valid(b) {
		t.Fatalf("mustJSON menghasilkan json tak valid: %s", b)
	}
	var m map[string]interface{}
	_ = json.Unmarshal(b, &m)
	if m["event"] != "EVT" {
		t.Errorf("event = %v", m["event"])
	}
}

func TestMustJSONFallbackOnError(t *testing.T) {
	// channel tak bisa di-marshal → fallback JSON.
	b := mustJSON(make(chan int))
	if !json.Valid(b) {
		t.Fatalf("fallback harus JSON valid: %s", b)
	}
}