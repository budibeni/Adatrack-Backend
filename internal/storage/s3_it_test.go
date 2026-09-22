package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

// itStore builds the live MinIO store from MEDIA_S3_* (skipped unless
// ADATRACK_IT=1 — the documented gate for tests that need the dev stack).
func itStore(t *testing.T) *S3Store {
	t.Helper()
	if os.Getenv("ADATRACK_IT") != "1" {
		t.Skip("ADATRACK_IT=1 required (live MinIO/S3 backend)")
	}
	endpoint := os.Getenv("MEDIA_S3_ENDPOINT")
	bucket := os.Getenv("MEDIA_S3_BUCKET")
	access := os.Getenv("MEDIA_S3_ACCESS_KEY")
	secret := os.Getenv("MEDIA_S3_SECRET_KEY")
	if endpoint == "" || bucket == "" || access == "" || secret == "" {
		t.Skip("MEDIA_S3_ENDPOINT/BUCKET/ACCESS_KEY/SECRET_KEY required")
	}
	store, err := NewS3(S3Config{
		Endpoint: endpoint, Region: envOr("MEDIA_S3_REGION", "us-east-1"),
		Bucket: bucket, AccessKey: access, SecretKey: secret,
	})
	if err != nil {
		t.Fatalf("NewS3: %v", err)
	}
	return store
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// TestITS3EnsureBucketAndHealth is the B5b bucket task: the service must be able
// to create the bucket idempotently and report health (FR-8.8).
func TestITS3EnsureBucketAndHealth(t *testing.T) {
	store := itStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := store.EnsureBucket(ctx); err != nil {
		t.Fatalf("EnsureBucket: %v", err)
	}
	if err := store.EnsureBucket(ctx); err != nil {
		t.Fatalf("EnsureBucket must be idempotent: %v", err)
	}
	if err := store.Health(ctx); err != nil {
		t.Fatalf("Health: %v", err)
	}
}

// TestITS3RoundTripPresignedByteExact is the FR-8.5 acceptance contract against
// the real object storage: upload → HEAD → GET → presigned GET must all return
// the SAME bytes, then delete makes the object disappear.
func TestITS3RoundTripPresignedByteExact(t *testing.T) {
	store := itStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := store.EnsureBucket(ctx); err != nil {
		t.Fatalf("EnsureBucket: %v", err)
	}
	key := fmt.Sprintf("ADATRACK-IT/%d/roundtrip.jpg", time.Now().UnixNano())
	payload := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46, 0x49, 0x46, 0x00, 0x01, 0x0D, 0x0A}
	defer func() {
		if err := store.Delete(context.Background(), key); err != nil {
			t.Logf("cleanup delete: %v", err)
		}
	}()

	obj, err := store.Put(ctx, key, payload, "image/jpeg")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if obj.Size != int64(len(payload)) || obj.ETag == "" {
		t.Fatalf("Put object = %+v, want size %d + ETag", obj, len(payload))
	}

	head, err := store.Head(ctx, key)
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if head.Size != int64(len(payload)) {
		t.Errorf("Head size = %d, want %d", head.Size, len(payload))
	}

	got, err := store.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("Get round trip mismatch: got %v want %v", got, payload)
	}

	signed, err := store.PresignGet(ctx, key, 2*time.Minute)
	if err != nil {
		t.Fatalf("PresignGet: %v", err)
	}
	resp, err := http.Get(signed) //nolint:gosec // URL comes from our own presigner
	if err != nil {
		t.Fatalf("GET presigned URL: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		t.Fatalf("presigned GET status = %d (%s)", resp.StatusCode, body)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read presigned body: %v", err)
	}
	if !bytes.Equal(body, payload) {
		t.Errorf("presigned round trip mismatch: got %v want %v", body, payload)
	}

	if err := store.Delete(ctx, key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := store.Delete(ctx, key); err != nil {
		t.Errorf("Delete must be idempotent: %v", err)
	}
	if _, err := store.Head(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Errorf("Head(after delete) = %v, want ErrNotFound", err)
	}
}

// TestITS3PresignMissingObject documents the deliberate HEAD-before-presign so a
// client never receives a URL for an object that does not exist.
func TestITS3PresignMissingObject(t *testing.T) {
	store := itStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	_, err := store.PresignGet(ctx, "ADATRACK-IT/does-not-exist.jpg", time.Minute)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("PresignGet(missing) = %v, want ErrNotFound", err)
	}
}
