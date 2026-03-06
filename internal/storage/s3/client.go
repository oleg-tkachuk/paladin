package s3

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/metrics"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	"go.uber.org/zap"
)

type Client struct {
	Bucket     string
	PresignTTL time.Duration
	SSEType    string
	SSEKeyID   string

	s3        *s3.Client
	presigner *s3.PresignClient
	log       *zap.Logger
}

func New(ctx context.Context, cfg config.S3, log *zap.Logger) (*Client, error) {
	var optFns []func(*awsconfig.LoadOptions) error

	// If credentials are provided in config, use them.
	// Otherwise, LoadDefaultConfig will use the default chain (Env, IAM, etc.)
	if cfg.AccessKey != "" || cfg.SecretKey != "" {
		customCreds := credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, "")
		optFns = append(optFns, awsconfig.WithCredentialsProvider(customCreds))
	}

	createClient := func(endpoint string) (*s3.Client, error) {

		// Prepare configuration options
		currentOptFns := []func(*awsconfig.LoadOptions) error{
			awsconfig.WithRegion(cfg.Region),
		}
		currentOptFns = append(currentOptFns, optFns...)

		awsCfg, err := awsconfig.LoadDefaultConfig(ctx, currentOptFns...)
		if err != nil {
			return nil, fmt.Errorf("aws config load: %w", err)
		}

		return s3.NewFromConfig(awsCfg, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(endpoint)
			o.UsePathStyle = cfg.ForcePathStyle
		}), nil
	}

	c, err := createClient(cfg.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("create internal s3 client: %w", err)
	}

	presignClient := c
	if cfg.PublicEndpoint != "" {
		p, err := createClient(cfg.PublicEndpoint)
		if err != nil {
			return nil, fmt.Errorf("create public s3 client: %w", err)
		}
		presignClient = p
	}

	presigner := s3.NewPresignClient(presignClient)

	log.Info("S3 client initialized", zap.String("bucket", cfg.Bucket), zap.String("endpoint", cfg.Endpoint), zap.String("public_endpoint", cfg.PublicEndpoint))

	return &Client{
		Bucket:     cfg.Bucket,
		PresignTTL: cfg.PresignTTL,
		SSEType:    cfg.SSEType,
		SSEKeyID:   cfg.SSEKeyID,
		s3:         c,
		presigner:  presigner,
		log:        log,
	}, nil
}

// Presigned and HeadRecord are now used from domain package

func (c *Client) EnsureBucket(ctx context.Context) error {
	// Best-effort bucket creation for local S3 gateways; ignore errors if exists.
	_, err := c.s3.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(c.Bucket)})
	if err != nil {
		c.log.Debug("S3 bucket create ignored (likely exists)", zap.String("bucket", c.Bucket), zap.Error(err))

		return nil
	}
	c.log.Info("S3 bucket created", zap.String("bucket", c.Bucket))

	return nil
}

func (c *Client) PresignPutObject(ctx context.Context, key string, contentType string, sizeBytes int64, ttl time.Duration) (domain.Presigned, error) {
	in := &s3.PutObjectInput{
		Bucket:      aws.String(c.Bucket),
		Key:         aws.String(key),
		ContentType: aws.String(contentType),
		// ContentLength is not supported by all gateways in presign; enforced on control plane.
	}

	if c.SSEType != "" {
		in.ServerSideEncryption = types.ServerSideEncryption(c.SSEType)
		if c.SSEKeyID != "" {
			in.SSEKMSKeyId = aws.String(c.SSEKeyID)
		}
	}

	expiry := c.PresignTTL
	if ttl > 0 {
		expiry = ttl
	}

	out, err := c.presigner.PresignPutObject(ctx, in, s3.WithPresignExpires(expiry))
	if err != nil {
		c.log.Error("S3 presign PUT error", zap.String("key", key), zap.Error(err))

		return domain.Presigned{}, fmt.Errorf("presign put object: %w", err)
	}

	c.log.Debug("S3 presign PUT success", zap.String("key", key), zap.String("content_type", contentType))

	headers := map[string]string{"Content-Type": contentType}
	if c.SSEType != "" {
		headers["x-amz-server-side-encryption"] = c.SSEType
		if c.SSEKeyID != "" {
			headers["x-amz-server-side-encryption-aws-kms-key-id"] = c.SSEKeyID
		}
	}

	return domain.Presigned{
		URL:       out.URL,
		Method:    "PUT",
		Headers:   headers,
		ExpiresAt: time.Now().Add(expiry),
	}, nil
}

func (c *Client) PresignGetObject(ctx context.Context, key string, ttl time.Duration) (domain.Presigned, error) {
	in := &s3.GetObjectInput{
		Bucket: aws.String(c.Bucket),
		Key:    aws.String(key),
	}

	expiry := c.PresignTTL
	if ttl > 0 {
		expiry = ttl
	}

	out, err := c.presigner.PresignGetObject(ctx, in, s3.WithPresignExpires(expiry))
	if err != nil {
		c.log.Error("S3 presign GET error", zap.String("key", key), zap.Error(err))

		return domain.Presigned{}, fmt.Errorf("presign get object: %w", err)
	}

	c.log.Debug("S3 presign GET success", zap.String("key", key))

	return domain.Presigned{
		URL:       out.URL,
		Method:    "GET",
		ExpiresAt: time.Now().Add(expiry),
	}, nil
}

// MultipartInit is now used from domain package

