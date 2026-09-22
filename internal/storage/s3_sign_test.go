package storage

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// fixedClock pins the signature timestamps so the tests are deterministic.
func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

// newTestS3 builds a store with the AWS documentation credentials.
func newTestS3(t *testing.T) *S3Store {
	t.Helper()
	store, err := NewS3(S3Config{
		Endpoint:  "http://127.0.0.1:9000",
		Region:    "us-east-1",
		Bucket:    "adatrack-media",
		AccessKey: "AKIAIOSFODNN7EXAMPLE",
		SecretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	})
	if err != nil {
		t.Fatalf("NewS3: %v", err)
	}
	store.now = fixedClock(time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC))
	return store
}

// TestSignatureMatchesAWSReferenceVector verifies the SigV4 signing-key chain +
// string-to-sign against the canonical example published in the AWS SigV4
// documentation ("GET object with a Range header", 2013-05-24, us-east-1). The
// expected signature is the documented one — an independent oracle for our
// hand-rolled signer.
func TestSignatureMatchesAWSReferenceVector(t *testing.T) {
	store := newTestS3(t)
	canonicalRequest := strings.Join([]string{
		"GET",
		"/test.txt",
		"",
		"host:examplebucket.s3.amazonaws.com",
		"range:bytes=0-9",
		"x-amz-content-sha256:" + emptyPayloadHash,
		"x-amz-date:20130524T000000Z",
		"",
		"host;range;x-amz-content-sha256;x-amz-date",
		emptyPayloadHash,
	}, "\n")

	const (
		wantRequestHash = "7344ae5b7ee6c3e7e6b0fe0640412a37625d1fbfff95c48bbb2dc43964946972"
		wantSignature   = "f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"
	)
	got := store.signature("20130524/us-east-1/s3/aws4_request", "20130524T000000Z", canonicalRequest)
	if got != wantSignature {
		t.Fatalf("signature = %s, want the AWS reference %s", got, wantSignature)
	}
	if hash := sha256Hex([]byte(canonicalRequest)); hash != wantRequestHash {
		t.Errorf("canonical request hash = %s, want %s", hash, wantRequestHash)
	}
}

// TestSignV4HeaderShape asserts the header-auth form covers exactly the headers
// that must be signed and stamps the injected clock.
func TestSignV4HeaderShape(t *testing.T) {
	store := newTestS3(t)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut,
		"http://127.0.0.1:9000/adatrack-media/DEV001/1/202609/a.jpg", strings.NewReader("abc"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "image/jpeg")

	hash := sha256Hex([]byte("abc"))
	store.signV4(req, hash)

	if got := req.Header.Get("X-Amz-Date"); got != "20130524T000000Z" {
		t.Errorf("X-Amz-Date = %q, want the injected clock", got)
	}
	if got := req.Header.Get("X-Amz-Content-Sha256"); got != hash {
		t.Errorf("X-Amz-Content-Sha256 = %q, want %q", got, hash)
	}
	auth := req.Header.Get("Authorization")
	if !strings.HasPrefix(auth, sigAlgorithm+" Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request") {
		t.Errorf("Authorization = %q, want the AWS4 credential scope", auth)
	}
	for _, name := range []string{"content-type", "host", "x-amz-content-sha256", "x-amz-date"} {
		if !strings.Contains(auth, name) {
			t.Errorf("SignedHeaders %q is missing %s", auth, name)
		}
	}
	if len(auth) < 120 {
		t.Errorf("Authorization looks truncated: %q", auth)
	}
}

// TestCanonicalHeadersExcludeUnsignedHeaders documents that a header a proxy may
// add (e.g. X-Request-ID) is NOT part of the signature.
func TestCanonicalHeadersExcludeUnsignedHeaders(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "http://localhost/bucket/key", nil)
	req.Header.Set("X-Request-ID", "abc")
	req.Header.Set("Content-Type", "image/jpeg")
	block, signed := canonicalHeaders(req, "localhost:9000")
	if strings.Contains(block, "x-request-id") {
		t.Errorf("canonical headers must not cover X-Request-ID: %q", block)
	}
	if !strings.Contains(signed, "content-type") || !strings.Contains(signed, "host") {
		t.Errorf("signed headers = %q, want host + content-type", signed)
	}
}

