package s3adapter

import (
	"context"
	"fmt"
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

func (r *ObjectRouter) Head(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, objectKey, key string) (string, int64, string, string, error) {
	c, err := r.reg.For(ctx, backendID)
	if err != nil {
		return "", 0, "", "", err
	}
	return c.Head(ctx, bucket, tenantID, objectKey, key)
}

func (r *ObjectRouter) CopyObject(ctx context.Context, src, dst object.Location) error {
	// A server-side S3 copy runs on a single client, so src and dst must be
	// on the same backend. Cross-backend copy (a stream-through GET→PUT) is
	// Phase 3 (the shared→dedicated migration job); refuse it loudly here
	// rather than silently copying within the dst backend.
	if src.BackendID != dst.BackendID {
		return fmt.Errorf("s3 router: cross-backend copy not supported yet (src backend %q, dst backend %q)", src.BackendID, dst.BackendID)
	}
	c, err := r.reg.For(ctx, dst.BackendID)
	if err != nil {
		return err
	}
	return c.CopyObject(ctx, src, dst)
}

func (r *ObjectRouter) DeleteObject(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, objectKey, key string) error {
	c, err := r.reg.For(ctx, backendID)
	if err != nil {
		return err
	}
	return c.DeleteObject(ctx, bucket, tenantID, objectKey, key)
}

// PresignRouter routes presign.Storage calls by backend id.
type PresignRouter struct{ reg *BackendRegistry }

func NewPresignRouter(reg *BackendRegistry) *PresignRouter { return &PresignRouter{reg: reg} }

func (r *PresignRouter) PresignGet(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, objectKey, key string, ttl time.Duration, disposition string) (string, map[string]string, time.Time, error) {
	c, err := r.reg.For(ctx, backendID)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	return c.Presign().PresignGet(ctx, bucket, tenantID, objectKey, key, ttl, disposition)
}

func (r *PresignRouter) PresignPut(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, objectKey, key, contentType, checksumAlgo string, ttl time.Duration, sizeHint int64) (string, map[string]string, time.Time, error) {
	c, err := r.reg.For(ctx, backendID)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	return c.Presign().PresignPut(ctx, bucket, tenantID, objectKey, key, contentType, checksumAlgo, ttl, sizeHint)
}

func (r *PresignRouter) PresignPart(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, storageUploadID, objectKey, key string, partNumber int32, ttl time.Duration) (string, map[string]string, time.Time, error) {
	c, err := r.reg.For(ctx, backendID)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	return c.Presign().PresignPart(ctx, bucket, tenantID, storageUploadID, objectKey, key, partNumber, ttl)
}

// MultipartRouter routes multipart.Storage calls by backend id.
type MultipartRouter struct{ reg *BackendRegistry }

func NewMultipartRouter(reg *BackendRegistry) *MultipartRouter { return &MultipartRouter{reg: reg} }

func (r *MultipartRouter) InitiateMultipart(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, objectKey, key, contentType string) (string, error) {
	c, err := r.reg.For(ctx, backendID)
	if err != nil {
		return "", err
	}
	return c.InitiateMultipart(ctx, bucket, tenantID, objectKey, key, contentType)
}

func (r *MultipartRouter) CompleteMultipart(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, storageUploadID, objectKey, key string, parts []multipart.PartETag) (string, int64, error) {
	c, err := r.reg.For(ctx, backendID)
	if err != nil {
		return "", 0, err
	}
	return c.CompleteMultipart(ctx, bucket, tenantID, storageUploadID, objectKey, key, parts)
}

func (r *MultipartRouter) AbortMultipart(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, storageUploadID, objectKey, key string) error {
	c, err := r.reg.For(ctx, backendID)
	if err != nil {
		return err
	}
	return c.AbortMultipart(ctx, bucket, tenantID, storageUploadID, objectKey, key)
}

func (r *MultipartRouter) PresignPart(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, storageUploadID, objectKey, key string, partNumber int32, ttl time.Duration) (string, map[string]string, time.Time, error) {
	c, err := r.reg.For(ctx, backendID)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	return c.PresignPart(ctx, bucket, tenantID, storageUploadID, objectKey, key, partNumber, ttl)
}

// StreamRouter routes object.StreamSink calls by backend id.
type StreamRouter struct{ reg *BackendRegistry }

func NewStreamRouter(reg *BackendRegistry) *StreamRouter { return &StreamRouter{reg: reg} }

func (r *StreamRouter) Open(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, objectKey, key, contentType string, sizeHint int64) (object.StreamWriter, error) {
	c, err := r.reg.For(ctx, backendID)
	if err != nil {
		return nil, err
	}
	return c.Open(ctx, bucket, tenantID, objectKey, key, contentType, sizeHint)
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