func (c *Client) CreateMultipartUpload(ctx context.Context, key string, contentType string) (domain.MultipartInit, error) {
	in := &s3.CreateMultipartUploadInput{
		Bucket:      aws.String(c.Bucket),
		Key:         aws.String(key),
		ContentType: aws.String(contentType),
	}

	if c.SSEType != "" {
		in.ServerSideEncryption = types.ServerSideEncryption(c.SSEType)
		if c.SSEKeyID != "" {
			in.SSEKMSKeyId = aws.String(c.SSEKeyID)
		}
	}

	start := time.Now()
	var status string
	defer func() { metrics.RecordS3Op(ctx, "create_multipart", status, start) }()

	out, err := c.s3.CreateMultipartUpload(ctx, in)
	if err != nil {
		status = "error"
		c.log.Error("S3 create multipart error", zap.String("key", key), zap.Error(err))

		return domain.MultipartInit{}, fmt.Errorf("create multipart upload: %w", err)
	}

	status = "success"
	uploadID := aws.ToString(out.UploadId)
	c.log.Info("S3 multipart upload initiated", zap.String("key", key), zap.String("upload_id", uploadID))

	return domain.MultipartInit{
		UploadID:  aws.ToString(out.UploadId),
		Key:       key,
		Bucket:    c.Bucket,
		ExpiresAt: time.Now().Add(c.PresignTTL),
	}, nil
}

func (c *Client) PresignUploadPart(ctx context.Context, key, uploadID string, partNumber int32, ttl time.Duration) (domain.Presigned, error) {
	expiry := c.PresignTTL
	if ttl > 0 {
		expiry = ttl
	}

	out, err := c.presigner.PresignUploadPart(ctx, &s3.UploadPartInput{
		Bucket:     aws.String(c.Bucket),
		Key:        aws.String(key),
		UploadId:   aws.String(uploadID),
		PartNumber: aws.Int32(partNumber),
	}, s3.WithPresignExpires(expiry))
	if err != nil {
		return domain.Presigned{}, fmt.Errorf("presign upload part: %w", err)
	}

	return domain.Presigned{
		URL:       out.URL,
		Method:    "PUT",
		ExpiresAt: time.Now().Add(expiry),
	}, nil
}

func (c *Client) CompleteMultipartUpload(ctx context.Context, key, uploadID string, parts []domain.CompletePart) error {
	s3Parts := make([]types.CompletedPart, len(parts))
	for i, p := range parts {
		s3Parts[i] = types.CompletedPart{
			ETag:       aws.String(p.ETag),
			PartNumber: aws.Int32(p.PartNumber),
		}
	}

	start := time.Now()
	var status string
	defer func() { metrics.RecordS3Op(ctx, "complete_multipart", status, start) }()

	_, err := c.s3.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(c.Bucket),
		Key:      aws.String(key),
		UploadId: aws.String(uploadID),
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: s3Parts,
		},
	})

	if err != nil {
		status = "error"
		c.log.Error("S3 complete multipart error", zap.String("key", key), zap.String("upload_id", uploadID), zap.Error(err))

		return fmt.Errorf("complete multipart upload: %w", err)
	}

	status = "success"
	c.log.Info("S3 multipart upload completed", zap.String("key", key), zap.String("upload_id", uploadID))

	return nil
}

func (c *Client) AbortMultipartUpload(ctx context.Context, key, uploadID string) error {
	_, err := c.s3.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(c.Bucket),
		Key:      aws.String(key),
		UploadId: aws.String(uploadID),
	})

	return err
}

func (c *Client) BucketName() string {
	return c.Bucket
}

func (c *Client) PresignTTLDuration() time.Duration {
	return c.PresignTTL
}

func (c *Client) HeadObject(ctx context.Context, key string) (*domain.HeadRecord, error) {
	start := time.Now()
	var status string
	defer func() { metrics.RecordS3Op(ctx, "head_object", status, start) }()

	out, err := c.s3.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(c.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		status = "error"

		return nil, err
	}

	status = "success"

	return &domain.HeadRecord{
		Key:          key,
		ETag:         aws.ToString(out.ETag),
		SizeBytes:    aws.ToInt64(out.ContentLength),
		ContentType:  aws.ToString(out.ContentType),
		LastModified: aws.ToTime(out.LastModified),
		Metadata:     out.Metadata,
	}, nil
}
func (c *Client) DeleteObject(ctx context.Context, key string) error {
	start := time.Now()
	var status string
	defer func() { metrics.RecordS3Op(ctx, "delete_object", status, start) }()

	_, err := c.s3.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(c.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		status = "error"

		return err
	}
	status = "success"

	return nil
}

func (c *Client) Ping(ctx context.Context) (domain.S3PingResult, error) {
	if c == nil || c.s3 == nil {
		return domain.S3PingResult{
			Status:  "unavailable",
			Message: "s3 client not initialized",
		}, nil
	}

	start := time.Now()
	_, err := c.s3.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(c.Bucket),
	})
	latency := time.Since(start)

	result := domain.S3PingResult{
		Bucket: c.Bucket,
	}

	if err == nil {
		result.Status = "healthy"
		result.HttpStatus = 200
		result.Message = fmt.Sprintf("OK (latency: %v)", latency.Round(time.Millisecond))

		return result, nil
	}

	// Process S3 error responses
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		result.Message = apiErr.ErrorMessage()
		switch apiErr.ErrorCode() {
		case "NotFound", "NoSuchBucket":
			result.Status = "degraded"
			result.HttpStatus = 404
		case "Forbidden", "AccessDenied":
			result.Status = "unavailable"
			result.HttpStatus = 403
		default:
			result.Status = "unavailable"
		}
	} else {
		result.Status = "unavailable"
		result.Message = err.Error()
	}

	// Extract HTTP status code if available
	if apiErr, ok := err.(interface{ HTTPStatusCode() int }); ok {
		result.HttpStatus = apiErr.HTTPStatusCode()
	}

	return result, nil
}
