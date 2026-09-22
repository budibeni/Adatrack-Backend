package storage

// s3_ops_test.go — kontrak HTTP S3Store diuji TANPA MinIO hidup.
//
// Sebelum file ini, `Put`/`Head`/`Get`/`Delete`/`PresignGet`/`Health`/
// `EnsureBucket` beserta helper error-mapping-nya 0 % kecuali lewat suite IT
// (`s3_it_test.go`, butuh `ADATRACK_IT=1` + MinIO). Akibatnya pengukuran non-IT
// (dan gate coverage) hanya melihat ~50 %. Di sini endpoint S3 tiruan
// (httptest, path-style) memverifikasi verb, path, header, pemetaan status →
// error, dan sifat idempoten — hermetik dan deterministik.

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// s3Stub is a minimal path-style S3 endpoint backed by a map.
type s3Stub struct {
	mu      sync.Mutex
	objects map[string][]byte
	calls   []string
	// failAllStatus, when non-zero, is returned for every request.
	failAllStatus int
	// emptyErrorBody drops the body of an error response (statusError detail "").
	emptyErrorBody bool
}

func newS3Stub() *s3Stub { return &s3Stub{objects: map[string][]byte{}} }

func (s *s3Stub) record(method, path string) {
	s.mu.Lock()
	s.calls = append(s.calls, method+" "+path)
	s.mu.Unlock()
}

func (s *s3Stub) methods() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

// object returns a copy of the stored bytes for a path.
func (s *s3Stub) object(path string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.objects[path]
	return append([]byte(nil), data...), ok
}

