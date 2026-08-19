// Package multipart implements the MultipartService business logic.
//
// Lifecycle: Initiate → (PresignPart × N) → Complete|Abort. Session state
// lives in multipart_uploads and multipart_parts; the backing S3-level
// multipart upload is owned by the chosen storage backend.
package multipart

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin-private/capability"
	"github.com/oleg-tkachuk/paladin-private/internal/api/v1/apiutil"
	"github.com/oleg-tkachuk/paladin-private/internal/api/v1/object"
	"github.com/oleg-tkachuk/paladin-private/internal/auth"
	"github.com/oleg-tkachuk/paladin-private/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin-private/internal/statemachine"
)

type Storage interface {
	InitiateMultipart(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, objectKey, key, contentType string) (storageUploadID string, err error)
	CompleteMultipart(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, storageUploadID, objectKey, key string, parts []PartETag) (etag string, sizeBytes int64, err error)
	AbortMultipart(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, storageUploadID, objectKey, key string) error
	PresignPart(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, storageUploadID, objectKey, key string, partNumber int32, ttl time.Duration) (url string, headers map[string]string, expiresAt time.Time, err error)
}

// Part is a record of an uploaded multipart part as stored in multipart_parts.
type Part struct {
	PartNumber int32
	SizeBytes  int64
	ETag       string
	Checksum   string
	UploadedAt time.Time
}

type PartETag struct {
	PartNumber int32
	ETag       string
}

type Session struct {
	UploadID        string
	ObjectID        uuid.UUID
	TenantID        uuid.UUID
	BackendID       string // storage backend the upload was initiated against
	Bucket          string // physical S3 bucket; anchored at initiate time
	ObjectKey       string
	Key             string
	StorageUploadID string
	PartSizeBytes   int64
	TotalParts      int32
	CreatedAt       time.Time
}

type InitiateArgs struct {
	TenantID      uuid.UUID
	ObjectKey     string
	Key           string
	ContentType   string
	TotalParts    int32
	PartSizeBytes int64
	SizeHint      int64
	ChecksumAlgo  string
	Metadata      map[string]string
	Tags          map[string]string
}

type CompleteArgs struct {
	TenantID uuid.UUID
	UploadID string
	Parts    []PartETag
}

type Repository interface {
	InitiateSession(ctx context.Context, args InitiateArgs, objectID uuid.UUID, storageUploadID, backendID, bucket string) (Session, error)
	GetSession(ctx context.Context, uploadID string) (Session, error)
	RecordPart(ctx context.Context, uploadID string, part PartETag, sizeBytes int64, checksum string) error
	DeleteSession(ctx context.Context, uploadID string) error
	GetObjectLocation(ctx context.Context, objectID uuid.UUID) (objectKey, key string, err error)
	// LookupBucket returns the storage backend id and the physical S3 bucket
	// bound to the ObjectKey. Used to route storage calls to the right
	// (backend, bucket); callers that don't route on backend yet may discard
	// backendID (docs/backend-registry.md).
	LookupBucket(ctx context.Context, tenantID uuid.UUID, objectKey string, write bool) (backendID, bucket string, err error)
	// ListParts returns recorded parts for an upload, ordered by part_number.
	// Pagination is keyset on part_number; pageToken is the last seen number.
	ListParts(ctx context.Context, uploadID string, pageSize int32, pageToken string) ([]Part, string, error)
}

// VersionRecorder is the optional hook that records a versions-row when the
// parent bucket has versioning_enabled. Implemented by *object.VersionHandler
// (declared as an interface here to avoid a cycle).
type VersionRecorder interface {
	OnPromote(ctx context.Context, obj VersionedObject) error
}

// VersionedObject is the minimal projection the recorder needs at promote
// time. Mirrors object.Object so the wiring layer can pass the same value
// through both packages without a separate conversion step.
type VersionedObject struct {
	ObjectID     uuid.UUID
	TenantID     uuid.UUID
	ObjectKey    string
	Key          string
	ContentType  string
	SizeBytes    int64
	ETag         string
	ChecksumAlgo string
	Checksum     string
	Metadata     map[string]string
	Tags         map[string]string
}

