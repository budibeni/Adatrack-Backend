package controllers

// http_test.go — request-parsing helpers of the PRD §8.1/§8.5 contract:
// `parsePagination`, `parseIncludeDeleted`, `pathID`, `bindJSON` and the
// validation-message renderer. These are the single funnel every handler uses,
// so their edge cases (bad page values, oversized pages, malformed bodies,
// per-tag messages) are asserted here once instead of per endpoint.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
)

// httpTestService builds a Service with an explicit page-size policy so the
// parsePagination default/max behaviour can be exercised in isolation.
func httpTestService(defaultPage, maxPage int) *Service {
	settings := testSettings()
	settings.DefaultPageSize = defaultPage
	settings.MaxPageSize = maxPage
	return NewService(Deps{Settings: settings, Store: newFakeStore()})
}

// ---------------------------------------------------------------------------
// parsePagination (PRD §8.5 rule 4)
// ---------------------------------------------------------------------------

// TestParsePaginationBranches: defaults, explicit values, the max-page cap and
// both rejection paths (non-integer / < 1) are all rejected client-side.
func TestParsePaginationBranches(t *testing.T) {
	svc := httpTestService(25, 100)

	cases := []struct {
		name      string
		target    string
		wantPage  int
		wantLimit int
		wantField string
	}{
		{name: "absent uses the configured default", target: "/api/v1/vehicles", wantPage: 1, wantLimit: 25},
		{name: "explicit page and limit", target: "/api/v1/vehicles?page=3&limit=10", wantPage: 3, wantLimit: 10},
		{name: "limit equals the maximum", target: "/api/v1/vehicles?limit=100", wantPage: 1, wantLimit: 100},
		{name: "blank values fall back", target: "/api/v1/vehicles?page=&limit=", wantPage: 1, wantLimit: 25},
		{name: "page is not an integer", target: "/api/v1/vehicles?page=abc", wantField: "page"},
		{name: "page below one", target: "/api/v1/vehicles?page=0", wantField: "page"},
		{name: "negative page", target: "/api/v1/vehicles?page=-4", wantField: "page"},
		{name: "limit is not an integer", target: "/api/v1/vehicles?limit=abc", wantField: "limit"},
		{name: "limit below one", target: "/api/v1/vehicles?limit=0", wantField: "limit"},
		{name: "limit above the maximum", target: "/api/v1/vehicles?limit=101", wantField: "limit"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := testContext(http.MethodGet, tc.target, "", adminIdentity())
			page, limit, apiErr := svc.parsePagination(c)

			if tc.wantField == "" {
				if apiErr != nil {
					t.Fatalf("parsePagination(%s) failed: %s", tc.target, apiErr.Message)
				}
				if page != tc.wantPage || limit != tc.wantLimit {
					t.Fatalf("got (page=%d, limit=%d), want (%d, %d)", page, limit, tc.wantPage, tc.wantLimit)
				}
				return
			}

			if apiErr == nil {
				t.Fatalf("parsePagination(%s) accepted an invalid value", tc.target)
			}
			if apiErr.Status != http.StatusBadRequest || apiErr.Code != CodeValidationError {
				t.Errorf("got %d %s, want 400 %s", apiErr.Status, apiErr.Code, CodeValidationError)
			}
			if _, ok := apiErr.Fields[tc.wantField]; !ok {
				t.Errorf("error fields %v do not name %q", apiErr.Fields, tc.wantField)
			}
			if page != 0 || limit != 0 {
				t.Errorf("a rejected page must not leak values: (page=%d, limit=%d)", page, limit)
			}
			// The helper itself never writes a response — the handler does.
			if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
				t.Errorf("parsePagination must not touch the response (code=%d)", rec.Code)
			}
		})
	}
}

