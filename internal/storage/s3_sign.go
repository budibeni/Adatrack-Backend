// S3-compatible object storage for B5b (PRD Module 8 / FR-8.2): an S3Store
// implemented with the STANDARD LIBRARY only (no AWS SDK), because the project
// deliberately keeps its dependency surface small and MinIO/S3 only needs
// SigV4 + four verbs (PUT/HEAD/GET/DELETE) + presigned GET. All paths are
// path-style (`{endpoint}/{bucket}/{key}`), which MinIO serves by default and
// plain S3 supports for any bucket name (no DNS/vhost constraints).
package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	sigAlgorithm = "AWS4-HMAC-SHA256"
	sigService   = "s3"
	// emptyPayloadHash is sha256("") — the payload hash of HEAD/GET/DELETE.
	emptyPayloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	// unsignedPayload marks a presigned request whose body is not part of the
	// signature (the documented SigV4 query-auth mode).
	unsignedPayload = "UNSIGNED-PAYLOAD"
	// maxPresignTTL is the SigV4 limit for X-Amz-Expires (7 days).
	maxPresignTTL = 7 * 24 * time.Hour
)

// S3Config is the resolved object-storage configuration (MEDIA_S3_*, FR-8.2).
type S3Config struct {
	Endpoint  string // http://127.0.0.1:9000 (LOCAL) / http://minio:9000 (COOLIFY)
	Region    string // us-east-1 default
	Bucket    string
	AccessKey string
	SecretKey string
	// UseSSL is kept for parity with the compose/env contract; the endpoint
	// scheme always wins (an explicit http:// endpoint is never upgraded).
	UseSSL bool
	// Timeout bounds one storage round trip (default 30 s).
	Timeout time.Duration
}

// ErrUnavailable reports a storage backend that could not be reached (maps to
// HTTP 503 upstream — never a 500, the failure is a dependency outage).
var ErrUnavailable = errors.New("storage: backend unavailable")

// S3Store talks to an S3-compatible endpoint (MinIO dev / S3-OSS prod).
type S3Store struct {
	cfg    S3Config
	base   *url.URL
	client *http.Client
	// now is injectable so signature tests are deterministic.
	now func() time.Time
}

// NewS3 validates the configuration and builds the client.
func NewS3(cfg S3Config) (*S3Store, error) {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return nil, errors.New("storage: MEDIA_S3_ENDPOINT must be set for the s3 backend")
	}
	if strings.TrimSpace(cfg.Bucket) == "" {
		return nil, errors.New("storage: MEDIA_S3_BUCKET must be set for the s3 backend")
	}
	if cfg.AccessKey == "" || cfg.SecretKey == "" {
		return nil, errors.New("storage: MEDIA_S3_ACCESS_KEY/MEDIA_S3_SECRET_KEY must be set for the s3 backend")
	}
	parsed, err := url.Parse(cfg.Endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("storage: invalid MEDIA_S3_ENDPOINT %q", cfg.Endpoint)
	}
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &S3Store{
		cfg:    cfg,
		base:   parsed,
		client: &http.Client{Timeout: timeout},
		now:    func() time.Time { return time.Now().UTC() },
	}, nil
}

// Endpoint returns the configured endpoint (diagnostics/logging).
func (s *S3Store) Endpoint() string { return s.cfg.Endpoint }

// Bucket returns the configured bucket (FR-8.8 storage_objects{bucket}).
func (s *S3Store) Bucket() string { return s.cfg.Bucket }

// objectURL builds the path-style URL of one object key.
func (s *S3Store) objectURL(key string) string {
	var b strings.Builder
	b.WriteString(s.base.Scheme)
	b.WriteString("://")
	b.WriteString(s.base.Host)
	if basePath := strings.TrimSuffix(s.base.EscapedPath(), "/"); basePath != "" {
		b.WriteString(basePath)
	}
	b.WriteString("/")
	b.WriteString(s3Escape(s.cfg.Bucket, true))
	for _, seg := range strings.Split(strings.TrimPrefix(key, "/"), "/") {
		b.WriteString("/")
		b.WriteString(s3Escape(seg, true))
	}
	return b.String()
}

