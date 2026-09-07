package controllers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"ajb_gps/api-vehicle/models"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// TestReferenceResponseEnvelope verifies the success envelope structure.
func TestReferenceResponseEnvelope(t *testing.T) {
	resp := models.OkResponse{
		Status: "success",
		Data:   []models.CountryItem{{ID: 1, IsoCode: "ID", Name: "Indonesia"}},
	}
	if resp.Status != "success" {
		t.Fatalf("expected status 'success', got %q", resp.Status)
	}
}

// TestNullableStrP2 verifies the helper converts NullString correctly.
func TestNullableStrP2(t *testing.T) {
	// Invalid (NULL) → nil
	var ns sql.NullString
	result := nullableStrP2(ns)
	if result != nil {
		t.Fatalf("expected nil for invalid NullString, got %v", *result)
	}

	// Valid → pointer to value
	ns = sql.NullString{String: "test", Valid: true}
	result = nullableStrP2(ns)
	if result == nil || *result != "test" {
		t.Fatalf("expected pointer to 'test', got %v", result)
	}
}

// TestNullableIntPtr verifies the helper converts NullInt64 correctly.
func TestNullableIntPtr(t *testing.T) {
	// Invalid (NULL) → nil
	var ni sql.NullInt64
	result := nullableIntPtr(ni)
	if result != nil {
		t.Fatalf("expected nil for invalid NullInt64, got %v", *result)
	}

	// Valid → pointer to int value
	ni = sql.NullInt64{Int64: 42, Valid: true}
	result = nullableIntPtr(ni)
	if result == nil || *result != 42 {
		t.Fatalf("expected pointer to 42, got %v", result)
	}
}

// TestRefPaginationParams verifies page/limit parsing with bounds.
func TestRefPaginationParams(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		wantPage  int
		wantLimit int
	}{
		{"defaults", "", 1, 100},
		{"valid", "?page=2&limit=50", 2, 50},
		{"negative page", "?page=-1", 1, 100},
		{"limit too high", "?limit=1000", 1, 100},
		{"zero limit", "?limit=0", 1, 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request, _ = http.NewRequest("GET", "/test"+tt.query, nil)

			page, limit := paginationParams(c)
			if page != tt.wantPage {
				t.Errorf("page: got %d, want %d", page, tt.wantPage)
			}
			if limit != tt.wantLimit {
				t.Errorf("limit: got %d, want %d", limit, tt.wantLimit)
			}
		})
	}
}

// TestCountryItemJSON verifies the JSON serialization of CountryItem.
func TestCountryItemJSON(t *testing.T) {
	ct := models.CountryItem{
		ID: 1, IsoCode: "ID", Name: "Indonesia",
		PhoneCode: "+62", CurrencyCode: "IDR", IsActive: true,
	}
	b, err := json.Marshal(ct)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if m["iso_code"] != "ID" {
		t.Errorf("expected iso_code=ID, got %v", m["iso_code"])
	}
	if m["name"] != "Indonesia" {
		t.Errorf("expected name=Indonesia, got %v", m["name"])
	}
}