// TestParsePaginationDefaultsWhenUnset: an unset/zero-default page size must not
// produce `limit=0` (which would make every list query empty).
func TestParsePaginationDefaultsWhenUnset(t *testing.T) {
	for _, defaultPage := range []int{0, -5} {
		svc := httpTestService(defaultPage, 0)
		c, _ := testContext(http.MethodGet, "/api/v1/vehicles", "", adminIdentity())

		page, limit, apiErr := svc.parsePagination(c)
		if apiErr != nil {
			t.Fatalf("default page size %d: unexpected error %s", defaultPage, apiErr.Message)
		}
		if page != 1 || limit != 100 {
			t.Errorf("default page size %d: got (page=%d, limit=%d), want (1, 100)", defaultPage, page, limit)
		}
	}

	// MaxPageSize <= 0 disables the cap rather than rejecting every value.
	uncapped := httpTestService(25, 0)
	c, _ := testContext(http.MethodGet, "/api/v1/vehicles?limit=5000", "", adminIdentity())
	_, limit, apiErr := uncapped.parsePagination(c)
	if apiErr != nil {
		t.Fatalf("uncapped service rejected limit=5000: %s", apiErr.Message)
	}
	if limit != 5000 {
		t.Errorf("uncapped limit = %d, want 5000", limit)
	}
}

// ---------------------------------------------------------------------------
// parseIncludeDeleted (PRD §6.0.1)
// ---------------------------------------------------------------------------

