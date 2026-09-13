package controllers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"ajb_gps/api-vehicle/models"

	"github.com/gin-gonic/gin"
)

func avginCtx(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	return c, w
}

// ---------------------------------------------------------------------
// writeSuccess / writeSuccessWithTotal — envelope GAP #3.
// ---------------------------------------------------------------------

func TestAVWriteSuccessEnvelope(t *testing.T) {
	c, w := avginCtx(t)
	writeSuccess(c, http.StatusOK, map[string]interface{}{"k": "v"})

	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "success" {
		t.Errorf("status = %v", body["status"])
	}
	if body["pagination"] != nil {
		t.Errorf("pagination harus nil, dapat %v", body["pagination"])
	}
}

func TestAVWriteSuccessWithPagination(t *testing.T) {
	c, w := avginCtx(t)
	page := &models.PaginationInfo{Page: 3, Limit: 10, Total: 200}
	writeSuccess(c, http.StatusOK, []int{1}, page)

	var body map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	p, ok := body["pagination"].(map[string]interface{})
	if !ok {
		t.Fatalf("pagination harus objek: %#v", body["pagination"])
	}
	if p["page"] != float64(3) || p["total"] != float64(200) {
		t.Errorf("pagination salah: %#v", p)
	}
}

func TestAVWriteSuccessWithTotal(t *testing.T) {
	c, w := avginCtx(t)
	writeSuccessWithTotal(c, http.StatusOK, []int{1}, 42)

	var body map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["total_records"] != float64(42) {
		t.Errorf("total_records = %v, want 42", body["total_records"])
	}
}

// ---------------------------------------------------------------------
// atoiDefault — default fallback + valid parse.
// ---------------------------------------------------------------------

func TestAtoiDefaultExtraEdges(t *testing.T) {
	if got := atoiDefault("", 5); got != 5 {
		t.Errorf("empty = %d, want 5", got)
	}
	if got := atoiDefault("17", 5); got != 17 {
		t.Errorf("valid = %d, want 17", got)
	}
	if got := atoiDefault("abc", 9); got != 9 {
		t.Errorf("invalid = %d, want 9", got)
	}
	if got := atoiDefault("-3", 9); got != 9 {
		t.Errorf("negatif = %d, want 9", got)
	}
}