// Package storage provides the object-storage abstraction of B5b (PRD Module
// 8): an S3-compatible client (MinIO dev / S3-OSS prod) implemented with the
// standard library only, plus an in-memory store for dev/tests that keeps the
// byte-exact round-trip contract of the presigned GET acceptance.
package storage

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound reports a missing object (maps to HTTP 404 upstream).
var ErrNotFound = errors.New("storage: object not found")

// Object is the metadata returned by a successful Put.
type Object struct {
	Key  string
	Size int64
	ETag string
}

// Store is the object-storage surface used by service-media (FR-8.1/8.5):
// upload → catalog → presigned GET → retention deletion.
type Store interface {
	// Put stores one object byte-exactly (content type preserved).
	Put(ctx context.Context, key string, data []byte, contentType string) (Object, error)
	// Head returns the metadata of one object; storage.ErrNotFound when absent.
	Head(ctx context.Context, key string) (Object, error)
	// Get returns the stored bytes; storage.ErrNotFound when absent.
	Get(ctx context.Context, key string) ([]byte, error)
	// Delete removes one object (idempotent: a missing object is not an error).
	Delete(ctx context.Context, key string) error
	// PresignGet issues a time-limited GET URL for one object.
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
	// PresignPut issues a time-limited PUT URL so an edge agent can upload the
	// object directly to the bucket (the FR-8.1 "JSON + presigned PUT" flow).
	PresignPut(ctx context.Context, key, contentType string, ttl time.Duration) (string, error)
	// Health verifies bucket reachability (readiness probe).
	Health(ctx context.Context) error
}
