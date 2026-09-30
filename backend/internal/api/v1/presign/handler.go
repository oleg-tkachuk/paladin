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

	"github.com/oleg-tkachuk/paladin/backend/internal/api/v1/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/v1/object"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/metrics"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/capability"
)

type Config struct {
	DefaultTTL     time.Duration
	MaxTTL         time.Duration
	DefaultMaxSize int64
}

// Safety bounds applied when Config leaves TTLs unset. A presigned URL
// bypasses Paladin and hits S3 directly, so it cannot be revoked before it
// expires — an unbounded MaxTTL (the zero value) would let a caller mint
// effectively permanent links. These ceilings are the last line of
// defense; operators tune the real values via config.
const (
	fallbackDefaultTTL = 1 * time.Hour
	fallbackMaxTTL     = 7 * 24 * time.Hour
)

type Storage interface {
	PresignGet(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, collection, key string, ttl time.Duration, disposition string) (url string, headers map[string]string, expiresAt time.Time, err error)
	PresignPut(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, collection, key, contentType, checksumAlgo string, ttl time.Duration, sizeHint int64) (url string, headers map[string]string, expiresAt time.Time, err error)
	PresignPart(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, storageUploadID, collection, key string, partNumber int32, ttl time.Duration) (url string, headers map[string]string, expiresAt time.Time, err error)
}

type Repository interface {
	LookupObjectByName(ctx context.Context, tenantID uuid.UUID, collection string, objectID uuid.UUID) (resolvedCollection, key, state string, err error)
	LookupMultipartSession(ctx context.Context, uploadID string) (storageUploadID, collection, key string, err error)
	// LookupBucket returns the physical S3 bucket bound to a Collection.
	// Used to route presign URLs to the correct bucket.
	LookupBucket(ctx context.Context, tenantID uuid.UUID, collection string, write bool) (backendID, bucket string, err error)
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

func (h *Handler) PresignGet(ctx context.Context, collection, objectIDStr string, ttl time.Duration, disposition string) (url string, hdrs map[string]string, exp time.Time, err error) {
	// Instrumented with a defer rather than a wrapper: the connectshim
	// coverage gate reads the body of the method the shim calls, and
	// delegating to an unexported twin hid this method's authorize call from
	// it. Named results are the cost of keeping the gate able to see the
	// gating — a fair trade.
	//
	// Paladin never proxies bytes, so a presigned URL IS the transfer as far
	// as the control plane is concerned. otelconnect counts the RPC; this
	// counts whether it produced a usable URL.
	start := time.Now()
	defer func() {
		metrics.RecordPresign(ctx, "get", presignOutcome(err), time.Since(start).Seconds())
	}()

	tenantID, p, err := apiutil.CallerContext(ctx)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	if collection == "" || objectIDStr == "" {
		return "", nil, time.Time{}, connect.NewError(connect.CodeInvalidArgument, errors.New("collection and object_id are required"))
	}
	objectID, err := uuid.Parse(objectIDStr)
	if err != nil {
		return "", nil, time.Time{}, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid object_id: %w", err))
	}
	collection, key, state, err := h.repo.LookupObjectByName(ctx, tenantID, collection, objectID)
	if err != nil {
		return "", nil, time.Time{}, connect.NewError(connect.CodeNotFound, err)
	}
	if state != "AVAILABLE" {
		return "", nil, time.Time{}, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("object state %s does not allow GET", state))
	}
	objectURI := "object://" + tenantID.String() + "/" + collection + "/" + key
	// Presigned GET URL grants OpGet on the underlying object; gate
	// on both OpPresign (the act of issuing a URL) and OpGet (the op
	// the URL ultimately authorises).
	if err := auth.AssertCapabilityOp(ctx, capability.OpPresign, objectURI); err != nil {
		return "", nil, time.Time{}, err
	}
	if err := auth.AssertCapabilityOp(ctx, capability.OpGet, objectURI); err != nil {
		return "", nil, time.Time{}, err
	}
	// Resolve the (backend, bucket) BEFORE authz so a bucket:/collection:-
	// scoped read PAT enforces here; the same resolution routes PresignGet.
	backendID, bucket, err := h.repo.LookupBucket(ctx, tenantID, collection, false) // presign GET (read)
	if err != nil {
		return "", nil, time.Time{}, object.MapResolveErr(err)
	}
	if err := h.authorize(ctx, p, tenantID, collection, key, backendID, bucket, cedar.ActionPresignGet); err != nil {
		return "", nil, time.Time{}, err
	}
	// Capability budget burn — gates issuance for over-budget callers
	// before we hand them a usable presigned URL. No-op when the
	// caller is JWT-authenticated or ChargePerRequest is 0.
	if err := auth.ChargeRequest(ctx); err != nil {
		return "", nil, time.Time{}, err
	}
	return h.storage.PresignGet(ctx, backendID, bucket, tenantID, collection, key, h.resolveTTL(ttl), disposition)
}