// QuotaUpdater is the post-promote accounting hook — same shape as the
// one on object.Handler. Multipart and single-PUT promotions share usage
// counters per tenant.
type QuotaUpdater interface {
	OnObjectPromoted(ctx context.Context, tenantID uuid.UUID, sizeBytes int64) error
}

type Handler struct {
	repo     Repository
	storage  Storage
	policy   cedar.Authorizer
	sm       *statemachine.Transitioner
	versions VersionRecorder // optional
	quota    QuotaUpdater    // optional
}

// SetQuotaUpdater attaches the optional usage hook called on
// CompleteMultipartUpload after a successful promotion. nil = no-op.
func (h *Handler) SetQuotaUpdater(q QuotaUpdater) { h.quota = q }

func NewHandler(repo Repository, storage Storage, policy cedar.Authorizer, sm *statemachine.Transitioner) *Handler {
	return &Handler{repo: repo, storage: storage, policy: policy, sm: sm}
}

// SetVersionRecorder attaches the optional recorder used after a successful
// CompleteMultipartUpload promote. Wired by main.
func (h *Handler) SetVersionRecorder(v VersionRecorder) { h.versions = v }

// InitiateMultipartUpload creates the PENDING object row and opens a storage
// multipart session. Handler contract: size_bytes is required here because
// part sizing needs it (unlike UploadObject where it's a hint).
func (h *Handler) InitiateMultipartUpload(ctx context.Context, args InitiateArgs) (*Session, error) {
	tenantID, p, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	args.TenantID = tenantID
	if args.SizeHint <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("size_bytes is required for multipart uploads"))
	}
	objectURI := "object://" + tenantID.String() + "/" + args.ObjectKey + "/" + args.Key
	if err := auth.AssertCapabilityOp(ctx, capability.OpPut, objectURI); err != nil {
		return nil, err
	}
	// Resolve the (backend, bucket) BEFORE authz so a bucket:/object_key:-
	// scoped write PAT enforces on multipart init; the same resolution routes
	// InitiateMultipart and anchors the session below.
	backendID, bucket, err := h.repo.LookupBucket(ctx, tenantID, args.ObjectKey, true) // multipart init (mutation)
	if err != nil {
		return nil, object.MapResolveErr(err)
	}
	if err := h.authorize(ctx, p, tenantID, args.ObjectKey, args.Key, backendID, bucket, cedar.ActionPutObject, args.SizeHint, args.ContentType); err != nil {
		return nil, err
	}

	storageUploadID, err := h.storage.InitiateMultipart(ctx, backendID, bucket, tenantID, args.ObjectKey, args.Key, args.ContentType)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("storage initiate: %w", err))
	}

	objectID := uuid.Must(uuid.NewV7())
	session, err := h.repo.InitiateSession(ctx, args, objectID, storageUploadID, backendID, bucket)
	if err != nil {
		// Best-effort rollback: abort the orphan storage session. Log and
		// proceed — a background sweeper eventually cleans stragglers.
		_ = h.storage.AbortMultipart(ctx, backendID, bucket, tenantID, storageUploadID, args.ObjectKey, args.Key)
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &session, nil
}

