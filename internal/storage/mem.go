package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// Mem is the in-memory Store backing dev/tests: byte-exact round trips and
// symbolic presigned URLs (`mem://bucket/key`) — retrieval in dev always goes
// through the in-process Get, so the byte-persis contract is testable without
// live MinIO.
type Mem struct {
	mu      sync.RWMutex
	bucket  string
	objects map[string]memObject
}

type memObject struct {
	data        []byte
	contentType string
	etag        string
}

// NewMem builds an empty in-memory store for one bucket.
func NewMem(bucket string) *Mem {
	return &Mem{bucket: bucket, objects: map[string]memObject{}}
}

// Put stores one object (defensive byte copy).
func (m *Mem) Put(_ context.Context, key string, data []byte, contentType string) (Object, error) {
	cp := append([]byte(nil), data...)
	sum := sha256.Sum256(cp)
	obj := memObject{data: cp, contentType: contentType, etag: hex.EncodeToString(sum[:])}
	m.mu.Lock()
	m.objects[key] = obj
	m.mu.Unlock()
	return Object{Key: key, Size: int64(len(cp)), ETag: obj.etag}, nil
}

// Head returns the metadata of one object (ErrNotFound when absent).
func (m *Mem) Head(_ context.Context, key string) (Object, error) {
	m.mu.RLock()
	obj, ok := m.objects[key]
	m.mu.RUnlock()
	if !ok {
		return Object{}, ErrNotFound
	}
	return Object{Key: key, Size: int64(len(obj.data)), ETag: obj.etag}, nil
}

// Get returns a copy of the stored bytes (byte-exact round trip).
func (m *Mem) Get(_ context.Context, key string) ([]byte, error) {
	m.mu.RLock()
	obj, ok := m.objects[key]
	m.mu.RUnlock()
	if !ok {
		return nil, ErrNotFound
	}
	return append([]byte(nil), obj.data...), nil
}

// Delete removes one object (idempotent).
func (m *Mem) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	delete(m.objects, key)
	m.mu.Unlock()
	return nil
}

// PresignPut returns a symbolic upload URL for the dev/test store. Retrieval in
// dev always goes through the in-process Put, so the URL only documents the
// intent (`mem://bucket/key?upload=1&expires=<unix>`); the live MinIO/S3 path
// signs a real URL.
func (m *Mem) PresignPut(_ context.Context, key, contentType string, ttl time.Duration) (string, error) {
	if contentType == "" {
		return "", ErrNotFound
	}
	return fmt.Sprintf("mem://%s/%s?upload=1&expires=%d", m.bucket, key, time.Now().Add(ttl).Unix()), nil
}

// PresignGet returns a symbolic URL carrying the expiry (dev/test only); the
// caller resolves it via Get inside the same process.
func (m *Mem) PresignGet(_ context.Context, key string, ttl time.Duration) (string, error) {
	m.mu.RLock()
	_, ok := m.objects[key]
	m.mu.RUnlock()
	if !ok {
		return "", ErrNotFound
	}
	return fmt.Sprintf("mem://%s/%s?expires=%d", m.bucket, key, time.Now().Add(ttl).Unix()), nil
}

// Health always succeeds.
func (m *Mem) Health(_ context.Context) error { return nil }
