package storage

import (
	"fmt"
	"strings"
)

// Options are the object-storage settings resolved from MEDIA_* (FR-8.1/FR-8.2).
// Kept as plain values (not internal.Config) so this package stays standalone.
type Options struct {
	// Backend selects the implementation: "mem" (dev/test) or "s3".
	Backend string
	// S3 connection settings (MEDIA_S3_*).
	Endpoint  string
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
	UseSSL    bool
}

// New builds the store selected by Options.Backend ("mem" is the dev default).
func New(opts Options) (Store, error) {
	switch strings.ToLower(strings.TrimSpace(opts.Backend)) {
	case "", "mem":
		return NewMem(opts.Bucket), nil
	case "s3":
		return NewS3(S3Config{
			Endpoint:  opts.Endpoint,
			Region:    opts.Region,
			Bucket:    opts.Bucket,
			AccessKey: opts.AccessKey,
			SecretKey: opts.SecretKey,
			UseSSL:    opts.UseSSL,
		})
	default:
		return nil, fmt.Errorf("storage: unknown MEDIA_BACKEND %q (want mem|s3)", opts.Backend)
	}
}
