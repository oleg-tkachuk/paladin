// Package s3adapter implements the handler-facing storage interfaces
// (object.Storage, object.StreamSink, multipart.Storage, presign.Storage)
// on top of an aws-sdk-go-v2 S3 client.
//
// One adapter instance fronts a single physical storage backend (primary).
// CompletionMode is sourced from the bundled config: when that backend has
// S3 event notifications enabled, handlers return Implicit; otherwise
// Explicit.
package s3adapter

import (
	"context"
	"crypto/md5" // #nosec G501 — legacy S3 ETag for part-level integrity, not a secret
	"encoding/hex"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/multipart"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/object"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/presign"
	"github.com/oleg-tkachuk/paladin/internal/config"
)

// Client is the all-in-one S3-compatible adapter.
type Client struct {
	s3      *s3.Client
	presign *s3.PresignClient
	cfg     config.StorageBackend
	mode    object.CompletionMode
	sseType string // "", "AES256", or "aws:kms"
	sseKey  string
}

// New builds an S3 client from one storage-backend entry. `backend.Events.Enabled`
// decides CompletionMode (IMPLICIT when events drive promotion, EXPLICIT otherwise).
func New(ctx context.Context, backend config.StorageBackend) (*Client, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(backend.Region),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(backend.AccessKey, backend.SecretKey, ""),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("aws config: %w", err)
	}

	opts := []func(*s3.Options){
		func(o *s3.Options) {
			o.UsePathStyle = backend.ForcePathStyle
			if backend.Endpoint != "" {
				o.BaseEndpoint = aws.String(backend.Endpoint)
			}
		},
	}
	s3c := s3.NewFromConfig(awsCfg, opts...)

	mode := object.CompletionModeExplicit
	if backend.Events.Enabled {
		mode = object.CompletionModeImplicit
	}

	return &Client{
		s3:      s3c,
		presign: s3.NewPresignClient(s3c),
		cfg:     backend,
		mode:    mode,
		sseType: backend.SSE.Type,
		sseKey:  backend.SSE.KeyID,
	}, nil
}

// ─── object.Storage ────────────────────────────────────────────────────────

var _ object.Storage = (*Client)(nil)

func (c *Client) PresignPut(ctx context.Context, args object.PresignPutArgs) (string, map[string]string, time.Time, error) {
	in := &s3.PutObjectInput{
		Bucket:      aws.String(args.Bucket),
		Key:         aws.String(args.Key),
		ContentType: aws.String(args.ContentType),
	}
	c.applySSE(in)
	req, err := c.presign.PresignPutObject(ctx, in, s3.WithPresignExpires(args.TTL))
	if err != nil {
		return "", nil, time.Time{}, fmt.Errorf("presign put: %w", err)
	}
	return req.URL, signedHeaders(req), time.Now().Add(args.TTL), nil
}

func (c *Client) PresignPost(ctx context.Context, args object.PresignPostArgs) (string, map[string]string, time.Time, error) {
	// aws-sdk-go-v2 doesn't ship PresignPost; return a v4-signed PUT URL with
	// POST-style fields so callers that requested POST have a working fallback.
	in := &s3.PutObjectInput{
		Bucket:      aws.String(args.Bucket),
		Key:         aws.String(args.Key),
		ContentType: aws.String(args.ContentType),
	}
	c.applySSE(in)
	req, err := c.presign.PresignPutObject(ctx, in, s3.WithPresignExpires(args.TTL))
	if err != nil {
		return "", nil, time.Time{}, fmt.Errorf("presign post fallback: %w", err)
	}
	fields := map[string]string{
		"Content-Type":          args.ContentType,
		"Content-Length-Range":  fmt.Sprintf("0,%d", args.MaxSizeBytes),
		"X-Amz-Signed-Fallback": "put",
	}
	for k, v := range signedHeaders(req) {
		fields[k] = v
	}
	return req.URL, fields, time.Now().Add(args.TTL), nil
}

