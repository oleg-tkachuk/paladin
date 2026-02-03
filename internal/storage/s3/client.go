package s3

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"paladin/internal/config"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
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
	staticCreds := credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, "")

	createClient := func(endpoint string) (*s3.Client, error) {
		resolved, err := url.Parse(endpoint)
		if err != nil {
			return nil, fmt.Errorf("invalid s3 endpoint: %w", err)
		}

		customResolver := aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
			if strings.EqualFold(service, s3.ServiceID) {
				return aws.Endpoint{
					URL:               resolved.String(),
					HostnameImmutable: true,
					SigningRegion:     cfg.Region,
				}, nil
			}
			return aws.Endpoint{}, &aws.EndpointNotFoundError{}
		})

		awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
			awsconfig.WithRegion(cfg.Region),
			awsconfig.WithCredentialsProvider(staticCreds),
			awsconfig.WithEndpointResolverWithOptions(customResolver),
		)
		if err != nil {
			return nil, fmt.Errorf("aws config load: %w", err)
		}

		return s3.NewFromConfig(awsCfg, func(o *s3.Options) {
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

type Presigned struct {
	URL       string            `json:"url"`
	Method    string            `json:"method"`
	Headers   map[string]string `json:"headers,omitempty"`
	ExpiresAt time.Time         `json:"expires_at"`
}

type HeadRecord struct {
	Key          string
	ETag         string
	SizeBytes    int64
	ContentType  string
	LastModified time.Time
	Metadata     map[string]string
}

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

func (c *Client) Health(ctx context.Context) error {
	if c == nil || c.s3 == nil {
		return fmt.Errorf("s3 client not initialized")
	}

	_, err := c.s3.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(c.Bucket),
	})

	return err
}

func (c *Client) PresignPutObject(ctx context.Context, key string, contentType string, sizeBytes int64, ttl time.Duration) (Presigned, error) {
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
		return Presigned{}, fmt.Errorf("presign put object: %w", err)
	}

	c.log.Debug("S3 presign PUT success", zap.String("key", key), zap.String("content_type", contentType))

	headers := map[string]string{"Content-Type": contentType}
	if c.SSEType != "" {
		headers["x-amz-server-side-encryption"] = c.SSEType
		if c.SSEKeyID != "" {
			headers["x-amz-server-side-encryption-aws-kms-key-id"] = c.SSEKeyID
		}
	}

	return Presigned{
		URL:       out.URL,
		Method:    "PUT",
		Headers:   headers,
		ExpiresAt: time.Now().Add(expiry),
	}, nil
}

func (c *Client) PresignGetObject(ctx context.Context, key string, ttl time.Duration) (Presigned, error) {
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
		return Presigned{}, fmt.Errorf("presign get object: %w", err)
	}

	c.log.Debug("S3 presign GET success", zap.String("key", key))

	return Presigned{
		URL:       out.URL,
		Method:    "GET",
		ExpiresAt: time.Now().Add(expiry),
	}, nil
}

type MultipartInit struct {
	UploadID  string    `json:"upload_id"`
	Key       string    `json:"key"`
	Bucket    string    `json:"bucket"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (c *Client) CreateMultipartUpload(ctx context.Context, key string, contentType string) (MultipartInit, error) {
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

	out, err := c.s3.CreateMultipartUpload(ctx, in)
	if err != nil {
		c.log.Error("S3 create multipart error", zap.String("key", key), zap.Error(err))
		return MultipartInit{}, fmt.Errorf("create multipart upload: %w", err)
	}

	uploadID := aws.ToString(out.UploadId)
	c.log.Info("S3 multipart upload initiated", zap.String("key", key), zap.String("upload_id", uploadID))

	return MultipartInit{
		UploadID:  aws.ToString(out.UploadId),
		Key:       key,
		Bucket:    c.Bucket,
		ExpiresAt: time.Now().Add(c.PresignTTL),
	}, nil
}

func (c *Client) PresignUploadPart(ctx context.Context, key, uploadID string, partNumber int32, ttl time.Duration) (Presigned, error) {
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
		return Presigned{}, fmt.Errorf("presign upload part: %w", err)
	}

	return Presigned{
		URL:       out.URL,
		Method:    "PUT",
		ExpiresAt: time.Now().Add(expiry),
	}, nil
}

func (c *Client) CompleteMultipartUpload(ctx context.Context, key, uploadID string, parts []types.CompletedPart) error {
	_, err := c.s3.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(c.Bucket),
		Key:      aws.String(key),
		UploadId: aws.String(uploadID),
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: parts,
		},
	})

	if err != nil {
		c.log.Error("S3 complete multipart error", zap.String("key", key), zap.String("upload_id", uploadID), zap.Error(err))
		return fmt.Errorf("complete multipart upload: %w", err)
	}

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

func (c *Client) HeadObject(ctx context.Context, key string) (*HeadRecord, error) {
	out, err := c.s3.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(c.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, err
	}

	return &HeadRecord{
		Key:          key,
		ETag:         aws.ToString(out.ETag),
		SizeBytes:    aws.ToInt64(out.ContentLength),
		ContentType:  aws.ToString(out.ContentType),
		LastModified: aws.ToTime(out.LastModified),
		Metadata:     out.Metadata,
	}, nil
}
func (c *Client) DeleteObject(ctx context.Context, key string) error {
	_, err := c.s3.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(c.Bucket),
		Key:    aws.String(key),
	})
	return err
}
