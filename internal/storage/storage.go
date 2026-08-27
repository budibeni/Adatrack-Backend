// Package storage provides a thin, S3-compatible object-store abstraction used
// by service-media (Phase B5b, PRD v1.3.0 Module 8). It exposes a minimal
// interface (Put/PresignGet/PresignPut/Delete/List/Ping) so callers are decoupled
// from the concrete backend (MinIO dev, AWS S3 / Aliyun OSS prod), and unit tests
// can run against an in-memory or mock implementation without a live store.
package storage

import (
	"context"
	"errors"
	"io"
	"time"
)

// Object describes a stored media object.
type Object struct {
	// Key is the object key: {company}/{vehicle}/{yyyyMM}/{uuid}.
	Key string
	// Bucket is the object-store bucket.
	Bucket string
	// SizeBytes is the object size in bytes (0 if unknown).
	SizeBytes int64
	// MimeType is the content type (allowlisted by service-media ingest).
	MimeType string
}

// Store abstracts an S3-compatible object store.
type Store interface {
	// PutObject writes an object (setting content type) and returns bytes written.
	PutObject(ctx context.Context, bucket, key string, r io.Reader, size int64, contentType string) (int64, error)
	// PresignGet returns a time-limited presigned GET URL for a private object.
	PresignGet(ctx context.Context, bucket, key string, ttl time.Duration) (string, error)
	// PresignPut returns a time-limited presigned PUT URL (FR-8.1 JSON flow).
	PresignPut(ctx context.Context, bucket, key string, ttl time.Duration) (string, error)
	// Delete removes an object (idempotent).
	Delete(ctx context.Context, bucket, key string) error
	// List returns object keys under a prefix.
	List(ctx context.Context, bucket, prefix string) ([]string, error)
	// Ping reports whether the store is reachable (readiness /healthz).
	Ping(ctx context.Context) error
	// Buckets returns the configured buckets (for the storage_objects metric).
	Buckets() []string
	// Stat reports the stored object's size (or ErrObjectNotFound).
	Stat(ctx context.Context, bucket, key string) (int64, error)
}

// Well-known errors returned by implementations.
var (
	// ErrBucketNotFound is returned when an operation targets a bucket that does
	// not exist (and the implementation cannot auto-create it).
	ErrBucketNotFound = errors.New("storage: bucket not found")
	// ErrObjectNotFound is returned when a read/delete targets a missing object.
	ErrObjectNotFound = errors.New("storage: object not found")
)