func (c *Client) PresignGet(ctx context.Context, args object.PresignGetArgs) (string, map[string]string, time.Time, error) {
	in := &s3.GetObjectInput{
		Bucket: aws.String(args.Bucket),
		Key:    aws.String(args.Key),
	}
	if args.ContentDisposition != "" {
		in.ResponseContentDisposition = aws.String(args.ContentDisposition)
	}
	req, err := c.presign.PresignGetObject(ctx, in, s3.WithPresignExpires(args.TTL))
	if err != nil {
		return "", nil, time.Time{}, fmt.Errorf("presign get: %w", err)
	}
	return req.URL, signedHeaders(req), time.Now().Add(args.TTL), nil
}

func (c *Client) Head(ctx context.Context, bucket, key string) (string, int64, string, string, error) {
	out, err := c.s3.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return "", 0, "", "", fmt.Errorf("head: %w", err)
	}
	etag := strings.Trim(aws.ToString(out.ETag), `"`)
	var size int64
	if out.ContentLength != nil {
		size = *out.ContentLength
	}
	checksum := ""
	switch {
	case out.ChecksumSHA256 != nil:
		checksum = *out.ChecksumSHA256
	case out.ChecksumCRC32C != nil:
		checksum = *out.ChecksumCRC32C
	case out.ChecksumCRC32 != nil:
		checksum = *out.ChecksumCRC32
	}
	// SeaweedFS / real S3: no Sequencer from HEAD; leave empty. The event
	// pipeline supplies it where available.
	return etag, size, checksum, "", nil
}

func (c *Client) CopyObject(ctx context.Context, src, dst object.Location) error {
	_, err := c.s3.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(dst.Bucket),
		Key:        aws.String(dst.Key),
		CopySource: aws.String(src.Bucket + "/" + src.Key),
	})
	if err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	return nil
}

func (c *Client) DeleteObject(ctx context.Context, bucket, key string) error {
	_, err := c.s3.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	return nil
}

// CompletionMode ignores the bucket — the adapter fronts a single backend
// whose Implicit/Explicit mode was chosen at construction time.
func (c *Client) CompletionMode(bucketID string) object.CompletionMode {
	return c.mode
}

// ─── multipart.Storage ─────────────────────────────────────────────────────

var _ multipart.Storage = (*Client)(nil)

func (c *Client) InitiateMultipart(ctx context.Context, bucket, key, contentType string) (string, error) {
	in := &s3.CreateMultipartUploadInput{
		Bucket:      aws.String(bucket),
		Key:         aws.String(key),
		ContentType: aws.String(contentType),
	}
	c.applyMultipartSSE(in)
	out, err := c.s3.CreateMultipartUpload(ctx, in)
	if err != nil {
		return "", fmt.Errorf("initiate multipart: %w", err)
	}
	return aws.ToString(out.UploadId), nil
}

func (c *Client) CompleteMultipart(ctx context.Context, storageUploadID, bucket, key string, parts []multipart.PartETag) (string, int64, error) {
	completed := make([]s3types.CompletedPart, 0, len(parts))
	for _, p := range parts {
		completed = append(completed, s3types.CompletedPart{
			PartNumber: aws.Int32(p.PartNumber),
			ETag:       aws.String(p.ETag),
		})
	}
	out, err := c.s3.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:          aws.String(bucket),
		Key:             aws.String(key),
		UploadId:        aws.String(storageUploadID),
		MultipartUpload: &s3types.CompletedMultipartUpload{Parts: completed},
	})
	if err != nil {
		return "", 0, fmt.Errorf("complete multipart: %w", err)
	}
	etag := strings.Trim(aws.ToString(out.ETag), `"`)

	head, err := c.s3.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return etag, 0, fmt.Errorf("head after complete: %w", err)
	}
	var size int64
	if head.ContentLength != nil {
		size = *head.ContentLength
	}
	return etag, size, nil
}

func (c *Client) AbortMultipart(ctx context.Context, storageUploadID, bucket, key string) error {
	_, err := c.s3.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(bucket),
		Key:      aws.String(key),
		UploadId: aws.String(storageUploadID),
	})
	if err != nil {
		return fmt.Errorf("abort multipart: %w", err)
	}
	return nil
}

