package storage

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type S3Store struct {
	client     *minio.Client
	bucketName string
}

// NewS3Store creates a new S3Store
func NewS3Store(endpoint, accessKey, secretKey, bucketName, region string, useSSL bool) (*S3Store, error) {
	minioClient, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
		Region: region,
	})
	if err != nil {
		return nil, err
	}

	return &S3Store{
		client:     minioClient,
		bucketName: bucketName,
	}, nil
}

func (s *S3Store) buildKey(companyCode, vehicleID, yyyyMM, uuid string) string {
	return fmt.Sprintf("%s/%s/%s/%s", companyCode, vehicleID, yyyyMM, uuid)
}

func (s *S3Store) Upload(ctx context.Context, companyCode, vehicleID, yyyyMM, uuid, contentType string, reader io.Reader, size int64) (string, error) {
	key := s.buildKey(companyCode, vehicleID, yyyyMM, uuid)
	_, err := s.client.PutObject(ctx, s.bucketName, key, reader, size, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return "", err
	}
	return key, nil
}

func (s *S3Store) GetPresignedURL(ctx context.Context, companyCode, vehicleID, yyyyMM, uuid string) (string, error) {
	key := s.buildKey(companyCode, vehicleID, yyyyMM, uuid)
	url, err := s.client.PresignedGetObject(ctx, s.bucketName, key, time.Hour, nil)
	if err != nil {
		return "", err
	}
	return url.String(), nil
}

func (s *S3Store) GetPresignedPutURL(ctx context.Context, companyCode, vehicleID, yyyyMM, uuid, contentType string) (string, error) {
	key := s.buildKey(companyCode, vehicleID, yyyyMM, uuid)
	url, err := s.client.PresignedPutObject(ctx, s.bucketName, key, time.Hour)
	if err != nil {
		return "", err
	}
	return url.String(), nil
}

func (s *S3Store) Delete(ctx context.Context, companyCode, vehicleID, yyyyMM, uuid string) error {
	key := s.buildKey(companyCode, vehicleID, yyyyMM, uuid)
	return s.client.RemoveObject(ctx, s.bucketName, key, minio.RemoveObjectOptions{})
}
