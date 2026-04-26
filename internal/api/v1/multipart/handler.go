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

	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/internal/statemachine"
)

type Storage interface {
	InitiateMultipart(ctx context.Context, bucket string, tenantID uuid.UUID, objectKey, key, contentType string) (storageUploadID string, err error)
	CompleteMultipart(ctx context.Context, bucket string, tenantID uuid.UUID, storageUploadID, objectKey, key string, parts []PartETag) (etag string, sizeBytes int64, err error)
	AbortMultipart(ctx context.Context, bucket string, tenantID uuid.UUID, storageUploadID, objectKey, key string) error
}

type PartETag struct {
	PartNumber int32
	ETag       string
}

type Session struct {
	UploadID        string
	ObjectID        uuid.UUID
	TenantID        uuid.UUID
	Bucket          string // physical S3 bucket; resolved from ObjectKey row at lookup
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
	InitiateSession(ctx context.Context, args InitiateArgs, objectID uuid.UUID, storageUploadID string) (Session, error)
	GetSession(ctx context.Context, uploadID string) (Session, error)
	RecordPart(ctx context.Context, uploadID string, part PartETag, sizeBytes int64, checksum string) error
	DeleteSession(ctx context.Context, uploadID string) error
	GetObjectLocation(ctx context.Context, objectID uuid.UUID) (objectKey, key string, err error)
	// LookupBucket returns the physical S3 bucket bound to the ObjectKey.
	// Used to route storage calls to the right bucket.
	LookupBucket(ctx context.Context, tenantID uuid.UUID, objectKey string) (string, error)
}

type Handler struct {
	repo    Repository
	storage Storage
	policy  *cedar.Engine
	sm      *statemachine.Transitioner
}

func NewHandler(repo Repository, storage Storage, policy *cedar.Engine, sm *statemachine.Transitioner) *Handler {
	return &Handler{repo: repo, storage: storage, policy: policy, sm: sm}
}

// InitiateMultipartUpload creates the PENDING object row and opens a storage
// multipart session. Handler contract: size_bytes is required here because
// part sizing needs it (unlike UploadObject where it's a hint).
func (h *Handler) InitiateMultipartUpload(ctx context.Context, args InitiateArgs) (*Session, error) {
	tenantID, p, err := callerContext(ctx)
	if err != nil {
		return nil, err
	}
	args.TenantID = tenantID
	if args.SizeHint <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("size_bytes is required for multipart uploads"))
	}
	if err := h.authorize(ctx, p, tenantID, args.ObjectKey, args.Key, cedar.ActionPutObject, args.SizeHint, args.ContentType); err != nil {
		return nil, err
	}

	bucket, err := h.repo.LookupBucket(ctx, tenantID, args.ObjectKey)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	storageUploadID, err := h.storage.InitiateMultipart(ctx, bucket, tenantID, args.ObjectKey, args.Key, args.ContentType)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("storage initiate: %w", err))
	}

	objectID := uuid.Must(uuid.NewV7())
	session, err := h.repo.InitiateSession(ctx, args, objectID, storageUploadID)
	if err != nil {
		// Best-effort rollback: abort the orphan storage session. Log and
		// proceed — a background sweeper eventually cleans stragglers.
		_ = h.storage.AbortMultipart(ctx, bucket, tenantID, storageUploadID, args.ObjectKey, args.Key)
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &session, nil
}

func (h *Handler) CompleteMultipartUpload(ctx context.Context, args CompleteArgs) error {
	tenantID, _, err := callerContext(ctx)
	if err != nil {
		return err
	}
	args.TenantID = tenantID

	sess, err := h.repo.GetSession(ctx, args.UploadID)
	if err != nil {
		return connect.NewError(connect.CodeNotFound, err)
	}
	bucket := sess.Bucket
	if bucket == "" {
		bucket, err = h.repo.LookupBucket(ctx, tenantID, sess.ObjectKey)
		if err != nil {
			return connect.NewError(connect.CodeNotFound, err)
		}
	}
	etag, size, err := h.storage.CompleteMultipart(ctx, bucket, tenantID, sess.StorageUploadID, sess.ObjectKey, sess.Key, args.Parts)
	if err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("storage complete: %w", err))
	}
	// No sequencer from multipart completion — events will supply one later.
	_, err = h.sm.PromoteToAvailable(ctx, sess.ObjectID, etag, size, "", "", statemachine.SourceRPC)
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	if err := h.repo.DeleteSession(ctx, args.UploadID); err != nil {
		// Session delete failure is non-fatal — the object is AVAILABLE.
		// Cleanup can happen via TTL sweep.
		_ = err
	}
	return nil
}

func (h *Handler) AbortMultipartUpload(ctx context.Context, uploadID string) error {
	tenantID, _, err := callerContext(ctx)
	if err != nil {
		return err
	}
	sess, err := h.repo.GetSession(ctx, uploadID)
	if err != nil {
		return connect.NewError(connect.CodeNotFound, err)
	}
	bucket := sess.Bucket
	if bucket == "" {
		bucket, err = h.repo.LookupBucket(ctx, tenantID, sess.ObjectKey)
		if err != nil {
			return connect.NewError(connect.CodeNotFound, err)
		}
	}
	if err := h.storage.AbortMultipart(ctx, bucket, tenantID, sess.StorageUploadID, sess.ObjectKey, sess.Key); err != nil {
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

func (h *Handler) authorize(ctx context.Context, p *auth.Principal, tenantID uuid.UUID, objectKey, key, action string, sizeBytes int64, contentType string) error {
	decision, err := h.policy.IsAuthorized(ctx,
		&cedar.Principal{Subject: p.Subject, TenantID: tenantID, Roles: p.Roles},
		action,
		&cedar.Resource{TenantID: tenantID, ObjectKey: objectKey, Key: key, SizeBytes: sizeBytes, ContentType: contentType},
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

func callerContext(ctx context.Context) (uuid.UUID, *auth.Principal, error) {
	t, err := auth.TenantFromContext(ctx)
	if err != nil {
		return uuid.Nil, nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return uuid.Nil, nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	return t, p, nil
}
