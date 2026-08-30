package storage

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3Config carries S3-compatible connection settings for the S3 store.
type S3Config struct {
	// Endpoint is host[:port] WITHOUT scheme (e.g. "localhost:9000"). A scheme
	// prefix is tolerated and stripped; TLS is derived from UseSSL.
	Endpoint string
	// Bucket is the default bucket used by service-media ingest/presign.
	Bucket string
	// AccessKey / SecretKey for static credentials (minioadmin in dev MinIO).
	AccessKey string
	SecretKey string
	// UseSSL selects HTTPS when true.
	UseSSL bool
	// Region is the S3 region (optional; minio defaults to us-east-1).
	Region string
}

// S3Store is the S3-compatible implementation of Store built on minio-go/v7.
type S3Store struct {
	client *minio.Client
	keys   []string // buckets this store is configured to manage (metrics)
}

// NewS3Store builds an S3Store and, when EnsureBucket is true, creates the
// default bucket if missing (idempotent). Errors are returned early; retries
// are handled by the caller/service (exponential backoff per convention).
func NewS3Store(cfg S3Config, extraBuckets []string) (*S3Store, error) {
	endpoint := stripScheme(cfg.Endpoint)
	opts := &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
	}
	if cfg.Region != "" {
		opts.Region = cfg.Region
	}
	cl, err := minio.New(endpoint, opts)
	if err != nil {
		return nil, fmt.Errorf("storage: init minio client: %w", err)
	}
	keys := []string{}
	if cfg.Bucket != "" {
		keys = append(keys, cfg.Bucket)
	}
	seen := map[string]bool{}
	for _, b := range extraBuckets {
		b = strings.TrimSpace(b)
		if b == "" || seen[b] {
			continue
		}
		seen[b] = true
		keys = append(keys, b)
	}
	return &S3Store{client: cl, keys: keys}, nil
}

// EnsureBucket creates the bucket when absent.
func (s *S3Store) EnsureBucket(ctx context.Context, bucket string) error {
	exists, err := s.client.BucketExists(ctx, bucket)
	if err != nil {
		return fmt.Errorf("storage: bucket exists check: %w", err)
	}
	if exists {
		return nil
	}
	if err := s.client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
		return fmt.Errorf("storage: make bucket %s: %w", bucket, err)
	}
	return nil
}

func (s *S3Store) PutObject(ctx context.Context, bucket, key string, r io.Reader, size int64, contentType string) (int64, error) {
	if size < 0 {
		size = -1 // let minio determine from stream
	}
	info, err := s.client.PutObject(ctx, bucket, key, r, size, minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return 0, fmt.Errorf("storage: put %s: %w", key, err)
	}
	return info.Size, nil
}

func (s *S3Store) PresignGet(ctx context.Context, bucket, key string, ttl time.Duration) (string, error) {
	p, err := s.client.PresignedGetObject(ctx, bucket, key, ttl, nil)
	if err != nil {
		return "", fmt.Errorf("storage: presign get %s: %w", key, err)
	}
	return p.String(), nil
}

func (s *S3Store) PresignPut(ctx context.Context, bucket, key string, ttl time.Duration) (string, error) {
	p, err := s.client.PresignedPutObject(ctx, bucket, key, ttl)
	if err != nil {
		return "", fmt.Errorf("storage: presign put %s: %w", key, err)
	}
	return p.String(), nil
}

func (s *S3Store) Delete(ctx context.Context, bucket, key string) error {
	err := s.client.RemoveObject(ctx, bucket, key, minio.RemoveObjectOptions{})
	if err != nil {
		return fmt.Errorf("storage: delete %s: %w", key, err)
	}
	return nil
}

// Stat returns the object size via a head/stat request.
func (s *S3Store) Stat(ctx context.Context, bucket, key string) (int64, error) {
	info, err := s.client.StatObject(ctx, bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return 0, fmt.Errorf("storage: stat %s: %w", key, err)
	}
	return info.Size, nil
}

func (s *S3Store) List(ctx context.Context, bucket, prefix string) ([]string, error) {
	ch := s.client.ListObjects(ctx, bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true})
	out := make([]string, 0, 16)
	for o := range ch {
		if o.Err != nil {
			return out, fmt.Errorf("storage: list %s/%s: %w", bucket, prefix, o.Err)
		}
		if !o.IsDeleteMarker {
			out = append(out, o.Key)
		}
	}
	return out, nil
}

func (s *S3Store) Ping(ctx context.Context) error {
	for _, b := range s.keys {
		exists, err := s.client.BucketExists(ctx, b)
		if err == nil && !exists {
			// Convenience for fresh dev deploys: create it.
			if cerr := s.client.MakeBucket(ctx, b, minio.MakeBucketOptions{}); cerr != nil {
				return fmt.Errorf("storage: ping bucket %s: %w", b, cerr)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("storage: ping bucket %s: %w", b, err)
		}
	}
	if len(s.keys) == 0 {
		// No configured bucket → probe server with an unauthenticated list call.
		_, err := s.client.ListBuckets(ctx)
		return err
	}
	return nil
}

func (s *S3Store) Buckets() []string {
	out := make([]string, len(s.keys))
	copy(out, s.keys)
	return out
}

// stripScheme removes an explicit "http://" / "https://" scheme from an endpoint
// so TLS is controlled solely by cfg.UseSSL (minio.New must receive a host:port).
func stripScheme(endpoint string) string {
	ep := strings.TrimSpace(endpoint)
	if ep == "" {
		return ep
	}
	if strings.HasPrefix(ep, "http://") {
		return strings.TrimPrefix(ep, "http://")
	}
	if strings.HasPrefix(ep, "https://") {
		return strings.TrimPrefix(ep, "https://")
	}
	return ep
}