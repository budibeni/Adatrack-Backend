package storage

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

// TestMemRoundTripByteExact is the B5a acceptance contract (FR-8.5): what is
// uploaded comes back byte-persis, including binary payloads and exact length.
func TestMemRoundTripByteExact(t *testing.T) {
	ctx := context.Background()
	store := NewMem("adatrack-media")

	payload := []byte{0x00, 0x01, 0xFF, 0xFE, 0x7F, 0x80, 0x0D, 0x0A}
	obj, err := store.Put(ctx, "ACME/sos/1.jpg", payload, "image/jpeg")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if obj.Size != int64(len(payload)) {
		t.Errorf("Put size = %d, want %d", obj.Size, len(payload))
	}
	if obj.ETag == "" {
		t.Error("Put must return a non-empty ETag")
	}

	got, err := store.Get(ctx, "ACME/sos/1.jpg")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("round trip mismatch: got %d bytes %v, want %v", len(got), got, payload)
	}
}

// TestMemNotFoundAndIdempotentDelete covers the missing-object contract.
func TestMemNotFoundAndIdempotentDelete(t *testing.T) {
	ctx := context.Background()
	store := NewMem("adatrack-media")

	if _, err := store.Get(ctx, "missing/key"); err != ErrNotFound {
		t.Errorf("Get(missing) err = %v, want ErrNotFound", err)
	}
	if _, err := store.PresignGet(ctx, "missing/key", time.Minute); err != ErrNotFound {
		t.Errorf("PresignGet(missing) err = %v, want ErrNotFound", err)
	}
	if err := store.Delete(ctx, "missing/key"); err != nil {
		t.Errorf("Delete(missing) = %v, want nil (idempotent)", err)
	}

	if _, err := store.Put(ctx, "tmp", []byte("x"), "text/plain"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := store.Delete(ctx, "tmp"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Get(ctx, "tmp"); err != ErrNotFound {
		t.Errorf("Get(after delete) = %v, want ErrNotFound", err)
	}
}

// TestMemPresignContainsKeyAndExpiry documents the symbolic dev URL shape
// (`mem://bucket/key?expires=...`) used until MinIO is provisioned live.
func TestMemPresignContainsKeyAndExpiry(t *testing.T) {
	ctx := context.Background()
	store := NewMem("adatrack-media")
	if _, err := store.Put(ctx, "ACME/alarm/2.mp4", []byte("clip"), "video/mp4"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	raw, err := store.PresignGet(ctx, "ACME/alarm/2.mp4", 5*time.Minute)
	if err != nil {
		t.Fatalf("PresignGet: %v", err)
	}
	if !strings.HasPrefix(raw, "mem://adatrack-media/ACME/alarm/2.mp4?expires=") {
		t.Errorf("presigned URL = %q, want mem://<bucket>/<key>?expires=<unix>", raw)
	}
}

// TestMemHealthAndEmptyKeyHealth documents Health as always-OK for dev.
func TestMemHealth(t *testing.T) {
	if err := NewMem("adatrack-media").Health(context.Background()); err != nil {
		t.Errorf("Health = %v, want nil", err)
	}
}
