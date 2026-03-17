package domain

import (
	"context"
	"time"
)

type StorageClient interface {
	BucketName() string
	PresignTTLDuration() time.Duration
	PresignPutObject(ctx context.Context, key string, contentType string, sizeBytes int64, ttl time.Duration) (Presigned, error)
	PresignGetObject(ctx context.Context, key string, ttl time.Duration) (Presigned, error)
	PresignUploadPart(ctx context.Context, key, uploadID string, partNumber int32, ttl time.Duration) (Presigned, error)
	CreateMultipartUpload(ctx context.Context, key string, contentType string) (MultipartInit, error)
	CompleteMultipartUpload(ctx context.Context, key, uploadID string, parts []CompletePart) error
	AbortMultipartUpload(ctx context.Context, key, uploadID string) error
	HeadObject(ctx context.Context, key string) (*HeadRecord, error)
	DeleteObject(ctx context.Context, key string) error
	CopyObject(ctx context.Context, srcKey, dstKey string) error
	Ping(ctx context.Context) (S3PingResult, error)
}
