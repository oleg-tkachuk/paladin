package s3adapter

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/multipart"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/object"
)

// Routers implement the handler-facing storage interfaces (object.Storage,
// presign.Storage, multipart.Storage, object.StreamSink, bucket.Provisioner)
// by resolving the target *Client from a BackendRegistry per call and
// delegating. The backend id travels in the method args — either a BackendID
// field (presign args / Location) or a leading backendID parameter — so a
// single deployment routes on the object's own backend without the client
// knowing about the registry. On a one-backend config every call resolves to
// the same client, so behaviour is identical to the pre-registry wiring.
//
// PresignGet/PresignPut appear on more than one interface with different
// signatures, so each interface gets its own router type (one struct cannot
// carry two same-named methods).

// Compile-time interface conformance (bucket.Provisioner is checked at the
// wire.Storage assignment to avoid importing the bucket package here).
var (
	_ object.Storage    = (*ObjectRouter)(nil)
	_ object.StreamSink = (*StreamRouter)(nil)
)

// ObjectRouter routes object.Storage calls by backend id.
type ObjectRouter struct{ reg *BackendRegistry }

func NewObjectRouter(reg *BackendRegistry) *ObjectRouter { return &ObjectRouter{reg: reg} }

func (r *ObjectRouter) PresignPut(ctx context.Context, a object.PresignPutArgs) (string, map[string]string, time.Time, error) {
	c, err := r.reg.For(ctx, a.BackendID)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	return c.PresignPut(ctx, a)
}

func (r *ObjectRouter) PresignPost(ctx context.Context, a object.PresignPostArgs) (string, map[string]string, time.Time, error) {
	c, err := r.reg.For(ctx, a.BackendID)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	return c.PresignPost(ctx, a)
}

func (r *ObjectRouter) PresignGet(ctx context.Context, a object.PresignGetArgs) (string, map[string]string, time.Time, error) {
	c, err := r.reg.For(ctx, a.BackendID)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	return c.PresignGet(ctx, a)
}

func (r *ObjectRouter) Head(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, collection, key string) (string, int64, string, string, error) {
	c, err := r.reg.For(ctx, backendID)
	if err != nil {
		return "", 0, "", "", err
	}
	return c.Head(ctx, bucket, tenantID, collection, key)
}

func (r *ObjectRouter) CopyObject(ctx context.Context, src, dst object.Location) error {
	// Same backend: a server-side S3 CopyObject runs on one client (no bytes
	// flow through this process).
	if src.BackendID == dst.BackendID {
		c, err := r.reg.For(ctx, dst.BackendID)
		if err != nil {
			return err
		}
		return c.CopyObject(ctx, src, dst)
	}
	// Cross-backend (ADR-0011 Phase 3 slice 3): the two objects live on
	// different S3 endpoints, so stream through — GET from the source client
	// into the destination's multipart writer, which parts the body so
	// arbitrarily large objects copy without buffering the whole thing.
	return r.streamThrough(ctx, src, dst)
}

func (r *ObjectRouter) streamThrough(ctx context.Context, src, dst object.Location) error {
	srcC, err := r.reg.For(ctx, src.BackendID)
	if err != nil {
		return fmt.Errorf("stream copy: source backend: %w", err)
	}
	dstC, err := r.reg.For(ctx, dst.BackendID)
	if err != nil {
		return fmt.Errorf("stream copy: dest backend: %w", err)
	}
	reader, contentType, err := srcC.GetStream(ctx, src.Bucket, src.TenantID, src.Collection, src.Key)
	if err != nil {
		return fmt.Errorf("stream copy: open source: %w", err)
	}
	defer func() { _ = reader.Close() }()

	w, err := dstC.Open(ctx, dst.Bucket, dst.TenantID, dst.Collection, dst.Key, contentType, 0)
	if err != nil {
		return fmt.Errorf("stream copy: open dest: %w", err)
	}
	if _, err := io.Copy(w, reader); err != nil {
		_ = w.Abort()
		return fmt.Errorf("stream copy: transfer: %w", err)
	}
	if _, _, _, err := w.Close(); err != nil {
		return fmt.Errorf("stream copy: finalize dest: %w", err)
	}
	return nil
}

func (r *ObjectRouter) DeleteObject(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, collection, key string) error {
	c, err := r.reg.For(ctx, backendID)
	if err != nil {
		return err
	}
	return c.DeleteObject(ctx, bucket, tenantID, collection, key)
}

// PresignRouter routes presign.Storage calls by backend id.
type PresignRouter struct{ reg *BackendRegistry }

func NewPresignRouter(reg *BackendRegistry) *PresignRouter { return &PresignRouter{reg: reg} }

