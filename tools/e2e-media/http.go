package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

// client is a thin HTTP helper with a bound timeout.
type client struct{ hc *http.Client }

func newClient(timeout time.Duration) *client {
	return &client{hc: &http.Client{Timeout: timeout}}
}

// request performs one HTTP call and returns the status + body.
func (c *client) request(method, url string, body []byte, headers map[string]string) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		return 0, nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, data, nil
}

// login authenticates against service-websocket and returns the access token.
func (c *client) login(base, email, password string) (string, error) {
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	status, raw, err := c.request(http.MethodPost, base+"/api/v1/auth/login", body,
		map[string]string{"Content-Type": "application/json"})
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("login status %d: %s", status, trim(raw, 200))
	}
	var env struct {
		Data struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return "", fmt.Errorf("decode login: %w", err)
	}
	if env.Data.AccessToken == "" {
		return "", fmt.Errorf("login returned no access token: %s", trim(raw, 200))
	}
	return env.Data.AccessToken, nil
}

// bearer builds the Authorization header.
func bearer(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

// withHeader adds one header to a copy of base.
func withHeader(base map[string]string, key, value string) map[string]string {
	out := map[string]string{}
	for k, v := range base {
		out[k] = v
	}
	out[key] = value
	return out
}

// multipartBody builds a multipart/form-data body + content type for the ingest.
func multipartBody(fields map[string]string, filename, contentType string, data []byte) ([]byte, string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for name, value := range fields {
		if err := w.WriteField(name, value); err != nil {
			return nil, "", err
		}
	}
	hdr := make(map[string][]string)
	hdr["Content-Disposition"] = []string{`form-data; name="file"; filename="` + filename + `"`}
	hdr["Content-Type"] = []string{contentType}
	part, err := w.CreatePart(hdr)
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write(data); err != nil {
		return nil, "", err
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), w.FormDataContentType(), nil
}

// decodeError returns the PRD §8.1 error_code of a failed response.
func decodeError(raw []byte) string {
	var env errorEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return ""
	}
	return env.ErrorCode
}

// decodeEnvelope unwraps the `data` block.
func decodeEnvelope(raw []byte, dst any) error {
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return err
	}
	if len(env.Data) == 0 {
		return fmt.Errorf("envelope carried no data: %s", trim(raw, 160))
	}
	return json.Unmarshal(env.Data, dst)
}

// trim shortens a payload for logs.
func trim(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}

// mediaURL joins the service base with a path.
func mediaURL(base, path string) string {
	return strings.TrimRight(base, "/") + path
}