// signV4 signs an outgoing request in place (header auth): host, x-amz-date,
// x-amz-content-sha256 and every header already present are covered.
func (s *S3Store) signV4(req *http.Request, payloadHash string) {
	t := s.now()
	amzDate := t.Format("20060102T150405Z")
	scope := t.Format("20060102") + "/" + s.cfg.Region + "/" + sigService + "/aws4_request"

	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)

	block, signedHeaders := canonicalHeaders(req, s.cfg.Host())
	canonicalRequest := strings.Join([]string{
		req.Method,
		s3EscapePath(req.URL.EscapedPath()),
		canonicalQuery(req.URL),
		block,
		signedHeaders,
		payloadHash,
	}, "\n")

	req.Header.Set("Authorization", sigAlgorithm+" Credential="+s.cfg.AccessKey+"/"+scope+
		", SignedHeaders="+signedHeaders+", Signature="+s.signature(scope, amzDate, canonicalRequest))
}

// presignGet builds a query-auth GET URL (X-Amz-Signature in the query string),
// which is what a browser can open without credentials (FR-8.4).
func (s *S3Store) presignGet(key string, ttl time.Duration) (string, error) {
	return s.presign(http.MethodGet, key, ttl)
}

// PresignPut signs a query-auth PUT URL so an edge agent can upload the object
// directly (FR-8.1 JSON + presigned PUT flow). Only `host` is signed, so the
// client is free to send its own Content-Type — the mime type is validated by
// the catalog before the URL is issued.
func (s *S3Store) PresignPut(_ context.Context, key, contentType string, ttl time.Duration) (string, error) {
	if contentType == "" {
		return "", fmt.Errorf("storage: presign put requires a content type")
	}
	return s.presign(http.MethodPut, key, ttl)
}

// presign builds a query-auth URL for one method (GET/PUT).
func (s *S3Store) presign(method, key string, ttl time.Duration) (string, error) {
	if ttl <= 0 || ttl > maxPresignTTL {
		ttl = maxPresignTTL
	}
	t := s.now()
	amzDate := t.Format("20060102T150405Z")
	scope := t.Format("20060102") + "/" + s.cfg.Region + "/" + sigService + "/aws4_request"

	raw, err := url.Parse(s.objectURL(key))
	if err != nil {
		return "", fmt.Errorf("storage: presign parse: %w", err)
	}
	q := raw.Query()
	q.Set("X-Amz-Algorithm", sigAlgorithm)
	q.Set("X-Amz-Credential", s.cfg.AccessKey+"/"+scope)
	q.Set("X-Amz-Date", amzDate)
	q.Set("X-Amz-Expires", fmt.Sprintf("%d", int64(ttl.Seconds())))
	q.Set("X-Amz-SignedHeaders", "host")
	raw.RawQuery = canonicalQueryValues(q)

	canonicalRequest := strings.Join([]string{
		method,
		s3EscapePath(raw.EscapedPath()),
		raw.RawQuery,
		"host:" + s.cfg.Host() + "\n",
		"host",
		unsignedPayload,
	}, "\n")
	raw.RawQuery += "&X-Amz-Signature=" + s.signature(scope, amzDate, canonicalRequest)
	return raw.String(), nil
}

// signature computes the SigV4 signature of a canonical request.
func (s *S3Store) signature(scope, amzDate, canonicalRequest string) string {
	sum := sha256.Sum256([]byte(canonicalRequest))
	stringToSign := strings.Join([]string{
		sigAlgorithm, amzDate, scope, hex.EncodeToString(sum[:]),
	}, "\n")
	key := hmacSHA256([]byte("AWS4"+s.cfg.SecretKey), scope[:8])
	key = hmacSHA256(key, s.cfg.Region)
	key = hmacSHA256(key, sigService)
	key = hmacSHA256(key, "aws4_request")
	return hex.EncodeToString(hmacSHA256(key, stringToSign))
}

// Host returns the endpoint host used in the signed `host` header.
func (c S3Config) Host() string {
	parsed, err := url.Parse(c.Endpoint)
	if err != nil || parsed.Host == "" {
		return ""
	}
	return parsed.Host
}