func (h *Handler) PresignPut(ctx context.Context, collection, objectIDStr, contentType, checksumAlgo string, ttl time.Duration, sizeHint int64) (url string, hdrs map[string]string, exp time.Time, err error) {
	// Instrumented with a defer rather than a wrapper: the connectshim
	// coverage gate reads the body of the method the shim calls, and
	// delegating to an unexported twin hid this method's authorize call from
	// it. Named results are the cost of keeping the gate able to see the
	// gating — a fair trade.
	//
	// Paladin never proxies bytes, so a presigned URL IS the transfer as far
	// as the control plane is concerned. otelconnect counts the RPC; this
	// counts whether it produced a usable URL.
	start := time.Now()
	defer func() {
		metrics.RecordPresign(ctx, "put", presignOutcome(err), time.Since(start).Seconds())
	}()

	tenantID, p, err := apiutil.CallerContext(ctx)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	if collection == "" || objectIDStr == "" {
		return "", nil, time.Time{}, connect.NewError(connect.CodeInvalidArgument, errors.New("collection and object_id are required"))
	}
	objectID, err := uuid.Parse(objectIDStr)
	if err != nil {
		return "", nil, time.Time{}, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid object_id: %w", err))
	}
	collection, key, state, err := h.repo.LookupObjectByName(ctx, tenantID, collection, objectID)
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
	objectURI := "object://" + tenantID.String() + "/" + collection + "/" + key
	if err := auth.AssertCapabilityOp(ctx, capability.OpPresign, objectURI); err != nil {
		return "", nil, time.Time{}, err
	}
	if err := auth.AssertCapabilityOp(ctx, capability.OpPut, objectURI); err != nil {
		return "", nil, time.Time{}, err
	}
	// Resolve the (backend, bucket) BEFORE authz so a bucket:/collection:-
	// scoped write PAT enforces here; the same resolution routes PresignPut.
	backendID, bucket, err := h.repo.LookupBucket(ctx, tenantID, collection, true) // presign PUT (mutation)
	if err != nil {
		return "", nil, time.Time{}, object.MapResolveErr(err)
	}
	if err := h.authorize(ctx, p, tenantID, collection, key, backendID, bucket, cedar.ActionPresignPut); err != nil {
		return "", nil, time.Time{}, err
	}
	if err := auth.ChargeRequest(ctx); err != nil {
		return "", nil, time.Time{}, err
	}
	return h.storage.PresignPut(ctx, backendID, bucket, tenantID, collection, key, contentType, checksumAlgo, h.resolveTTL(ttl), sizeHint)
}

func (h *Handler) PresignPart(ctx context.Context, uploadID string, partNumber int32, ttl time.Duration) (url string, hdrs map[string]string, exp time.Time, err error) {
	// Instrumented with a defer rather than a wrapper: the connectshim
	// coverage gate reads the body of the method the shim calls, and
	// delegating to an unexported twin hid this method's authorize call from
	// it. Named results are the cost of keeping the gate able to see the
	// gating — a fair trade.
	//
	// Paladin never proxies bytes, so a presigned URL IS the transfer as far
	// as the control plane is concerned. otelconnect counts the RPC; this
	// counts whether it produced a usable URL.
	start := time.Now()
	defer func() {
		metrics.RecordPresign(ctx, "part", presignOutcome(err), time.Since(start).Seconds())
	}()

	tenantID, p, err := apiutil.CallerContext(ctx)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	storageUploadID, collection, key, err := h.repo.LookupMultipartSession(ctx, uploadID)
	if err != nil {
		return "", nil, time.Time{}, connect.NewError(connect.CodeNotFound, err)
	}
	objectURI := "object://" + tenantID.String() + "/" + collection + "/" + key
	if err := auth.AssertCapabilityOp(ctx, capability.OpPresign, objectURI); err != nil {
		return "", nil, time.Time{}, err
	}
	if err := auth.AssertCapabilityOp(ctx, capability.OpPut, objectURI); err != nil {
		return "", nil, time.Time{}, err
	}
	// Resolve the (backend, bucket) BEFORE authz so a bucket:/collection:-
	// scoped write PAT enforces here; the same resolution routes PresignPart.
	backendID, bucket, err := h.repo.LookupBucket(ctx, tenantID, collection, true) // presign part upload (mutation)
	if err != nil {
		return "", nil, time.Time{}, object.MapResolveErr(err)
	}
	if err := h.authorize(ctx, p, tenantID, collection, key, backendID, bucket, cedar.ActionPresignPut); err != nil {
		return "", nil, time.Time{}, err
	}
	if err := auth.ChargeRequest(ctx); err != nil {
		return "", nil, time.Time{}, err
	}
	return h.storage.PresignPart(ctx, backendID, bucket, tenantID, storageUploadID, collection, key, partNumber, h.resolveTTL(ttl))
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

// authorize runs the Cedar check for a presign action. backendID/bucket carry
// the resolved physical binding so the scope-enforcement built-in can confine a
// bucket:/collection:-scoped PAT to its own bucket — callers resolve the bucket
// (via LookupBucket) BEFORE calling this so a scoped principal is not
// fail-closed on the write/read path. Empty backendID/bucket (binding not
// resolvable) leaves the resource without those scope keys, which only ever
// denies a scoped principal — unscoped/roles-only callers are unaffected.
func (h *Handler) authorize(ctx context.Context, p *auth.Principal, tenantID uuid.UUID, collection, key, backendID, bucket, action string) error {
	decision, err := h.policy.IsAuthorized(ctx,
		apiutil.CedarPrincipalFor(p, tenantID),
		action,
		&cedar.Resource{TenantID: tenantID, Collection: collection, Key: key, BackendID: backendID, BucketName: bucket},
		cedar.RequestContext{Now: time.Now()},
	)
	if err != nil {
		return apiutil.MapError(fmt.Errorf("authz: %w", err))
	}
	if decision != cedar.DecisionAllow {
		return connect.NewError(connect.CodePermissionDenied, errors.New("denied by policy"))
	}
	return nil
}

// presignOutcome collapses an error into a bounded label. The connect code is
// the right granularity: it separates "denied by policy" from "no such object"
// from "budget exhausted" — three different operational problems — without
// admitting the unbounded set of error strings.
func presignOutcome(err error) string {
	if err == nil {
		return "ok"
	}
	return connect.CodeOf(err).String()
}
