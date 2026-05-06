// Package objectKey implements the ObjectKeyService business logic.
//
// Buckets are logical namespaces mapped onto a physical storage backend.
// Create/Update/Delete operations go through Cedar authorization. Delete is
// restricted if any non-DELETED objects still reference the objectKey (FK).
package objectkey

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/apiutil"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
)

type ObjectKey struct {
	TenantID        uuid.UUID
	ObjectKey       string
	DisplayName     string
	BackendID       string
	BucketName      string
	CedarPolicy     string
	LifecycleRules  []byte // JSONB bytes; parsed by caller if needed
	ResourceVersion int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type ObjectKeyStats struct {
	ObjectCountAvailable int64
	ObjectCountPending   int64
	ObjectCountDeleted   int64
	SizeBytesAvailable   int64
}

type CreateObjectKeyArgs struct {
	TenantID       uuid.UUID
	ObjectKey      string
	DisplayName    string
	BackendID      string
	BucketName     string
	CedarPolicy    string
	LifecycleRules []byte
}

type UpdateObjectKeyArgs struct {
	TenantID        uuid.UUID
	ObjectKey       string
	ExpectedVersion int64
	DisplayName     *string
	CedarPolicy     *string
	LifecycleRules  []byte
}

type ListObjectKeysArgs struct {
	TenantID  uuid.UUID
	PageSize  int32
	PageToken string
}

type Repository interface {
	Create(ctx context.Context, args CreateObjectKeyArgs) (ObjectKey, error)
	Get(ctx context.Context, tenantID uuid.UUID, objectKey string) (ObjectKey, error)
	Update(ctx context.Context, args UpdateObjectKeyArgs) (ObjectKey, error)
	Delete(ctx context.Context, tenantID uuid.UUID, objectKey string, expectedVersion int64) error
	List(ctx context.Context, args ListObjectKeysArgs) ([]ObjectKey, string, error)
	Stats(ctx context.Context, tenantID uuid.UUID, objectKey string) (ObjectKeyStats, error)
	// Rebind atomically swaps the (backend_id, bucket_name) target. DB
	// trigger enforces tenancy on single-tenant buckets.
	Rebind(ctx context.Context, tenantID uuid.UUID, objectKey, backendID, bucketName string, expectedVersion int64) error
}

type Handler struct {
	repo           Repository
	policy         cedar.Authorizer
	defaultBackend string
}

func NewHandler(repo Repository, policy cedar.Authorizer, defaultBackend string) *Handler {
	return &Handler{repo: repo, policy: policy, defaultBackend: defaultBackend}
}

func (h *Handler) CreateObjectKey(ctx context.Context, args CreateObjectKeyArgs) (*ObjectKey, error) {
	tenantID, principal, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	args.TenantID = tenantID
	// Fall back to the configured default backend when the caller omits it.
	// The backend name is a FK to storage_backends.id, so an empty string
	// would fail the constraint.
	if args.BackendID == "" {
		args.BackendID = h.defaultBackend
	}
	if err := h.authorizeFull(ctx, principal, tenantID, args.ObjectKey, args.BackendID, args.BucketName, cedar.ActionManageObjectKey); err != nil {
		return nil, err
	}
	b, err := h.repo.Create(ctx, args)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("create objectKey: %w", err))
	}
	return &b, nil
}

func (h *Handler) GetObjectKey(ctx context.Context, objectKey string) (*ObjectKey, error) {
	tenantID, principal, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, principal, tenantID, objectKey, cedar.ActionManageObjectKey); err != nil {
		return nil, err
	}
	b, err := h.repo.Get(ctx, tenantID, objectKey)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	return &b, nil
}

func (h *Handler) UpdateObjectKey(ctx context.Context, args UpdateObjectKeyArgs) (*ObjectKey, error) {
	tenantID, principal, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	args.TenantID = tenantID
	if err := h.authorize(ctx, principal, tenantID, args.ObjectKey, cedar.ActionManageObjectKey); err != nil {
		return nil, err
	}
	b, err := h.repo.Update(ctx, args)
	if err != nil {
		return nil, mapVersionErr(err)
	}
	return &b, nil
}

