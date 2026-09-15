package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// session is one authenticated principal (tenant admin, driver, platform admin).
type session struct {
	Email        string
	AccessToken  string
	RefreshToken string
	Role         string
	CompanyCode  string
	UserID       int64
}

// httpClient is a thin JSON client for the REST API.
type httpClient struct {
	base   string
	client *http.Client
}

// newHTTPClient builds the REST client.
func newHTTPClient(opt options) *httpClient {
	return &httpClient{
		base: strings.TrimRight(opt.baseURL, "/"),
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// response is one decoded API answer.
type response struct {
	Status     int
	Body       map[string]any
	RawBody    string
	Headers    http.Header
	RequestErr error
}

// data returns the `data` object of a success envelope.
func (r response) data() map[string]any {
	if obj, ok := r.Body["data"].(map[string]any); ok {
		return obj
	}
	return map[string]any{}
}

// dataList returns the `data` array of a success envelope.
func (r response) dataList() []any {
	if list, ok := r.Body["data"].([]any); ok {
		return list
	}
	return nil
}

// errorCode returns `error_code` of an error envelope.
func (r response) errorCode() string {
	code, _ := r.Body["error_code"].(string)
	return code
}

// pagination returns the pagination block.
func (r response) pagination() map[string]any {
	if obj, ok := r.Body["pagination"].(map[string]any); ok {
		return obj
	}
	return nil
}

// request performs one JSON request; `token` may be empty.
func (c *httpClient) request(ctx context.Context, method, path, token string, body any) response {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return response{RequestErr: err}
		}
		reader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return response{RequestErr: err}
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return response{RequestErr: err}
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return response{Status: resp.StatusCode, RequestErr: err}
	}
	decoded := map[string]any{}
	if len(bytes.TrimSpace(raw)) > 0 {
		_ = json.Unmarshal(raw, &decoded)
	}
	return response{Status: resp.StatusCode, Body: decoded, RawBody: string(raw), Headers: resp.Header}
}

// login authenticates a principal and returns its session.
func (c *httpClient) login(ctx context.Context, email, password string) (*session, response) {
	resp := c.request(ctx, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"email": email, "password": password,
	})
	if resp.RequestErr != nil {
		return nil, resp
	}
	data := resp.data()
	access, _ := data["access_token"].(string)
	refresh, _ := data["refresh_token"].(string)
	user, _ := data["user"].(map[string]any)
	sess := &session{Email: email, AccessToken: access, RefreshToken: refresh}
	if user != nil {
		sess.Role, _ = user["role"].(string)
		sess.CompanyCode, _ = user["company_code"].(string)
		if id, ok := user["id"].(float64); ok {
			sess.UserID = int64(id)
		}
	}
	return sess, resp
}

// get performs an authenticated GET.
func (c *httpClient) get(ctx context.Context, path, token string) response {
	return c.request(ctx, http.MethodGet, path, token, nil)
}

// statusDetail renders a compact response summary for the report.
func statusDetail(r response) string {
	return fmt.Sprintf("status=%d body=%s", r.Status, trim(r.RawBody, 160))
}

// trim shortens a string for the report output.
func trim(s string, n int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