// TestPresignGetShape verifies the query-auth URL (FR-8.4) carries the documented
// X-Amz-* parameters and a hex signature.
func TestPresignGetShape(t *testing.T) {
	store := newTestS3(t)
	raw, err := store.presignGet("DEV001/1/202609/a.jpg", 5*time.Minute)
	if err != nil {
		t.Fatalf("presignGet: %v", err)
	}
	for _, needle := range []string{
		"X-Amz-Algorithm=AWS4-HMAC-SHA256",
		"X-Amz-Credential=AKIAIOSFODNN7EXAMPLE%2F20130524%2Fus-east-1%2Fs3%2Faws4_request",
		"X-Amz-Date=20130524T000000Z",
		"X-Amz-Expires=300",
		"X-Amz-SignedHeaders=host",
		"X-Amz-Signature=",
	} {
		if !strings.Contains(raw, needle) {
			t.Errorf("presigned URL %q is missing %s", raw, needle)
		}
	}
	sig := raw[strings.Index(raw, "X-Amz-Signature=")+len("X-Amz-Signature="):]
	if len(sig) != 64 || strings.ContainsFunc(sig, func(r rune) bool {
		return !strings.ContainsRune("0123456789abcdef", r)
	}) {
		t.Errorf("signature %q is not a sha256 hex digest", sig)
	}
}

// TestPresignClampsTTL documents the 7-day SigV4 ceiling.
func TestPresignClampsTTL(t *testing.T) {
	store := newTestS3(t)
	raw, err := store.presignGet("key", 30*24*time.Hour)
	if err != nil {
		t.Fatalf("presignGet: %v", err)
	}
	if !strings.Contains(raw, "X-Amz-Expires=604800") {
		t.Errorf("presigned URL %q must clamp the TTL to 604800 s", raw)
	}
}

// TestS3Escape covers the SigV4 URI-encoding rules.
func TestS3Escape(t *testing.T) {
	cases := map[string]string{
		"simple":         "simple",
		"a b":            "a%20b",
		"a+b":            "a%2Bb",
		"a/b":            "a%2Fb",
		"~_-.":           "~_-.",
		"DEV001/1/x.jpg": "DEV001%2F1%2Fx.jpg",
	}
	for in, want := range cases {
		if got := s3Escape(in, true); got != want {
			t.Errorf("s3Escape(%q, true) = %q, want %q", in, got, want)
		}
	}
	if got := s3Escape("DEV001/1/x.jpg", false); got != "DEV001/1/x.jpg" {
		t.Errorf("s3Escape(path, false) = %q, want the separators preserved", got)
	}
}

// TestObjectURLPathStyle pins the documented key layout (FR-8.2):
// {company}/{vehicle}/{yyyyMM}/{uuid} under the bucket.
func TestObjectURLPathStyle(t *testing.T) {
	store := newTestS3(t)
	got := store.objectURL("DEV001/12/202609/42-abcdef.jpg")
	want := "http://127.0.0.1:9000/adatrack-media/DEV001/12/202609/42-abcdef.jpg"
	if got != want {
		t.Errorf("objectURL = %q, want %q", got, want)
	}
	if bucket := store.objectURL(""); bucket != "http://127.0.0.1:9000/adatrack-media/" {
		t.Errorf("bucket URL = %q, want the bucket root (used by HEAD bucket)", bucket)
	}
}

// TestNewRejectsIncompleteS3Config documents the fail-fast boot contract.
func TestNewRejectsIncompleteS3Config(t *testing.T) {
	if _, err := New(Options{Backend: "s3"}); err == nil {
		t.Error("New(s3) without endpoint/bucket/credentials must fail")
	}
	if _, err := New(Options{Backend: "gcs"}); err == nil {
		t.Error("New(unknown backend) must fail")
	}
	store, err := New(Options{Backend: "mem", Bucket: "adatrack-media"})
	if err != nil || store == nil {
		t.Fatalf("New(mem) = %v, %v", store, err)
	}
}
