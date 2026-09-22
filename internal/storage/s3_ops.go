package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// maxGetBytes caps Get: the endpoint is used for verification/small objects,
// while large clips are streamed by the client through a presigned URL. A larger
// object is reported as an error instead of being buffered in memory (FR-4.4).
const maxGetBytes = 64 << 20

// Put uploads one object byte-exactly (FR-8.2).
func (s *S3Store) Put(ctx context.Context, key string, data []byte, contentType string) (Object, error) {
	hash := sha256Hex(data)
	req, err := s.newRequest(ctx, http.MethodPut, key, bytes.NewReader(data), contentType)
	if err != nil {
		return Object{}, err
	}
	req.ContentLength = int64(len(data))
	s.signV4(req, hash)

	resp, err := s.do(req)
	if err != nil {
		return Object{}, err
	}
	defer drain(resp)

	if resp.StatusCode != http.StatusOK {
		return Object{}, fmt.Errorf("storage: put %s: %w", key, statusError(resp))
	}
	return Object{Key: key, Size: int64(len(data)), ETag: trimETag(resp.Header.Get("ETag"))}, nil
}

// Head returns the metadata of one object (ErrNotFound when absent).
func (s *S3Store) Head(ctx context.Context, key string) (Object, error) {
	req, err := s.newRequest(ctx, http.MethodHead, key, nil, "")
	if err != nil {
		return Object{}, err
	}
	s.signV4(req, emptyPayloadHash)

	resp, err := s.do(req)
	if err != nil {
		return Object{}, err
	}
	defer drain(resp)

	if resp.StatusCode == http.StatusNotFound {
		return Object{}, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return Object{}, fmt.Errorf("storage: head %s: %w", key, statusError(resp))
	}
	size, _ := strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64)
	return Object{Key: key, Size: size, ETag: trimETag(resp.Header.Get("ETag"))}, nil
}

// Get downloads one object (bounded by maxGetBytes).
func (s *S3Store) Get(ctx context.Context, key string) ([]byte, error) {
	req, err := s.newRequest(ctx, http.MethodGet, key, nil, "")
	if err != nil {
		return nil, err
	}
	s.signV4(req, emptyPayloadHash)

	resp, err := s.do(req)
	if err != nil {
		return nil, err
	}
	defer drain(resp)

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("storage: get %s: %w", key, statusError(resp))
	}
	limited := io.LimitReader(resp.Body, maxGetBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("storage: read %s: %w", key, err)
	}
	if int64(len(data)) > maxGetBytes {
		return nil, fmt.Errorf("storage: object %s exceeds the %d byte in-process read limit", key, maxGetBytes)
	}
	return data, nil
}

// Delete removes one object (idempotent: a missing object is not an error).
func (s *S3Store) Delete(ctx context.Context, key string) error {
	req, err := s.newRequest(ctx, http.MethodDelete, key, nil, "")
	if err != nil {
		return err
	}
	s.signV4(req, emptyPayloadHash)

	resp, err := s.do(req)
	if err != nil {
		return err
	}
	defer drain(resp)
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("storage: delete %s: %w", key, statusError(resp))
	}
	return nil
}

// PresignGet issues a time-limited GET URL. Existence is verified with a HEAD so
// the caller (and the client) never receives a URL for a missing object — the
// same contract as the in-memory store (FR-8.4).
func (s *S3Store) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if _, err := s.Head(ctx, key); err != nil {
		return "", err
	}
	return s.presignGet(key, ttl)
}

// Health verifies bucket reachability (FR-8.8 /healthz).
func (s *S3Store) Health(ctx context.Context) error {
	req, err := s.newRequest(ctx, http.MethodHead, "", nil, "")
	if err != nil {
		return err
	}
	s.signV4(req, emptyPayloadHash)

	resp, err := s.do(req)
	if err != nil {
		return err
	}
	defer drain(resp)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("storage: health %s: %w", s.cfg.Bucket, statusError(resp))
}

// newRequest builds a signed-candidate request for one object (empty key = bucket).
func (s *S3Store) newRequest(ctx context.Context, method, key string, body io.Reader, contentType string) (*http.Request, error) {
	url := s.objectURL(key)
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("storage: build %s %s: %w", method, key, err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return req, nil
}

// do executes the request, mapping transport failures onto ErrUnavailable.
func (s *S3Store) do(req *http.Request) (*http.Response, error) {
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return resp, nil
}

// statusError renders a non-2xx response as an error carrying the S3 code.
func statusError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	detail := strings.TrimSpace(string(body))
	if detail == "" {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return fmt.Errorf("status %d: %s", resp.StatusCode, detail)
}

// drain closes the body (a HEAD response has none).
func drain(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()
}

// trimETag removes the quotes S3 puts around an ETag.
func trimETag(raw string) string { return strings.Trim(raw, `"`) }

// EnsureBucket creates the configured bucket when it is missing (idempotent) —
// the B5b task "MinIO/S3 (bucket + policy)" so a fresh dev stack needs no manual
// `mc mb` and a Coolify deploy converges by itself. A bucket owned by another
// account (403 BucketAlreadyExists) is an error: the credentials are wrong.
func (s *S3Store) EnsureBucket(ctx context.Context) error {
	req, err := s.newRequest(ctx, http.MethodPut, "", nil, "")
	if err != nil {
		return err
	}
	s.signV4(req, emptyPayloadHash)

	resp, err := s.do(req)
	if err != nil {
		return err
	}
	defer drain(resp)

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusNoContent:
		return nil
	case http.StatusConflict, http.StatusBadRequest:
		// Already owned by these credentials (MinIO/S3 answer 409 or 400 with
		// BucketAlreadyOwnedByYou) — the bucket is usable, nothing to do.
		return nil
	default:
		return fmt.Errorf("storage: ensure bucket %s: %w", s.cfg.Bucket, statusError(resp))
	}
}

// IsNotFound reports whether err is a missing object (used by handlers).
func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }

// IsUnavailable reports whether err is a backend outage (maps to HTTP 503).
func IsUnavailable(err error) bool { return errors.Is(err, ErrUnavailable) }
