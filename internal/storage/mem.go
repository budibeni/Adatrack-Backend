package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

// MemStore is an in-memory Store implementation used by unit tests and as a
// deterministic dev fallback when no object store is configured. It is NOT
// meant for production (no durability / sharing across replicas).
type MemStore struct {
	mu      sync.RWMutex
	buckets map[string]map[string]memObject // bucket -> key -> object
}

type memObject struct {
	data []byte
	// mime is retained for introspection/tests.
	mime string
}

// NewMemStore constructs an empty in-memory store with the given buckets.
func NewMemStore(buckets ...string) *MemStore {
	m := &MemStore{buckets: map[string]map[string]memObject{}}
	for _, b := range buckets {
		m.buckets[b] = map[string]memObject{}
	}
	return m
}

// ensureBucket lazily creates bucket storage slots.
func (m *MemStore) ensureBucket(bucket string) {
	if _, ok := m.buckets[bucket]; !ok {
		m.buckets[bucket] = map[string]memObject{}
	}
}

func (m *MemStore) PutObject(_ context.Context, bucket, key string, r io.Reader, size int64, contentType string) (int64, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return 0, fmt.Errorf("storage(mem): read: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureBucket(bucket)
	m.buckets[bucket][key] = memObject{data: data, mime: contentType}
	if size >= 0 {
		return size, nil
	}
	return int64(len(data)), nil
}

func (m *MemStore) PresignGet(_ context.Context, bucket, key string, ttl time.Duration) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, ok := m.buckets[bucket]; !ok {
		return "", ErrBucketNotFound
	}
	if _, ok := m.buckets[bucket][key]; !ok {
		return "", ErrObjectNotFound
	}
	// Deterministic pseudo-URL for tests (no real signing on an in-memory store).
	return fmt.Sprintf("mem://%s/%s?ttl=%ds", bucket, key, int(ttl.Seconds())), nil
}

func (m *MemStore) PresignPut(_ context.Context, bucket, key string, ttl time.Duration) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, ok := m.buckets[bucket]; !ok {
		// Allow future put to create the bucket lazily.
		m.ensureBucket(bucket)
	}
	return fmt.Sprintf("mem://%s/%s?ttl=%ds", bucket, key, int(ttl.Seconds())), nil
}

func (m *MemStore) Delete(_ context.Context, bucket, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.buckets[bucket]; !ok {
		return nil
	}
	delete(m.buckets[bucket], key)
	return nil
}

// Stat returns the stored object size.
func (m *MemStore) Stat(_ context.Context, bucket, key string) (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, ok := m.buckets[bucket]; !ok {
		return 0, ErrBucketNotFound
	}
	o, ok := m.buckets[bucket][key]
	if !ok {
		return 0, ErrObjectNotFound
	}
	return int64(len(o.data)), nil
}

func (m *MemStore) List(_ context.Context, bucket, prefix string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, ok := m.buckets[bucket]; !ok {
		return nil, ErrBucketNotFound
	}
	out := make([]string, 0, 8)
	for k := range m.buckets[bucket] {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (m *MemStore) Ping(_ context.Context) error { return nil }

func (m *MemStore) Buckets() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, 0, len(m.buckets))
	for b := range m.buckets {
		out = append(out, b)
	}
	sort.Strings(out)
	return out
}

// Get returns a copy of an object's bytes for assertions in tests.
func (m *MemStore) Get(bucket, key string) ([]byte, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	o, ok := m.buckets[bucket][key]
	return bytes.Clone(o.data), ok
}