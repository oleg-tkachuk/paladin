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

	s3        *s3.Client
	presigner *s3.PresignClient
	log       *zap.Logger
}

func New(ctx context.Context, cfg config.S3, log *zap.Logger) (*Client, error) {
	staticCreds := credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, "")

	resolved, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid s3 endpoint: %w", err)
	}

	// AWS SDK requires an endpoint resolver for non-AWS endpoints
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

	c := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = cfg.ForcePathStyle
	})

	presigner := s3.NewPresignClient(c)

	log.Info("S3 client initialized", zap.String("bucket", cfg.Bucket), zap.String("endpoint", cfg.Endpoint))

	return &Client{
		Bucket:     cfg.Bucket,
		PresignTTL: cfg.PresignTTL,
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

func (c *Client) PresignPutObject(ctx context.Context, key string, contentType string, sizeBytes int64) (Presigned, error) {
	in := &s3.PutObjectInput{
		Bucket:      aws.String(c.Bucket),
		Key:         aws.String(key),
		ContentType: aws.String(contentType),
		// ContentLength is not supported by all gateways in presign; enforced on control plane.
	}

	out, err := c.presigner.PresignPutObject(ctx, in, s3.WithPresignExpires(c.PresignTTL))
	if err != nil {
		c.log.Error("S3 presign PUT error", zap.String("key", key), zap.Error(err))
		return Presigned{}, err
	}

	c.log.Debug("S3 presign PUT success", zap.String("key", key), zap.String("content_type", contentType))

	return Presigned{
		URL:       out.URL,
		Method:    "PUT",
		Headers:   map[string]string{"Content-Type": contentType},
		ExpiresAt: time.Now().Add(c.PresignTTL),
	}, nil
}

func (c *Client) PresignGetObject(ctx context.Context, key string) (Presigned, error) {
	in := &s3.GetObjectInput{
		Bucket: aws.String(c.Bucket),
		Key:    aws.String(key),
	}

	out, err := c.presigner.PresignGetObject(ctx, in, s3.WithPresignExpires(c.PresignTTL))
	if err != nil {
		c.log.Error("S3 presign GET error", zap.String("key", key), zap.Error(err))
		return Presigned{}, err
	}

	c.log.Debug("S3 presign GET success", zap.String("key", key))

	return Presigned{
		URL:       out.URL,
		Method:    "GET",
		ExpiresAt: time.Now().Add(c.PresignTTL),
	}, nil
}

type MultipartInit struct {
	UploadID  string    `json:"upload_id"`
	Key       string    `json:"key"`
	Bucket    string    `json:"bucket"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (c *Client) CreateMultipartUpload(ctx context.Context, key string, contentType string) (MultipartInit, error) {
	out, err := c.s3.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket:      aws.String(c.Bucket),
		Key:         aws.String(key),
		ContentType: aws.String(contentType),
	})
	if err != nil {
		c.log.Error("S3 create multipart error", zap.String("key", key), zap.Error(err))
		return MultipartInit{}, err
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

func (c *Client) PresignUploadPart(ctx context.Context, key, uploadID string, partNumber int32) (Presigned, error) {
	out, err := c.presigner.PresignUploadPart(ctx, &s3.UploadPartInput{
		Bucket:     aws.String(c.Bucket),
		Key:        aws.String(key),
		UploadId:   aws.String(uploadID),
		PartNumber: aws.Int32(partNumber),
	}, s3.WithPresignExpires(c.PresignTTL))
	if err != nil {
		return Presigned{}, err
	}

	return Presigned{
		URL:       out.URL,
		Method:    "PUT",
		ExpiresAt: time.Now().Add(c.PresignTTL),
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
		return err
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
