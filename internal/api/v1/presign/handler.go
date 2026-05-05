// Package presign implements the PresignService business logic.
//
// Presign RPCs are distinct from UploadObject: they issue URLs for already-
// registered objects (PUT resume, GET for consumers, multipart parts). They
// enforce Cedar at signing time and never touch object state.
package presign

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/apiutil"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
)

type Config struct {
	DefaultTTL     time.Duration
	MaxTTL         time.Duration
	DefaultMaxSize int64
}

type Storage interface {
	PresignGet(ctx context.Context, bucket string, tenantID uuid.UUID, objectKey, key string, ttl time.Duration, disposition string) (url string, headers map[string]string, expiresAt time.Time, err error)
	PresignPut(ctx context.Context, bucket string, tenantID uuid.UUID, objectKey, key, contentType, checksumAlgo string, ttl time.Duration, sizeHint int64) (url string, headers map[string]string, expiresAt time.Time, err error)
	PresignPart(ctx context.Context, bucket string, tenantID uuid.UUID, storageUploadID, objectKey, key string, partNumber int32, ttl time.Duration) (url string, headers map[string]string, expiresAt time.Time, err error)
}

type Repository interface {
	LookupObjectByName(ctx context.Context, tenantID uuid.UUID, objectKey string, objectID uuid.UUID) (resolvedObjectKey, key, state string, err error)
	LookupMultipartSession(ctx context.Context, uploadID string) (storageUploadID, objectKey, key string, err error)
	// LookupBucket returns the physical S3 bucket bound to an ObjectKey.
	// Used to route presign URLs to the correct bucket.
	LookupBucket(ctx context.Context, tenantID uuid.UUID, objectKey string) (string, error)
}

type Handler struct {
	repo    Repository
	storage Storage
	policy  *cedar.Engine
	cfg     Config
}

func NewHandler(repo Repository, storage Storage, policy *cedar.Engine, cfg Config) *Handler {
	return &Handler{repo: repo, storage: storage, policy: policy, cfg: cfg}
}

func (h *Handler) PresignGet(ctx context.Context, objectName string, ttl time.Duration, disposition string) (string, map[string]string, time.Time, error) {
	tenantID, p, err := apiutil.CallerContext(ctx)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	objectKey, objectID, err := apiutil.ParseObjectName(objectName)
	if err != nil {
		return "", nil, time.Time{}, connect.NewError(connect.CodeInvalidArgument, err)
	}
	objectKey, key, state, err := h.repo.LookupObjectByName(ctx, tenantID, objectKey, objectID)
	if err != nil {
		return "", nil, time.Time{}, connect.NewError(connect.CodeNotFound, err)
	}
	if state != "AVAILABLE" {
		return "", nil, time.Time{}, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("object state %s does not allow GET", state))
	}
	if err := h.authorize(ctx, p, tenantID, objectKey, key, cedar.ActionPresignGet); err != nil {
		return "", nil, time.Time{}, err
	}
	bucket, err := h.repo.LookupBucket(ctx, tenantID, objectKey)
	if err != nil {
		return "", nil, time.Time{}, connect.NewError(connect.CodeNotFound, err)
	}
	return h.storage.PresignGet(ctx, bucket, tenantID, objectKey, key, h.resolveTTL(ttl), disposition)
}

func (h *Handler) PresignPut(ctx context.Context, objectName, contentType, checksumAlgo string, ttl time.Duration, sizeHint int64) (string, map[string]string, time.Time, error) {
	tenantID, p, err := apiutil.CallerContext(ctx)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	objectKey, objectID, err := apiutil.ParseObjectName(objectName)
	if err != nil {
		return "", nil, time.Time{}, connect.NewError(connect.CodeInvalidArgument, err)
	}
	objectKey, key, state, err := h.repo.LookupObjectByName(ctx, tenantID, objectKey, objectID)
	if err != nil {
		return "", nil, time.Time{}, connect.NewError(connect.CodeNotFound, err)
	}
	// Only PENDING objects may receive a fresh upload URL. AVAILABLE objects
	// would silently overwrite committed data; FAILED/DELETED rows are
	// terminal and presigning a PUT against them is meaningless.
	if state != "PENDING" {
		return "", nil, time.Time{}, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("object state %s does not allow PUT", state))
	}
	if err := h.authorize(ctx, p, tenantID, objectKey, key, cedar.ActionPresignPut); err != nil {
		return "", nil, time.Time{}, err
	}
	bucket, err := h.repo.LookupBucket(ctx, tenantID, objectKey)
	if err != nil {
		return "", nil, time.Time{}, connect.NewError(connect.CodeNotFound, err)
	}
	return h.storage.PresignPut(ctx, bucket, tenantID, objectKey, key, contentType, checksumAlgo, h.resolveTTL(ttl), sizeHint)
}

func (h *Handler) PresignPart(ctx context.Context, uploadID string, partNumber int32, ttl time.Duration) (string, map[string]string, time.Time, error) {
	tenantID, p, err := apiutil.CallerContext(ctx)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	storageUploadID, objectKey, key, err := h.repo.LookupMultipartSession(ctx, uploadID)
	if err != nil {
		return "", nil, time.Time{}, connect.NewError(connect.CodeNotFound, err)
	}
	if err := h.authorize(ctx, p, tenantID, objectKey, key, cedar.ActionPresignPut); err != nil {
		return "", nil, time.Time{}, err
	}
	bucket, err := h.repo.LookupBucket(ctx, tenantID, objectKey)
	if err != nil {
		return "", nil, time.Time{}, connect.NewError(connect.CodeNotFound, err)
	}
	return h.storage.PresignPart(ctx, bucket, tenantID, storageUploadID, objectKey, key, partNumber, h.resolveTTL(ttl))
}

func (h *Handler) resolveTTL(requested time.Duration) time.Duration {
	if requested <= 0 {
		return h.cfg.DefaultTTL
	}
	if h.cfg.MaxTTL > 0 && requested > h.cfg.MaxTTL {
		return h.cfg.MaxTTL
	}
	return requested
}

func (h *Handler) authorize(ctx context.Context, p *auth.Principal, tenantID uuid.UUID, objectKey, key, action string) error {
	decision, err := h.policy.IsAuthorized(ctx,
		&cedar.Principal{Subject: p.Subject, TenantID: tenantID, Roles: p.Roles},
		action,
		&cedar.Resource{TenantID: tenantID, ObjectKey: objectKey, Key: key},
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
