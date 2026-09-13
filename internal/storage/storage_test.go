package storage

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestMemStoreCRUD(t *testing.T) {
	ctx := context.Background()
	s := NewMemStore("adatrack-media")

	key := "DEV001/1/202608/abc-123"
	n, err := s.PutObject(ctx, "adatrack-media", key, strings.NewReader("hello world"), 11, "image/jpeg")
	if err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	if n != 11 {
		t.Fatalf("PutObject size = %d, want 11", n)
	}

	// PresignGet
	u, err := s.PresignGet(ctx, "adatrack-media", key, time.Minute)
	if err != nil {
		t.Fatalf("PresignGet: %v", err)
	}
	if !strings.Contains(u, "abc-123") {
		t.Fatalf("PresignGet URL = %q, want key embedded", u)
	}

	// List
	items, err := s.List(ctx, "adatrack-media", "DEV001/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 || items[0] != key {
		t.Fatalf("List = %v", items)
	}

	// Get (assert helper)
	data, ok := s.Get("adatrack-media", key)
	if !ok || string(data) != "hello world" {
		t.Fatalf("Get returned %q, %v", data, ok)
	}

	// Delete
	if err := s.Delete(ctx, "adatrack-media", key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err = s.PresignGet(ctx, "adatrack-media", key, time.Minute)
	if !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("after delete PresignGet err=%v, want ErrObjectNotFound", err)
	}
}

func TestStorage_KeyLayout(t *testing.T) {
	// FR-8.2 key layout: {company}/{vehicle}/{yyyyMM}/{uuid}
	got := buildObjectKey("DEV001", "7", "202608", "uuid-42")
	want := "DEV001/7/202608/uuid-42"
	if got != want {
		t.Fatalf("buildObjectKey = %q, want %q", got, want)
	}
}

func TestS3Store_StripScheme(t *testing.T) {
	cases := map[string]string{
		"http://localhost:9000":  "localhost:9000",
		"https://s3.foo.com":     "s3.foo.com",
		"localhost:9000":         "localhost:9000",
		"s3.amazonaws.com":       "s3.amazonaws.com",
		"":                       "",
		"  http://127.0.0.1:9000 ": "127.0.0.1:9000",
	}
	for in, want := range cases {
		if got := stripScheme(in); got != want {
			t.Errorf("stripScheme(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMemStore_ErrorSemantics(t *testing.T) {
	s := NewMemStore("other-bucket")
	_, err := s.List(context.Background(), "missing-bucket", "x/")
	if !errors.Is(err, ErrBucketNotFound) {
		t.Fatalf("List missing bucket err=%v, want ErrBucketNotFound", err)
	}
	if s.Ping(context.Background()) != nil {
		t.Fatalf("Ping should succeed for mem store")
	}
}

func TestMemStore_Stat(t *testing.T) {
	ctx := context.Background()
	s := NewMemStore("b")
	_, _ = s.PutObject(ctx, "b", "k/1", strings.NewReader("hello"), 5, "text/plain")

	sz, err := s.Stat(ctx, "b", "k/1")
	if err != nil || sz != 5 {
		t.Fatalf("Stat = %d, %v; want 5, nil", sz, err)
	}
	if _, err := s.Stat(ctx, "b", "k/nope"); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("Stat missing = %v, want ErrObjectNotFound", err)
	}
	if _, err := s.Stat(ctx, "missing", "k/1"); !errors.Is(err, ErrBucketNotFound) {
		t.Fatalf("Stat missing bucket = %v, want ErrBucketNotFound", err)
	}
}

func buildObjectKey(company, vehicle, yyyyMM, uuid string) string {
	return company + "/" + vehicle + "/" + yyyyMM + "/" + uuid
}