func (s *s3Stub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.record(r.Method, r.URL.Path)

	s.mu.Lock()
	fail := s.failAllStatus
	empty := s.emptyErrorBody
	s.mu.Unlock()
	if fail != 0 {
		w.WriteHeader(fail)
		if !empty {
			_, _ = io.WriteString(w, "boom-detail")
		}
		return
	}

	body, exists := s.object(r.URL.Path)
	isBucket := !strings.Contains(strings.TrimPrefix(r.URL.Path, "/"), "/")

	switch r.Method {
	case http.MethodPut:
		if isBucket {
			w.WriteHeader(http.StatusOK)
			return
		}
		data, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.objects[r.URL.Path] = data
		s.mu.Unlock()
		w.Header().Set("ETag", `"etag-abc"`)
		w.WriteHeader(http.StatusOK)
	case http.MethodHead:
		if isBucket {
			w.WriteHeader(http.StatusOK)
			return
		}
		if !exists {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("ETag", `"etag-abc"`)
		w.Header().Set("Content-Length", "5")
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		if !exists {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	case http.MethodDelete:
		if !exists {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		s.mu.Lock()
		delete(s.objects, r.URL.Path)
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// newStubS3 wires an S3Store onto the stub endpoint.
func newStubS3(t *testing.T, stub *s3Stub) (*S3Store, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(stub)
	t.Cleanup(srv.Close)
	store, err := NewS3(S3Config{
		Endpoint: srv.URL, Region: "us-east-1", Bucket: "bucket-a",
		AccessKey: "access", SecretKey: "secret", Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewS3: %v", err)
	}
	return store, srv
}

// TestS3OpsRoundTrip verifies the verb/path/header contract of Put/Head/Get/Delete
// and the byte-exactness the media pipeline depends on (FR-8.2/FR-8.4).
func TestS3OpsRoundTrip(t *testing.T) {
	stub := newS3Stub()
	store, _ := newStubS3(t, stub)
	ctx := context.Background()
	key := "dashcam/2026/09/clip.bin"
	want := []byte("media-bytes-12345")

	if got := store.Endpoint(); !strings.HasPrefix(got, "http://127.0.0.1:") {
		t.Fatalf("Endpoint() = %q, want the stub URL", got)
	}
	if got := store.Bucket(); got != "bucket-a" {
		t.Fatalf("Bucket() = %q, want bucket-a", got)
	}

	obj, err := store.Put(ctx, key, want, "video/mp4")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if obj.Key != key || obj.Size != int64(len(want)) {
		t.Fatalf("Put returned %+v, want key/size %s/%d", obj, key, len(want))
	}
	if obj.ETag != "etag-abc" {
		t.Fatalf("Put ETag = %q, want unquoted etag-abc", obj.ETag)
	}
	// Path-style URL + byte-exact body landed on the endpoint.
	if got, ok := stub.object("/bucket-a/" + key); !ok || string(got) != string(want) {
		t.Fatalf("stub stored %q (ok=%v), want %q", got, ok, want)
	}

	head, err := store.Head(ctx, key)
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if head.Size != 5 || head.ETag != "etag-abc" {
		t.Fatalf("Head = %+v, want size 5 / etag-abc", head)
	}

	data, err := store.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(data) != string(want) {
		t.Fatalf("Get = %q, want %q", data, want)
	}

	if err := store.Delete(ctx, key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// Idempotent: deleting again is not an error (404 from the endpoint).
	if err := store.Delete(ctx, key); err != nil {
		t.Fatalf("Delete (2nd): %v", err)
	}

	// Missing object maps to the sentinel the handlers branch on.
	if _, err := store.Head(ctx, key); !IsNotFound(err) {
		t.Fatalf("Head(missing) = %v, want ErrNotFound", err)
	}
	if _, err := store.Get(ctx, key); !IsNotFound(err) {
		t.Fatalf("Get(missing) = %v, want ErrNotFound", err)
	}
	if _, err := store.PresignGet(ctx, key, time.Minute); !IsNotFound(err) {
		t.Fatalf("PresignGet(missing) = %v, want ErrNotFound (tanpa URL untuk objek hilang)", err)
	}

	if got := stub.methods(); len(got) == 0 {
		t.Fatal("stub tidak menerima request apa pun")
	}
}

// TestS3OpsErrorMapping pins the status -> error contract: the S3 code/body is
// surfaced (diagnosability) and 5xx is NOT reported as a missing object.
func TestS3OpsErrorMapping(t *testing.T) {
	ctx := context.Background()

	t.Run("5xx surfaces the status and detail", func(t *testing.T) {
		stub := newS3Stub()
		stub.failAllStatus = http.StatusInternalServerError
		store, _ := newStubS3(t, stub)

		_, err := store.Put(ctx, "k", []byte("x"), "text/plain")
		if err == nil {
			t.Fatal("Put pada 500 berhasil, want error")
		}
		if IsNotFound(err) {
			t.Fatalf("500 dipetakan sebagai not-found: %v", err)
		}
		if !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "boom-detail") {
			t.Fatalf("error tidak memuat status/detail: %v", err)
		}

		if err := store.Health(ctx); err == nil {
			t.Fatal("Health pada 500 = nil, want error")
		}
		if err := store.EnsureBucket(ctx); err == nil {
			t.Fatal("EnsureBucket pada 500 = nil, want error")
		}
		if err := store.Delete(ctx, "k"); err == nil {
			t.Fatal("Delete pada 500 = nil, want error")
		}
		if _, err := store.Get(ctx, "k"); err == nil {
			t.Fatal("Get pada 500 = nil, want error")
		}
	})

	t.Run("empty detail still yields a usable message", func(t *testing.T) {
		stub := newS3Stub()
		stub.failAllStatus = http.StatusServiceUnavailable
		stub.emptyErrorBody = true
		store, _ := newStubS3(t, stub)

		err := store.Health(ctx)
		if err == nil {
			t.Fatal("Health pada 503 = nil, want error")
		}
		if !strings.Contains(err.Error(), "status 503") {
			t.Fatalf("error tanpa detail = %v, want status 503", err)
		}
	})

	t.Run("EnsureBucket treats already-owned as success", func(t *testing.T) {
		for _, code := range []int{http.StatusOK, http.StatusCreated, http.StatusNoContent,
			http.StatusConflict, http.StatusBadRequest} {
			stub := newS3Stub()
			stub.failAllStatus = code
			store, _ := newStubS3(t, stub)
			if err := store.EnsureBucket(ctx); err != nil {
				t.Fatalf("EnsureBucket(%d) = %v, want nil (idempoten)", code, err)
			}
		}
	})
}

// TestS3OpsTransportFailureIsUnavailable asserts a dead endpoint is reported as
// ErrUnavailable (HTTP 503 upstream), never as a missing object (FR-8.8).
func TestS3OpsTransportFailureIsUnavailable(t *testing.T) {
	stub := newS3Stub()
	store, srv := newStubS3(t, stub)
	srv.Close() // endpoint mati: kegagalan transport
	ctx := context.Background()

	calls := map[string]func() error{
		"Put":          func() error { _, err := store.Put(ctx, "k", []byte("x"), "text/plain"); return err },
		"Head":         func() error { _, err := store.Head(ctx, "k"); return err },
		"Get":          func() error { _, err := store.Get(ctx, "k"); return err },
		"Delete":       func() error { return store.Delete(ctx, "k") },
		"Health":       func() error { return store.Health(ctx) },
		"EnsureBucket": func() error { return store.EnsureBucket(ctx) },
	}
	for name, call := range calls {
		err := call()
		if err == nil {
			t.Fatalf("%s pada endpoint mati = nil, want error", name)
		}
		if !IsUnavailable(err) {
			t.Fatalf("%s error = %v, want ErrUnavailable", name, err)
		}
		if IsNotFound(err) {
			t.Fatalf("%s error dipetakan sebagai not-found: %v", name, err)
		}
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("%s error tidak membungkus ErrUnavailable: %v", name, err)
		}
	}
}

// TestS3PresignGetExistingObject asserts HEAD-first behaviour plus the shape of a
// signed URL (clients fetch directly from it).
func TestS3PresignGetExistingObject(t *testing.T) {
	stub := newS3Stub()
	store, _ := newStubS3(t, stub)
	ctx := context.Background()
	key := "dashcam/clip.bin"

	if _, err := store.Put(ctx, key, []byte("12345"), "video/mp4"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	url, err := store.PresignGet(ctx, key, 15*time.Minute)
	if err != nil {
		t.Fatalf("PresignGet: %v", err)
	}
	// Path-style + V4 query parameters.
	if !strings.Contains(url, "/bucket-a/"+key) {
		t.Fatalf("presigned URL tidak path-style: %q", url)
	}
	for _, want := range []string{"X-Amz-Signature=", "X-Amz-Expires=", "X-Amz-Credential="} {
		if !strings.Contains(url, want) {
			t.Fatalf("presigned URL kehilangan %s: %q", want, url)
		}
	}
}

// TestMemHeadAndPresignPut closes the two in-memory methods no test reached:
// Head (metadata + ErrNotFound) and PresignPut (content-type guard).
func TestMemHeadAndPresignPut(t *testing.T) {
	ctx := context.Background()
	m := NewMem("bucket-a")

	if _, err := m.Head(ctx, "absent"); !IsNotFound(err) {
		t.Fatalf("Mem.Head(absent) = %v, want ErrNotFound", err)
	}

	obj, err := m.Put(ctx, "k", []byte("hello"), "text/plain")
	if err != nil {
		t.Fatalf("Mem.Put: %v", err)
	}
	head, err := m.Head(ctx, "k")
	if err != nil {
		t.Fatalf("Mem.Head: %v", err)
	}
	if head.Key != obj.Key || head.Size != obj.Size || head.ETag != obj.ETag {
		t.Fatalf("Mem.Head = %+v, want %+v", head, obj)
	}

	url, err := m.PresignPut(ctx, "k", "text/plain", time.Minute)
	if err != nil {
		t.Fatalf("Mem.PresignPut: %v", err)
	}
	if !strings.Contains(url, "mem://bucket-a/k") || !strings.Contains(url, "upload=1") {
		t.Fatalf("Mem.PresignPut URL = %q, want mem://bucket-a/k?...upload=1", url)
	}
	// Tanpa content-type tidak ada yang bisa diunggah: ditolak, bukan URL valid.
	if _, err := m.PresignPut(ctx, "k", "", time.Minute); !IsNotFound(err) {
		t.Fatalf("Mem.PresignPut(contentType kosong) = %v, want ErrNotFound", err)
	}
}
