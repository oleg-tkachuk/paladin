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
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

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
}

type Handler struct {
	repo           Repository
	policy         *cedar.Engine
	defaultBackend string
}

func NewHandler(repo Repository, policy *cedar.Engine, defaultBackend string) *Handler {
	return &Handler{repo: repo, policy: policy, defaultBackend: defaultBackend}
}

func (h *Handler) CreateObjectKey(ctx context.Context, args CreateObjectKeyArgs) (*ObjectKey, error) {
	tenantID, principal, err := authzContext(ctx)
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
	if err := h.authorize(ctx, principal, tenantID, args.ObjectKey, cedar.ActionAdminObjectKey); err != nil {
		return nil, err
	}
	b, err := h.repo.Create(ctx, args)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("create objectKey: %w", err))
	}
	return &b, nil
}

func (h *Handler) GetObjectKey(ctx context.Context, objectKey string) (*ObjectKey, error) {
	tenantID, principal, err := authzContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, principal, tenantID, objectKey, cedar.ActionAdminObjectKey); err != nil {
		return nil, err
	}
	b, err := h.repo.Get(ctx, tenantID, objectKey)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	return &b, nil
}

func (h *Handler) UpdateObjectKey(ctx context.Context, args UpdateObjectKeyArgs) (*ObjectKey, error) {
	tenantID, principal, err := authzContext(ctx)
	if err != nil {
		return nil, err
	}
	args.TenantID = tenantID
	if err := h.authorize(ctx, principal, tenantID, args.ObjectKey, cedar.ActionAdminObjectKey); err != nil {
		return nil, err
	}
	b, err := h.repo.Update(ctx, args)
	if err != nil {
		return nil, mapVersionErr(err)
	}
	return &b, nil
}

func (h *Handler) DeleteObjectKey(ctx context.Context, objectKey string, expectedVersion int64) error {
	tenantID, principal, err := authzContext(ctx)
	if err != nil {
		return err
	}
	if err := h.authorize(ctx, principal, tenantID, objectKey, cedar.ActionAdminObjectKey); err != nil {
		return err
	}
	if err := h.repo.Delete(ctx, tenantID, objectKey, expectedVersion); err != nil {
		return mapVersionErr(err)
	}
	return nil
}

func (h *Handler) ListObjectKeys(ctx context.Context, args ListObjectKeysArgs) ([]ObjectKey, string, error) {
	tenantID, _, err := authzContext(ctx)
	if err != nil {
		return nil, "", err
	}
	args.TenantID = tenantID
	// ListObjectKeys is permitted for any authenticated tenant member; per-objectKey
	// visibility is not filtered through Cedar here to keep pagination cheap.
	return h.repo.List(ctx, args)
}

func (h *Handler) GetObjectKeyStats(ctx context.Context, objectKey string) (*ObjectKeyStats, error) {
	tenantID, principal, err := authzContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, principal, tenantID, objectKey, cedar.ActionAdminObjectKey); err != nil {
		return nil, err
	}
	s, err := h.repo.Stats(ctx, tenantID, objectKey)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &s, nil
}

func authzContext(ctx context.Context) (uuid.UUID, *auth.Principal, error) {
	tenantID, err := auth.TenantFromContext(ctx)
	if err != nil {
		return uuid.Nil, nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return uuid.Nil, nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	return tenantID, p, nil
}

func (h *Handler) authorize(ctx context.Context, p *auth.Principal, tenantID uuid.UUID, objectKey, action string) error {
	decision, err := h.policy.IsAuthorized(ctx,
		&cedar.Principal{Subject: p.Subject, TenantID: tenantID, Roles: p.Roles},
		action,
		&cedar.Resource{TenantID: tenantID, ObjectKey: objectKey},
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