// ─── presign.Storage ───────────────────────────────────────────────────────
//
// object.Storage and presign.Storage both expose methods named PresignGet /
// PresignPut with different signatures, so the adapter cannot satisfy both
// on the same receiver. PresignView wraps a *Client and exposes the
// presign.Storage-shaped entrypoints. The Client primary methods remain
// wired to object.Storage.

type PresignView struct{ c *Client }

func (c *Client) Presign() *PresignView { return &PresignView{c: c} }

var _ presign.Storage = (*PresignView)(nil)

func (p *PresignView) PresignGet(ctx context.Context, bucket, key string, ttl time.Duration, disposition string) (string, map[string]string, time.Time, error) {
	return p.c.PresignGet(ctx, object.PresignGetArgs{
		Bucket: bucket, Key: key, TTL: ttl, ContentDisposition: disposition,
	})
}

func (p *PresignView) PresignPut(ctx context.Context, bucket, key, contentType, checksumAlgo string, ttl time.Duration, sizeHint int64) (string, map[string]string, time.Time, error) {
	return p.c.PresignPut(ctx, object.PresignPutArgs{
		Bucket: bucket, Key: key, ContentType: contentType,
		ChecksumAlgo: checksumAlgo, SizeHint: sizeHint, TTL: ttl,
	})
}

func (p *PresignView) PresignPart(ctx context.Context, storageUploadID, bucket, key string, partNumber int32, ttl time.Duration) (string, map[string]string, time.Time, error) {
	in := &s3.UploadPartInput{
		Bucket:     aws.String(bucket),
		Key:        aws.String(key),
		PartNumber: aws.Int32(partNumber),
		UploadId:   aws.String(storageUploadID),
	}
	req, err := p.c.presign.PresignUploadPart(ctx, in, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", nil, time.Time{}, fmt.Errorf("presign part: %w", err)
	}
	return req.URL, signedHeaders(req), time.Now().Add(ttl), nil
}

// ─── object.StreamSink ─────────────────────────────────────────────────────

var _ object.StreamSink = (*Client)(nil)

// Open returns a StreamWriter that uploads a single object via S3 multipart.
// 8 MiB parts are a reasonable default that balances memory vs S3 minimums.
func (c *Client) Open(ctx context.Context, bucket, key, contentType string, sizeHint int64) (object.StreamWriter, error) {
	in := &s3.CreateMultipartUploadInput{
		Bucket:      aws.String(bucket),
		Key:         aws.String(key),
		ContentType: aws.String(contentType),
	}
	c.applyMultipartSSE(in)
	out, err := c.s3.CreateMultipartUpload(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("open sink: %w", err)
	}
	partSize := c.cfg.PartSizeBytes
	if partSize <= 0 {
		partSize = 8 << 20
	}
	return &streamWriter{
		ctx:      ctx,
		c:        c,
		bucket:   bucket,
		key:      key,
		uploadID: aws.ToString(out.UploadId),
		partSize: partSize,
	}, nil
}

type streamWriter struct {
	ctx      context.Context
	c        *Client
	bucket   string
	key      string
	uploadID string
	partSize int64

	buf       []byte
	partNum   int32
	parts     []s3types.CompletedPart
	total     int64
	sumMD5    [16]byte
	hasher    *streamingMD5
	completed bool
	aborted   bool
}

func (w *streamWriter) Write(p []byte) (int, error) {
	n := len(p)
	w.buf = append(w.buf, p...)
	w.total += int64(n)
	if int64(len(w.buf)) >= w.partSize {
		if err := w.flushPart(false); err != nil {
			return 0, err
		}
	}
	return n, nil
}

func (w *streamWriter) flushPart(final bool) error {
	if len(w.buf) == 0 {
		return nil
	}
	w.partNum++
	out, err := w.c.s3.UploadPart(w.ctx, &s3.UploadPartInput{
		Bucket:     aws.String(w.bucket),
		Key:        aws.String(w.key),
		UploadId:   aws.String(w.uploadID),
		PartNumber: aws.Int32(w.partNum),
		Body:       strings.NewReader(string(w.buf)), // copy once
	})
	if err != nil {
		return fmt.Errorf("upload part %d: %w", w.partNum, err)
	}
	w.parts = append(w.parts, s3types.CompletedPart{
		PartNumber: aws.Int32(w.partNum),
		ETag:       out.ETag,
	})
	if w.hasher == nil {
		w.hasher = newStreamingMD5()
	}
	w.hasher.Write(w.buf)
	w.buf = w.buf[:0]
	_ = final
	return nil
}

