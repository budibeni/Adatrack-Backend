package storage

import (
	"context"
	"io"
)

// Store defines the interface for media storage
type Store interface {
	Upload(ctx context.Context, companyCode, vehicleID, yyyyMM, uuid, contentType string, reader io.Reader, size int64) (string, error)
	GetPresignedURL(ctx context.Context, companyCode, vehicleID, yyyyMM, uuid string) (string, error)
	GetPresignedPutURL(ctx context.Context, companyCode, vehicleID, yyyyMM, uuid, contentType string) (string, error)
	Delete(ctx context.Context, companyCode, vehicleID, yyyyMM, uuid string) error
}