func (h *Handler) DeleteObjectKey(ctx context.Context, objectKey string, expectedVersion int64) error {
	tenantID, principal, err := apiutil.CallerContext(ctx)
	if err != nil {
		return err
	}
	if err := h.authorize(ctx, principal, tenantID, objectKey, cedar.ActionManageObjectKey); err != nil {
		return err
	}
	if err := h.repo.Delete(ctx, tenantID, objectKey, expectedVersion); err != nil {
		return mapVersionErr(err)
	}
	return nil
}

func (h *Handler) ListObjectKeys(ctx context.Context, args ListObjectKeysArgs) ([]ObjectKey, string, error) {
	tenantID, principal, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, "", err
	}
	args.TenantID = tenantID
	// One tenant-scoped Cedar check up front; per-row filtering would
	// dominate pagination cost so we don't repeat it for every objectKey.
	if err := h.authorize(ctx, principal, tenantID, "", cedar.ActionManageObjectKey); err != nil {
		return nil, "", err
	}
	return h.repo.List(ctx, args)
}

func (h *Handler) GetObjectKeyStats(ctx context.Context, objectKey string) (*ObjectKeyStats, error) {
	tenantID, principal, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, principal, tenantID, objectKey, cedar.ActionManageObjectKey); err != nil {
		return nil, err
	}
	s, err := h.repo.Stats(ctx, tenantID, objectKey)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &s, nil
}

// BindObjectKeyToBucket rebinds the namespace to a different bucket. Cedar
// authorization uses ActionBindObjectKeyToBucket on the namespace.
func (h *Handler) BindObjectKeyToBucket(ctx context.Context, objectKey, bucket string, expectedVersion int64) (*ObjectKey, error) {
	tenantID, p, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	backendID, bucketName, err := splitBucketResourceName(bucket)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := h.authorizeFull(ctx, p, tenantID, objectKey, backendID, bucketName, cedar.ActionBindObjectKeyToBucket); err != nil {
		return nil, err
	}
	if err := h.repo.Rebind(ctx, tenantID, objectKey, backendID, bucketName, expectedVersion); err != nil {
		if errors.Is(err, ErrVersionMismatch) {
			return nil, connect.NewError(connect.CodeAborted, err)
		}
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("rebind: %w", err))
	}
	updated, err := h.repo.Get(ctx, tenantID, objectKey)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &updated, nil
}

// splitBucketResourceName parses "storageBackends/{backend}/buckets/{bucket}".
func splitBucketResourceName(name string) (backend, bucket string, err error) {
	parts := strings.Split(name, "/")
	if len(parts) != 4 || parts[0] != "storageBackends" || parts[2] != "buckets" {
		return "", "", fmt.Errorf("invalid bucket name %q", name)
	}
	return parts[1], parts[3], nil
}

func (h *Handler) authorize(ctx context.Context, p *auth.Principal, tenantID uuid.UUID, objectKey, action string) error {
	return h.authorizeFull(ctx, p, tenantID, objectKey, "", "", action)
}

// authorizeFull is the bucket-aware variant that passes the bucket binding
// to Cedar so the engine emits the full ObjectKey←Bucket←StorageBackend
// hierarchy. Use whenever the caller has the binding in hand (Create,
// BindObjectKeyToBucket, post-Get on Update).
func (h *Handler) authorizeFull(
	ctx context.Context,
	p *auth.Principal,
	tenantID uuid.UUID,
	objectKey, backendID, bucketName, action string,
) error {
	decision, err := h.policy.IsAuthorized(ctx,
		&cedar.Principal{Subject: p.Subject, TenantID: tenantID, Roles: p.Roles},
		action,
		&cedar.Resource{
			TenantID:   tenantID,
			ObjectKey:  objectKey,
			BackendID:  backendID,
			BucketName: bucketName,
		},
		cedar.RequestContext{Now: time.Now()},
	)
	if err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("authz: %w", err))
	}
	if decision != cedar.DecisionAllow {
		return connect.NewError(connect.CodePermissionDenied, errors.New("denied by policy"))
	}
	return nil
}

// ErrVersionMismatch is returned when optimistic-concurrency control fails.
// Repositories should surface it so handlers can map to CodeAborted.
var ErrVersionMismatch = errors.New("resource_version mismatch")

func mapVersionErr(err error) error {
	if errors.Is(err, ErrVersionMismatch) {
		return connect.NewError(connect.CodeAborted, err)
	}
	return connect.NewError(connect.CodeInternal, err)
}