func (w *streamWriter) Close() (string, int64, string, error) {
	if w.completed {
		return "", 0, "", fmt.Errorf("stream writer already closed")
	}
	if err := w.flushPart(true); err != nil {
		_ = w.Abort()
		return "", 0, "", err
	}
	out, err := w.c.s3.CompleteMultipartUpload(w.ctx, &s3.CompleteMultipartUploadInput{
		Bucket:          aws.String(w.bucket),
		Key:             aws.String(w.key),
		UploadId:        aws.String(w.uploadID),
		MultipartUpload: &s3types.CompletedMultipartUpload{Parts: w.parts},
	})
	if err != nil {
		_ = w.Abort()
		return "", 0, "", fmt.Errorf("close stream: %w", err)
	}
	w.completed = true
	etag := strings.Trim(aws.ToString(out.ETag), `"`)
	checksum := ""
	if w.hasher != nil {
		w.sumMD5 = w.hasher.Sum()
		checksum = hex.EncodeToString(w.sumMD5[:])
	}
	return etag, w.total, checksum, nil
}

func (w *streamWriter) Abort() error {
	if w.aborted || w.completed {
		return nil
	}
	w.aborted = true
	_, err := w.c.s3.AbortMultipartUpload(w.ctx, &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(w.bucket),
		Key:      aws.String(w.key),
		UploadId: aws.String(w.uploadID),
	})
	if err != nil {
		return fmt.Errorf("abort stream: %w", err)
	}
	return nil
}

// ─── helpers ──────────────────────────────────────────────────────────────

// signedHeaders flattens v4 signed-header values into a {name:value} map.
func signedHeaders(req *v4.PresignedHTTPRequest) map[string]string {
	out := make(map[string]string, len(req.SignedHeader))
	for k, v := range req.SignedHeader {
		if len(v) > 0 {
			out[k] = v[0]
		}
	}
	return out
}

// applySSE populates server-side encryption fields on a PutObjectInput.
func (c *Client) applySSE(in *s3.PutObjectInput) {
	switch strings.ToLower(c.sseType) {
	case "aes256":
		in.ServerSideEncryption = s3types.ServerSideEncryptionAes256
	case "aws:kms":
		in.ServerSideEncryption = s3types.ServerSideEncryptionAwsKms
		if c.sseKey != "" {
			in.SSEKMSKeyId = aws.String(c.sseKey)
		}
	}
}

// applyMultipartSSE mirrors applySSE for CreateMultipartUploadInput.
func (c *Client) applyMultipartSSE(in *s3.CreateMultipartUploadInput) {
	switch strings.ToLower(c.sseType) {
	case "aes256":
		in.ServerSideEncryption = s3types.ServerSideEncryptionAes256
	case "aws:kms":
		in.ServerSideEncryption = s3types.ServerSideEncryptionAwsKms
		if c.sseKey != "" {
			in.SSEKMSKeyId = aws.String(c.sseKey)
		}
	}
}

// ─── streamingMD5: fold across UploadPart calls ────────────────────────────
// md5 is used as a lightweight content digest surfaced back to the caller
// (StreamWriter.Close returns it as `checksum`). It is NOT used for auth.

type streamingMD5 struct {
	h io.Writer
	d interface {
		Sum(b []byte) []byte
	}
}

func newStreamingMD5() *streamingMD5 {
	h := md5.New() // #nosec G401
	return &streamingMD5{h: h, d: h}
}

func (s *streamingMD5) Write(p []byte) {
	_, _ = s.h.Write(p)
}

func (s *streamingMD5) Sum() [16]byte {
	var out [16]byte
	sum := s.d.Sum(nil)
	copy(out[:], sum[:16])
	return out
}

// parseSize makes the linter happy — tests may want to translate human sizes.
func parseSize(s string) int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

var _ = parseSize // retained for future use