func (r *PresignRouter) PresignGet(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, collection, key string, ttl time.Duration, disposition string) (string, map[string]string, time.Time, error) {
	c, err := r.reg.For(ctx, backendID)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	return c.Presign().PresignGet(ctx, bucket, tenantID, collection, key, ttl, disposition)
}

func (r *PresignRouter) PresignPut(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, collection, key, contentType, checksumAlgo string, ttl time.Duration, sizeHint int64) (string, map[string]string, time.Time, error) {
	c, err := r.reg.For(ctx, backendID)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	return c.Presign().PresignPut(ctx, bucket, tenantID, collection, key, contentType, checksumAlgo, ttl, sizeHint)
}

func (r *PresignRouter) PresignPart(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, storageUploadID, collection, key string, partNumber int32, ttl time.Duration) (string, map[string]string, time.Time, error) {
	c, err := r.reg.For(ctx, backendID)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	return c.Presign().PresignPart(ctx, bucket, tenantID, storageUploadID, collection, key, partNumber, ttl)
}

// MultipartRouter routes multipart.Storage calls by backend id.
type MultipartRouter struct{ reg *BackendRegistry }

func NewMultipartRouter(reg *BackendRegistry) *MultipartRouter { return &MultipartRouter{reg: reg} }

func (r *MultipartRouter) InitiateMultipart(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, collection, key, contentType string) (string, error) {
	c, err := r.reg.For(ctx, backendID)
	if err != nil {
		return "", err
	}
	return c.InitiateMultipart(ctx, bucket, tenantID, collection, key, contentType)
}

func (r *MultipartRouter) CompleteMultipart(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, storageUploadID, collection, key string, parts []multipart.PartETag) (string, int64, error) {
	c, err := r.reg.For(ctx, backendID)
	if err != nil {
		return "", 0, err
	}
	return c.CompleteMultipart(ctx, bucket, tenantID, storageUploadID, collection, key, parts)
}

func (r *MultipartRouter) ListMultipartParts(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, storageUploadID, collection, key string, maxParts, afterPartNumber int32) ([]multipart.Part, int32, error) {
	c, err := r.reg.For(ctx, backendID)
	if err != nil {
		return nil, 0, err
	}
	return c.ListMultipartParts(ctx, bucket, tenantID, storageUploadID, collection, key, maxParts, afterPartNumber)
}

func (r *MultipartRouter) AbortMultipart(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, storageUploadID, collection, key string) error {
	c, err := r.reg.For(ctx, backendID)
	if err != nil {
		return err
	}
	return c.AbortMultipart(ctx, bucket, tenantID, storageUploadID, collection, key)
}

func (r *MultipartRouter) PresignPart(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, storageUploadID, collection, key string, partNumber int32, ttl time.Duration) (string, map[string]string, time.Time, error) {
	c, err := r.reg.For(ctx, backendID)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	return c.PresignPart(ctx, bucket, tenantID, storageUploadID, collection, key, partNumber, ttl)
}

// StreamRouter routes object.StreamSink calls by backend id.
type StreamRouter struct{ reg *BackendRegistry }

func NewStreamRouter(reg *BackendRegistry) *StreamRouter { return &StreamRouter{reg: reg} }

func (r *StreamRouter) Open(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, collection, key, contentType string, sizeHint int64) (object.StreamWriter, error) {
	c, err := r.reg.For(ctx, backendID)
	if err != nil {
		return nil, err
	}
	return c.Open(ctx, bucket, tenantID, collection, key, contentType, sizeHint)
}

// ProvisionerRouter routes bucket.Provisioner calls by backend id. The backend
// id is already an explicit parameter on this interface, so routing is a
// straight lookup; the resolved client's own CreateBucket/DeleteBucket ignore
// their backendID argument (it targets the client it was built for).
type ProvisionerRouter struct{ reg *BackendRegistry }

func NewProvisionerRouter(reg *BackendRegistry) *ProvisionerRouter {
	return &ProvisionerRouter{reg: reg}
}

func (r *ProvisionerRouter) CreateBucket(ctx context.Context, backendID, bucketName, region string) error {
	c, err := r.reg.For(ctx, backendID)
	if err != nil {
		return err
	}
	return c.CreateBucket(ctx, backendID, bucketName, region)
}

func (r *ProvisionerRouter) DeleteBucket(ctx context.Context, backendID, bucketName string) error {
	c, err := r.reg.For(ctx, backendID)
	if err != nil {
		return err
	}
	return c.DeleteBucket(ctx, backendID, bucketName)
}

func (r *ProvisionerRouter) TagBucketOwner(ctx context.Context, backendID, bucketName string, tenantID uuid.UUID) error {
	c, err := r.reg.For(ctx, backendID)
	if err != nil {
		return err
	}
	return c.TagBucketOwner(ctx, backendID, bucketName, tenantID)
}