// TestParseIncludeDeletedBranches: only absent/blank/parseable booleans are
// accepted; everything else is the documented VALIDATION_ERROR.
func TestParseIncludeDeletedBranches(t *testing.T) {
	cases := []struct {
		name    string
		query   string
		want    bool
		wantErr bool
	}{
		{name: "absent", query: "", want: false},
		{name: "blank", query: "?include_deleted=", want: false},
		{name: "true", query: "?include_deleted=true", want: true},
		{name: "uppercase true", query: "?include_deleted=TRUE", want: true},
		{name: "one", query: "?include_deleted=1", want: true},
		{name: "false", query: "?include_deleted=false", want: false},
		{name: "zero", query: "?include_deleted=0", want: false},
		{name: "word yes", query: "?include_deleted=yes", wantErr: true},
		{name: "word maybe", query: "?include_deleted=maybe", wantErr: true},
		{name: "number two", query: "?include_deleted=2", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := testContext(http.MethodGet, "/api/v1/vehicles"+tc.query, "", adminIdentity())
			got, apiErr := parseIncludeDeleted(c)

			if tc.wantErr {
				if apiErr == nil {
					t.Fatalf("query %q accepted", tc.query)
				}
				if apiErr.Code != CodeValidationError {
					t.Errorf("got %s, want %s", apiErr.Code, CodeValidationError)
				}
				if _, ok := apiErr.Fields["include_deleted"]; !ok {
					t.Errorf("error fields %v do not name include_deleted", apiErr.Fields)
				}
				return
			}
			if apiErr != nil {
				t.Fatalf("query %q rejected: %s", tc.query, apiErr.Message)
			}
			if got != tc.want {
				t.Errorf("query %q = %v, want %v", tc.query, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// pathID / parsePositiveInt
// ---------------------------------------------------------------------------

// TestPathIDBranches: the path identifier must be a strictly positive integer,
// so negative/zero/non-numeric ids never reach the store.
func TestPathIDBranches(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    int64
		wantErr bool
	}{
		{name: "small id", raw: "1", want: 1},
		{name: "max int64", raw: "9223372036854775807", want: 9223372036854775807},
		{name: "zero", raw: "0", wantErr: true},
		{name: "negative", raw: "-3", wantErr: true},
		{name: "word", raw: "abc", wantErr: true},
		{name: "empty", raw: "", wantErr: true},
		{name: "decimal", raw: "1.5", wantErr: true},
		{name: "overflow", raw: "99999999999999999999", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := testContext(http.MethodGet, "/api/v1/vehicles/"+tc.raw, "", adminIdentity())
			c.Params = gin.Params{{Key: "id", Value: tc.raw}}

			id, apiErr := pathID(c)
			got, rawErr := parsePositiveInt(tc.raw)

			if tc.wantErr {
				if apiErr == nil {
					t.Fatalf("pathID(%q) accepted", tc.raw)
				}
				if apiErr.Code != CodeValidationError {
					t.Errorf("got %s, want %s", apiErr.Code, CodeValidationError)
				}
				if _, ok := apiErr.Fields["id"]; !ok {
					t.Errorf("error fields %v do not name id", apiErr.Fields)
				}
				if rawErr == nil {
					t.Errorf("parsePositiveInt(%q) accepted", tc.raw)
				}
				return
			}
			if apiErr != nil {
				t.Fatalf("pathID(%q) rejected: %s", tc.raw, apiErr.Message)
			}
			if id != tc.want || got != tc.want {
				t.Errorf("pathID(%q)=%d, parsePositiveInt(%q)=%d, want %d",
					tc.raw, id, tc.raw, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// bindJSON (PRD §8.1/§8.5 rule 1)
// ---------------------------------------------------------------------------

// bindTagsBody mirrors the validator tags validationMessage() renders. Fields
// carry `omitempty` so a table case can trip exactly one tag at a time; every
// body below therefore keeps `required` non-empty.
type bindTagsBody struct {
	Required string `json:"required" binding:"required"`
	Email    string `json:"email" binding:"omitempty,email"`
	Min      int    `json:"min" binding:"omitempty,min=5"`
	Max      int    `json:"max" binding:"omitempty,max=10"`
	OneOf    string `json:"oneof" binding:"omitempty,oneof=low high"`
	Gte      int    `json:"gte" binding:"omitempty,gte=2"`
	Lte      int    `json:"lte" binding:"omitempty,lte=7"`
	Other    string `json:"other" binding:"omitempty,ipv4"`
}

// TestBindJSONValidationMessages: every tag reachable through real binding is
// rendered with its parameter, and the offending field is named in lowercase
// (the client-facing key, PRD §8.1).
func TestBindJSONValidationMessages(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		wantField  string
		wantPhrase string
	}{
		{name: "required", body: `{"required":""}`, wantField: "required", wantPhrase: "is required"},
		{name: "omitted required", body: `{}`, wantField: "required", wantPhrase: "is required"},
		{name: "email", body: `{"required":"ok","email":"not-an-email"}`, wantField: "email", wantPhrase: "must be a valid email"},
		{name: "min", body: `{"required":"ok","min":1}`, wantField: "min", wantPhrase: "must be at least 5"},
		{name: "max", body: `{"required":"ok","max":99}`, wantField: "max", wantPhrase: "must be at most 10"},
		{name: "oneof", body: `{"required":"ok","oneof":"extreme"}`, wantField: "oneof", wantPhrase: "must be one of: low high"},
		{name: "gte", body: `{"required":"ok","gte":1}`, wantField: "gte", wantPhrase: "must be greater than or equal to 2"},
		{name: "lte", body: `{"required":"ok","lte":8}`, wantField: "lte", wantPhrase: "must be less than or equal to 7"},
		{name: "unknown tag falls back", body: `{"required":"ok","other":"999.1.1.1"}`, wantField: "other", wantPhrase: "is invalid"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := testContext(http.MethodPost, "/api/v1/vehicles", tc.body, adminIdentity())
			var dst bindTagsBody

			apiErr := bindJSON(c, &dst)
			if apiErr == nil {
				t.Fatalf("body %s was accepted", tc.body)
			}
			if apiErr.Status != http.StatusBadRequest || apiErr.Code != CodeValidationError {
				t.Errorf("got %d %s, want 400 %s", apiErr.Status, apiErr.Code, CodeValidationError)
			}
			got, ok := apiErr.Fields[tc.wantField]
			if !ok {
				t.Fatalf("error fields %v do not name %q", apiErr.Fields, tc.wantField)
			}
			if !strings.Contains(got, tc.wantPhrase) {
				t.Errorf("fields[%s] = %q, want it to contain %q", tc.wantField, got, tc.wantPhrase)
			}
			if rec.Body.Len() != 0 {
				t.Error("bindJSON must not write the response itself")
			}
		})
	}
}

// TestBindJSONAcceptsValidBody: a conforming body binds with no error and
// populates the destination.
func TestBindJSONAcceptsValidBody(t *testing.T) {
	c, _ := testContext(http.MethodPost, "/api/v1/vehicles",
		`{"required":"ok","email":"user@example.com","min":9,"max":3,"oneof":"high","gte":5,"lte":1,"other":"10.0.0.1"}`,
		adminIdentity())
	var dst bindTagsBody

	if apiErr := bindJSON(c, &dst); apiErr != nil {
		t.Fatalf("valid body rejected: %s (%v)", apiErr.Message, apiErr.Fields)
	}
	if dst.Required != "ok" || dst.Email != "user@example.com" || dst.OneOf != "high" {
		t.Errorf("body not bound: %+v", dst)
	}
}

// TestBindJSONMalformedBody: syntactically invalid JSON is reported against the
// synthetic `body` key instead of leaking the decoder error.
func TestBindJSONMalformedBody(t *testing.T) {
	for _, body := range []string{`{"required":`, `not-json`, `[1,2,3]`} {
		c, _ := testContext(http.MethodPost, "/api/v1/vehicles", body, adminIdentity())
		var dst bindTagsBody

		apiErr := bindJSON(c, &dst)
		if apiErr == nil {
			t.Fatalf("body %q was accepted", body)
		}
		if apiErr.Code != CodeValidationError {
			t.Errorf("body %q: got %s, want %s", body, apiErr.Code, CodeValidationError)
		}
		if got := apiErr.Fields["body"]; got != "malformed JSON body" {
			t.Errorf("body %q: fields[body] = %q, want %q", body, got, "malformed JSON body")
		}
	}
}

// errBody is a request body whose Read always fails (MaxBytesError simulation).
type errBody struct{ err error }

func (e errBody) Read([]byte) (int, error) { return 0, e.err }
func (errBody) Close() error               { return nil }

// TestBindJSONBodyTooLarge: a body rejected by the byte limit is reported as a
// dedicated 400 instead of a generic binding failure (PRD §9.5).
func TestBindJSONBodyTooLarge(t *testing.T) {
	c, _ := testContext(http.MethodPost, "/api/v1/vehicles", "", adminIdentity())
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Body = errBody{err: &http.MaxBytesError{Limit: 1}}
	var dst bindTagsBody

	apiErr := bindJSON(c, &dst)
	if apiErr == nil {
		t.Fatal("oversized body was accepted")
	}
	if apiErr.Status != http.StatusBadRequest || apiErr.Code != CodeValidationError {
		t.Errorf("got %d %s, want 400 %s", apiErr.Status, apiErr.Code, CodeValidationError)
	}
	if got := apiErr.Fields["body"]; got != "too large" {
		t.Errorf("fields[body] = %q, want %q", got, "too large")
	}
}

// stubFieldError drives validationMessage() with tags real binding cannot
// produce: `dive` is always reported as the offending element's own tag, so the
// defensive branch is asserted directly.
type stubFieldError struct {
	validator.FieldError
	tag   string
	param string
}

func (s stubFieldError) Tag() string   { return s.tag }
func (s stubFieldError) Param() string { return s.param }

// TestValidationMessageDiveAndFallback: the element-tag branch and the unknown
// tag fallback both render a client-safe message.
func TestValidationMessageDiveAndFallback(t *testing.T) {
	if got := validationMessage(stubFieldError{tag: "dive"}); got != "contains an invalid element" {
		t.Errorf("dive = %q, want %q", got, "contains an invalid element")
	}
	if got := validationMessage(stubFieldError{tag: "uuid4"}); got != "is invalid" {
		t.Errorf("unknown tag = %q, want %q", got, "is invalid")
	}
}
