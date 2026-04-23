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

	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
)

type Config struct {
	DefaultTTL     time.Duration
	MaxTTL         time.Duration
	DefaultMaxSize int64
}

type Storage interface {
	PresignGet(ctx context.Context, bucket, key string, ttl time.Duration, disposition string) (url string, headers map[string]string, expiresAt time.Time, err error)
	PresignPut(ctx context.Context, bucket, key, contentType, checksumAlgo string, ttl time.Duration, sizeHint int64) (url string, headers map[string]string, expiresAt time.Time, err error)
	PresignPart(ctx context.Context, storageUploadID, bucket, key string, partNumber int32, ttl time.Duration) (url string, headers map[string]string, expiresAt time.Time, err error)
}

type Repository interface {
	LookupObjectByName(ctx context.Context, tenantID uuid.UUID, bucketID string, objectID uuid.UUID) (bucket, key, state string, err error)
	LookupMultipartSession(ctx context.Context, uploadID string) (storageUploadID, bucket, key string, err error)
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
	tenantID, p, err := callerContext(ctx)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	bucketID, objectID, err := parseObjectName(objectName)
	if err != nil {
		return "", nil, time.Time{}, connect.NewError(connect.CodeInvalidArgument, err)
	}
	bucket, key, state, err := h.repo.LookupObjectByName(ctx, tenantID, bucketID, objectID)
	if err != nil {
		return "", nil, time.Time{}, connect.NewError(connect.CodeNotFound, err)
	}
	if state != "AVAILABLE" {
		return "", nil, time.Time{}, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("object state %s does not allow GET", state))
	}
	if err := h.authorize(ctx, p, tenantID, bucketID, key, cedar.ActionPresignGet); err != nil {
		return "", nil, time.Time{}, err
	}
	return h.storage.PresignGet(ctx, bucket, key, h.resolveTTL(ttl), disposition)
}

func (h *Handler) PresignPut(ctx context.Context, objectName, contentType, checksumAlgo string, ttl time.Duration, sizeHint int64) (string, map[string]string, time.Time, error) {
	tenantID, p, err := callerContext(ctx)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	bucketID, objectID, err := parseObjectName(objectName)
	if err != nil {
		return "", nil, time.Time{}, connect.NewError(connect.CodeInvalidArgument, err)
	}
	bucket, key, _, err := h.repo.LookupObjectByName(ctx, tenantID, bucketID, objectID)
	if err != nil {
		return "", nil, time.Time{}, connect.NewError(connect.CodeNotFound, err)
	}
	if err := h.authorize(ctx, p, tenantID, bucketID, key, cedar.ActionPresignPut); err != nil {
		return "", nil, time.Time{}, err
	}
	return h.storage.PresignPut(ctx, bucket, key, contentType, checksumAlgo, h.resolveTTL(ttl), sizeHint)
}

func (h *Handler) PresignPart(ctx context.Context, uploadID string, partNumber int32, ttl time.Duration) (string, map[string]string, time.Time, error) {
	tenantID, p, err := callerContext(ctx)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	storageUploadID, bucket, key, err := h.repo.LookupMultipartSession(ctx, uploadID)
	if err != nil {
		return "", nil, time.Time{}, connect.NewError(connect.CodeNotFound, err)
	}
	if err := h.authorize(ctx, p, tenantID, bucket, key, cedar.ActionPresignPut); err != nil {
		return "", nil, time.Time{}, err
	}
	return h.storage.PresignPart(ctx, storageUploadID, bucket, key, partNumber, h.resolveTTL(ttl))
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

func (h *Handler) authorize(ctx context.Context, p *auth.Principal, tenantID uuid.UUID, bucketID, key, action string) error {
	decision, err := h.policy.IsAuthorized(ctx,
		&cedar.Principal{Subject: p.Subject, TenantID: tenantID, Roles: p.Roles},
		action,
		&cedar.Resource{TenantID: tenantID, BucketID: bucketID, Key: key},
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

// parseObjectName parses "buckets/{bucket}/objects/{object_id}".
func parseObjectName(name string) (string, uuid.UUID, error) {
	const prefix = "buckets/"
	if len(name) <= len(prefix) || name[:len(prefix)] != prefix {
		return "", uuid.Nil, fmt.Errorf("invalid object name %q", name)
	}
	rest := name[len(prefix):]
	sep := -1
	for i, c := range rest {
		if c == '/' {
			sep = i
			break
		}
	}
	if sep <= 0 {
		return "", uuid.Nil, fmt.Errorf("invalid object name %q", name)
	}
	bucket := rest[:sep]
	remainder := rest[sep+1:]
	const objects = "objects/"
	if len(remainder) <= len(objects) || remainder[:len(objects)] != objects {
		return "", uuid.Nil, fmt.Errorf("invalid object name %q", name)
	}
	id, err := uuid.Parse(remainder[len(objects):])
	if err != nil {
		return "", uuid.Nil, fmt.Errorf("invalid object_id: %w", err)
	}
	return bucket, id, nil
}
