package s3adapter

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/multiparth"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
)

// Routers implement the handler-facing storage interfaces (object.Storage —
// which presign.Storage is a subset of — multipart.Storage,
// bucketh.Provisioner)
// by resolving the target *Client from a BackendRegistry per call and
// delegating. The backend id travels in the method args — either a BackendID
// field (presign args / Location) or a leading backendID parameter — so a
// single deployment routes on the object's own backend without the client
// knowing about the registry. On a one-backend config every call resolves to
// the same client, so behaviour is identical to the pre-registry wiring.

// Compile-time interface conformance (bucketh.Provisioner is checked at the
// wire.Storage assignment to avoid importing the bucket package here).
var _ objecth.Storage = (*ObjectRouter)(nil)

// ObjectRouter routes object.Storage calls by backend id.
type ObjectRouter struct{ reg *BackendRegistry }

func NewObjectRouter(reg *BackendRegistry) *ObjectRouter { return &ObjectRouter{reg: reg} }

func (r *ObjectRouter) PresignPut(ctx context.Context, a objecth.PresignPutArgs) (string, map[string]string, time.Time, error) {
	c, err := r.reg.For(ctx, a.BackendID)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	return c.PresignPut(ctx, a)
}

func (r *ObjectRouter) PresignPost(ctx context.Context, a objecth.PresignPostArgs) (string, map[string]string, time.Time, error) {
	c, err := r.reg.For(ctx, a.BackendID)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	return c.PresignPost(ctx, a)
}

func (r *ObjectRouter) PresignGet(ctx context.Context, a objecth.PresignGetArgs) (string, map[string]string, time.Time, error) {
	c, err := r.reg.For(ctx, a.BackendID)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	return c.PresignGet(ctx, a)
}

func (r *ObjectRouter) Head(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, collection, key, checksumAlgo string) (string, int64, string, string, error) {
	c, err := r.reg.For(ctx, backendID)
	if err != nil {
		return "", 0, "", "", err
	}
	return c.Head(ctx, bucket, tenantID, collection, key, checksumAlgo)
}

func (r *ObjectRouter) CopyObject(ctx context.Context, src, dst objecth.Location) error {
	// Same backend: a server-side S3 CopyObject runs on one client (no bytes
	// flow through this process).
	if src.BackendID == dst.BackendID {
		c, err := r.reg.For(ctx, dst.BackendID)
		if err != nil {
			return err
		}
		return c.CopyObject(ctx, src, dst)
	}
	// Cross-backend (ADR-0015 Phase 3 slice 3): the two objects live on
	// different S3 endpoints, so stream through — GET from the source client
	// into the destination's multipart writer, which parts the body so
	// arbitrarily large objects copy without buffering the whole thing.
	return r.streamThrough(ctx, src, dst)
}

func (r *ObjectRouter) streamThrough(ctx context.Context, src, dst objecth.Location) error {
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

// MultipartRouter routes multipart.Storage calls by backend id.
type MultipartRouter struct{ reg *BackendRegistry }

func NewMultipartRouter(reg *BackendRegistry) *MultipartRouter { return &MultipartRouter{reg: reg} }

func (r *MultipartRouter) InitiateMultipart(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, collection, key, contentType, checksumAlgo string) (string, error) {
	c, err := r.reg.For(ctx, backendID)
	if err != nil {
		return "", err
	}
	return c.InitiateMultipart(ctx, bucket, tenantID, collection, key, contentType, checksumAlgo)
}

func (r *MultipartRouter) CompleteMultipart(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, storageUploadID, collection, key, checksumAlgo string, parts []multiparth.PartETag) (string, int64, error) {
	c, err := r.reg.For(ctx, backendID)
	if err != nil {
		return "", 0, err
	}
	return c.CompleteMultipart(ctx, bucket, tenantID, storageUploadID, collection, key, checksumAlgo, parts)
}

func (r *MultipartRouter) ListMultipartParts(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, storageUploadID, collection, key string, maxParts, afterPartNumber int32) ([]multiparth.Part, int32, error) {
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

func (r *MultipartRouter) DeleteObject(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, collection, key string) error {
	c, err := r.reg.For(ctx, backendID)
	if err != nil {
		return err
	}
	return c.DeleteObject(ctx, bucket, tenantID, collection, key)
}

func (r *MultipartRouter) PresignPart(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, storageUploadID, collection, key string, part multiparth.PartBinding, ttl time.Duration) (string, map[string]string, time.Time, error) {
	c, err := r.reg.For(ctx, backendID)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	return c.PresignPart(ctx, bucket, tenantID, storageUploadID, collection, key, part, ttl)
}

// ProvisionerRouter routes bucketh.Provisioner calls by backend id. The backend
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

// SetAnonymousReadPolicy routes to the backend holding the bucket (ADR-0027).
func (r *ProvisionerRouter) SetAnonymousReadPolicy(ctx context.Context, backendID, bucketName string) error {
	c, err := r.reg.For(ctx, backendID)
	if err != nil {
		return err
	}
	return c.SetAnonymousReadPolicy(ctx, bucketName)
}