func (h *Handler) CompleteMultipartUpload(ctx context.Context, args CompleteArgs) error {
	tenantID, principal, err := apiutil.CallerContext(ctx)
	if err != nil {
		return err
	}
	args.TenantID = tenantID

	sess, err := h.repo.GetSession(ctx, args.UploadID)
	if err != nil {
		return connect.NewError(connect.CodeNotFound, err)
	}
	objectURI := "object://" + tenantID.String() + "/" + sess.ObjectKey + "/" + sess.Key
	if err := auth.AssertCapabilityOp(ctx, capability.OpPut, objectURI); err != nil {
		return err
	}
	// The session anchored its (backend, bucket) at initiate time; pass it to
	// authz so a bucket:/object_key:-scoped PAT enforces on complete.
	if err := h.authorize(ctx, principal, tenantID, sess.ObjectKey, sess.Key, sess.BackendID, sess.Bucket, cedar.ActionPutObject, 0, ""); err != nil {
		return err
	}
	backendID, bucket := sess.BackendID, sess.Bucket
	if bucket == "" {
		backendID, bucket, err = h.repo.LookupBucket(ctx, tenantID, sess.ObjectKey, true) // multipart complete (mutation)
		if err != nil {
			return object.MapResolveErr(err)
		}
	}
	etag, size, err := h.storage.CompleteMultipart(ctx, backendID, bucket, tenantID, sess.StorageUploadID, sess.ObjectKey, sess.Key, args.Parts)
	if err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("storage complete: %w", err))
	}
	// No sequencer from multipart completion — events will supply one later.
	changed, err := h.sm.PromoteToAvailable(ctx, sess.ObjectID, etag, size, "", "", statemachine.SourceRPC)
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	if changed && h.versions != nil {
		_ = h.versions.OnPromote(ctx, VersionedObject{
			ObjectID:    sess.ObjectID,
			TenantID:    sess.TenantID,
			ObjectKey:   sess.ObjectKey,
			Key:         sess.Key,
			SizeBytes:   size,
			ETag:        etag,
			ContentType: "", // multipart doesn't carry CT through Storage; lookup later
		})
	}
	// Quota accounting — symmetric with single-PUT path on object.Handler.
	// Suppressed errors: drift gets corrected by the nightly accounting job;
	// a transient failure must not undo a successful state transition.
	if changed && h.quota != nil {
		_ = h.quota.OnObjectPromoted(ctx, sess.TenantID, size)
	}
	if err := h.repo.DeleteSession(ctx, args.UploadID); err != nil {
		// Session delete failure is non-fatal — the object is AVAILABLE.
		// Cleanup can happen via TTL sweep.
		_ = err
	}
	return nil
}

func (h *Handler) AbortMultipartUpload(ctx context.Context, uploadID string) error {
	tenantID, principal, err := apiutil.CallerContext(ctx)
	if err != nil {
		return err
	}
	sess, err := h.repo.GetSession(ctx, uploadID)
	if err != nil {
		return connect.NewError(connect.CodeNotFound, err)
	}
	objectURI := "object://" + tenantID.String() + "/" + sess.ObjectKey + "/" + sess.Key
	if err := auth.AssertCapabilityOp(ctx, capability.OpDelete, objectURI); err != nil {
		return err
	}
	// Session-anchored (backend, bucket) → authz enforces bucket:/object_key:
	// scopes on abort.
	if err := h.authorize(ctx, principal, tenantID, sess.ObjectKey, sess.Key, sess.BackendID, sess.Bucket, cedar.ActionDeleteObject, 0, ""); err != nil {
		return err
	}
	backendID, bucket := sess.BackendID, sess.Bucket
	if bucket == "" {
		backendID, bucket, err = h.repo.LookupBucket(ctx, tenantID, sess.ObjectKey, true) // abort multipart (mutation)
		if err != nil {
			return object.MapResolveErr(err)
		}
	}
	if err := h.storage.AbortMultipart(ctx, backendID, bucket, tenantID, sess.StorageUploadID, sess.ObjectKey, sess.Key); err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	// Mark the underlying object FAILED so reconciler won't promote it.
	if err := h.sm.MarkFailed(ctx, sess.ObjectID, "multipart-aborted"); err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	if err := h.repo.DeleteSession(ctx, uploadID); err != nil {
		_ = err
	}
	return nil
}

