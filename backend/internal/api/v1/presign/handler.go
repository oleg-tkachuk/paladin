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
	"github.com/oleg-tkachuk/paladin/internal/api/v1/object"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/capability"
	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
)

type Config struct {
	DefaultTTL     time.Duration
	MaxTTL         time.Duration
	DefaultMaxSize int64
}

// Safety bounds applied when Config leaves TTLs unset. A presigned URL
// bypasses PALADIN and hits S3 directly, so it cannot be revoked before it
// expires — an unbounded MaxTTL (the zero value) would let a caller mint
// effectively permanent links. These ceilings are the last line of
// defense; operators tune the real values via config.
const (
	fallbackDefaultTTL = 1 * time.Hour
	fallbackMaxTTL     = 7 * 24 * time.Hour
)

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
	LookupBucket(ctx context.Context, tenantID uuid.UUID, objectKey string, write bool) (backendID, bucket string, err error)
}

type Handler struct {
	repo    Repository
	storage Storage
	policy  cedar.Authorizer
	cfg     Config
}

func NewHandler(repo Repository, storage Storage, policy cedar.Authorizer, cfg Config) *Handler {
	if cfg.MaxTTL <= 0 {
		cfg.MaxTTL = fallbackMaxTTL
	}
	if cfg.DefaultTTL <= 0 {
		cfg.DefaultTTL = fallbackDefaultTTL
	}
	// A default above the ceiling makes no sense — clamp it.
	if cfg.DefaultTTL > cfg.MaxTTL {
		cfg.DefaultTTL = cfg.MaxTTL
	}
	return &Handler{repo: repo, storage: storage, policy: policy, cfg: cfg}
}

func (h *Handler) PresignGet(ctx context.Context, objectKey, objectIDStr string, ttl time.Duration, disposition string) (string, map[string]string, time.Time, error) {
	tenantID, p, err := apiutil.CallerContext(ctx)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	if objectKey == "" || objectIDStr == "" {
		return "", nil, time.Time{}, connect.NewError(connect.CodeInvalidArgument, errors.New("object_key and object_id are required"))
	}
	objectID, err := uuid.Parse(objectIDStr)
	if err != nil {
		return "", nil, time.Time{}, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid object_id: %w", err))
	}
	objectKey, key, state, err := h.repo.LookupObjectByName(ctx, tenantID, objectKey, objectID)
	if err != nil {
		return "", nil, time.Time{}, connect.NewError(connect.CodeNotFound, err)
	}
	if state != "AVAILABLE" {
		return "", nil, time.Time{}, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("object state %s does not allow GET", state))
	}
	objectURI := "object://" + tenantID.String() + "/" + objectKey + "/" + key
	// Presigned GET URL grants OpGet on the underlying object; gate
	// on both OpPresign (the act of issuing a URL) and OpGet (the op
	// the URL ultimately authorises).
	if err := auth.AssertCapabilityOp(ctx, capability.OpPresign, objectURI); err != nil {
		return "", nil, time.Time{}, err
	}
	if err := auth.AssertCapabilityOp(ctx, capability.OpGet, objectURI); err != nil {
		return "", nil, time.Time{}, err
	}
	if err := h.authorize(ctx, p, tenantID, objectKey, key, cedar.ActionPresignGet); err != nil {
		return "", nil, time.Time{}, err
	}
	_, bucket, err := h.repo.LookupBucket(ctx, tenantID, objectKey, false) // presign GET (read); positional PresignGet routes in step 3
	if err != nil {
		return "", nil, time.Time{}, object.MapResolveErr(err)
	}
	// Capability budget burn — gates issuance for over-budget callers
	// before we hand them a usable presigned URL. No-op when the
	// caller is JWT-authenticated or ChargePerRequest is 0.
	if err := auth.ChargeRequest(ctx); err != nil {
		return "", nil, time.Time{}, err
	}
	return h.storage.PresignGet(ctx, bucket, tenantID, objectKey, key, h.resolveTTL(ttl), disposition)
}

func (h *Handler) PresignPut(ctx context.Context, objectKey, objectIDStr, contentType, checksumAlgo string, ttl time.Duration, sizeHint int64) (string, map[string]string, time.Time, error) {
	tenantID, p, err := apiutil.CallerContext(ctx)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	if objectKey == "" || objectIDStr == "" {
		return "", nil, time.Time{}, connect.NewError(connect.CodeInvalidArgument, errors.New("object_key and object_id are required"))
	}
	objectID, err := uuid.Parse(objectIDStr)
	if err != nil {
		return "", nil, time.Time{}, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid object_id: %w", err))
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
	objectURI := "object://" + tenantID.String() + "/" + objectKey + "/" + key
	if err := auth.AssertCapabilityOp(ctx, capability.OpPresign, objectURI); err != nil {
		return "", nil, time.Time{}, err
	}
	if err := auth.AssertCapabilityOp(ctx, capability.OpPut, objectURI); err != nil {
		return "", nil, time.Time{}, err
	}
	if err := h.authorize(ctx, p, tenantID, objectKey, key, cedar.ActionPresignPut); err != nil {
		return "", nil, time.Time{}, err
	}
	_, bucket, err := h.repo.LookupBucket(ctx, tenantID, objectKey, true) // presign PUT (mutation); positional PresignPut routes in step 3
	if err != nil {
		return "", nil, time.Time{}, object.MapResolveErr(err)
	}
	if err := auth.ChargeRequest(ctx); err != nil {
		return "", nil, time.Time{}, err
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
	objectURI := "object://" + tenantID.String() + "/" + objectKey + "/" + key
	if err := auth.AssertCapabilityOp(ctx, capability.OpPresign, objectURI); err != nil {
		return "", nil, time.Time{}, err
	}
	if err := auth.AssertCapabilityOp(ctx, capability.OpPut, objectURI); err != nil {
		return "", nil, time.Time{}, err
	}
	if err := h.authorize(ctx, p, tenantID, objectKey, key, cedar.ActionPresignPut); err != nil {
		return "", nil, time.Time{}, err
	}
	_, bucket, err := h.repo.LookupBucket(ctx, tenantID, objectKey, true) // presign part upload (mutation); positional PresignPart routes in step 3
	if err != nil {
		return "", nil, time.Time{}, object.MapResolveErr(err)
	}
	if err := auth.ChargeRequest(ctx); err != nil {
		return "", nil, time.Time{}, err
	}
	return h.storage.PresignPart(ctx, bucket, tenantID, storageUploadID, objectKey, key, partNumber, h.resolveTTL(ttl))
}

func (h *Handler) resolveTTL(requested time.Duration) time.Duration {
	// MaxTTL is always > 0 after NewHandler normalisation.
	if requested <= 0 {
		return h.cfg.DefaultTTL
	}
	if requested > h.cfg.MaxTTL {
		return h.cfg.MaxTTL
	}
	return requested
}

func (h *Handler) authorize(ctx context.Context, p *auth.Principal, tenantID uuid.UUID, objectKey, key, action string) error {
	decision, err := h.policy.IsAuthorized(ctx,
		&cedar.Principal{Subject: p.Subject, TenantID: tenantID, TenantSlug: p.TenantSlug, Roles: p.Roles, Scopes: apiutil.ScopeStrings(p.Scopes)},
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