// PresignPart issues a presigned URL for uploading a single part of an
// in-flight multipart session. Authorization is checked against the underlying
// object's (objectKey, key); the storage URL targets the bucket bound to that
// ObjectKey.
func (h *Handler) PresignPart(ctx context.Context, uploadID string, partNumber int32, ttl time.Duration) (string, map[string]string, time.Time, error) {
	tenantID, p, err := apiutil.CallerContext(ctx)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	sess, err := h.repo.GetSession(ctx, uploadID)
	if err != nil {
		return "", nil, time.Time{}, connect.NewError(connect.CodeNotFound, err)
	}
	if sess.TenantID != tenantID {
		return "", nil, time.Time{}, connect.NewError(connect.CodePermissionDenied, errors.New("tenant mismatch"))
	}
	if partNumber <= 0 || (sess.TotalParts > 0 && partNumber > sess.TotalParts) {
		return "", nil, time.Time{}, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("part_number %d out of range (1..%d)", partNumber, sess.TotalParts))
	}
	objectURI := "object://" + tenantID.String() + "/" + sess.ObjectKey + "/" + sess.Key
	// Presigned part URL grants Put on the underlying object; gate on
	// both OpPresign (the act of issuing a URL) and OpPut (the op the
	// URL ultimately authorises). Either failure short-circuits.
	if err := auth.AssertCapabilityOp(ctx, capability.OpPresign, objectURI); err != nil {
		return "", nil, time.Time{}, err
	}
	if err := auth.AssertCapabilityOp(ctx, capability.OpPut, objectURI); err != nil {
		return "", nil, time.Time{}, err
	}
	// Session-anchored (backend, bucket) → authz enforces bucket:/object_key:
	// scopes on the part presign.
	if err := h.authorize(ctx, p, tenantID, sess.ObjectKey, sess.Key, sess.BackendID, sess.Bucket, cedar.ActionPresignPut, 0, ""); err != nil {
		return "", nil, time.Time{}, err
	}
	backendID, bucket := sess.BackendID, sess.Bucket
	if bucket == "" {
		backendID, bucket, err = h.repo.LookupBucket(ctx, tenantID, sess.ObjectKey, true) // presign part (mutation)
		if err != nil {
			return "", nil, time.Time{}, object.MapResolveErr(err)
		}
	}
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	return h.storage.PresignPart(ctx, backendID, bucket, tenantID, sess.StorageUploadID, sess.ObjectKey, sess.Key, partNumber, ttl)
}

// ListParts returns the parts already recorded for an upload session. Used
// during resumption to figure out which part numbers still need uploading.
func (h *Handler) ListParts(ctx context.Context, uploadID string, pageSize int32, pageToken string) ([]Part, string, error) {
	tenantID, principal, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, "", err
	}
	sess, err := h.repo.GetSession(ctx, uploadID)
	if err != nil {
		return nil, "", connect.NewError(connect.CodeNotFound, err)
	}
	if sess.TenantID != tenantID {
		return nil, "", connect.NewError(connect.CodePermissionDenied, errors.New("tenant mismatch"))
	}
	if err := auth.AssertCapabilityOp(ctx, capability.OpList, ""); err != nil {
		return nil, "", err
	}
	// Session-anchored (backend, bucket) → authz enforces bucket:/object_key:
	// read scopes on listing parts.
	if err := h.authorize(ctx, principal, tenantID, sess.ObjectKey, sess.Key, sess.BackendID, sess.Bucket, cedar.ActionGetObject, 0, ""); err != nil {
		return nil, "", err
	}
	if pageSize <= 0 || pageSize > 1000 {
		pageSize = 100
	}
	parts, next, err := h.repo.ListParts(ctx, uploadID, pageSize, pageToken)
	if err != nil {
		return nil, "", connect.NewError(connect.CodeInternal, err)
	}
	return parts, next, nil
}

// authorize runs the Cedar check for a multipart action. backendID/bucket
// carry the resolved physical binding (from the session, or a pre-authz
// LookupBucket on initiate) so the scope-enforcement built-in can confine a
// bucket:/object_key:-scoped PAT to its own bucket. Empty backendID/bucket
// leaves the resource without those scope keys, which only ever denies a
// scoped principal — unscoped/roles-only callers are unaffected.
func (h *Handler) authorize(ctx context.Context, p *auth.Principal, tenantID uuid.UUID, objectKey, key, backendID, bucket, action string, sizeBytes int64, contentType string) error {
	decision, err := h.policy.IsAuthorized(ctx,
		apiutil.CedarPrincipalFor(p, tenantID),
		action,
		&cedar.Resource{TenantID: tenantID, ObjectKey: objectKey, Key: key, BackendID: backendID, BucketName: bucket, SizeBytes: sizeBytes, ContentType: contentType},
		cedar.RequestContext{SizeBytes: sizeBytes, ContentType: contentType, Now: time.Now()},
	)
	if err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("authz: %w", err))
	}
	if decision != cedar.DecisionAllow {
		return connect.NewError(connect.CodePermissionDenied, errors.New("denied by policy"))
	}
	return nil
}
