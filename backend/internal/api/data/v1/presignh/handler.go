// Package presign implements the PresignService business logic.
//
// Presign RPCs are distinct from UploadObject: they issue URLs for already-
// registered objects (PUT resume, GET for consumers). They enforce Cedar at
// signing time and never change an object's state; a fresh PUT URL moves the
// PENDING row's reaper deadline with it. Multipart part URLs are minted by
// MultipartUploadService, which owns the session they belong to.
package presignh

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/metrics"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/internal/presignttl"
	"github.com/oleg-tkachuk/paladin/backend/internal/uploadpolicy"
	"github.com/oleg-tkachuk/paladin/capability"
)

// Config is how long URLs live (internal/presignttl) and the global upload
// limits a bucket's constraints narrow (internal/uploadpolicy).
type Config struct {
	TTL    presignttl.Policy
	Limits uploadpolicy.Limits
}

type Storage interface {
	PresignGet(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, collection, key string, ttl time.Duration, disposition string) (url string, headers map[string]string, expiresAt time.Time, err error)
	PresignPut(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, collection, key, contentType, checksumAlgo string, ttl time.Duration, sizeHint int64) (url string, headers map[string]string, expiresAt time.Time, err error)
}

// ObjectRef is the slice of an object row a presign needs.
type ObjectRef struct {
	Collection  string
	Key         string
	State       string
	ContentType string
}

// ErrNotPending is returned by ExtendPendingPresign when the object left
// PENDING between the lookup and the update — it was completed, failed or
// deleted, and a new upload URL for it would be wrong.
var ErrNotPending = errors.New("object is no longer pending")

type Repository interface {
	LookupObject(ctx context.Context, tenantID uuid.UUID, collection string, objectID uuid.UUID) (ObjectRef, error)
	// LookupBucketMeta returns the physical (backend, bucket) bound to a
	// Collection plus the backend's events flag, which decides the upload's
	// completion mode.
	LookupBucketMeta(ctx context.Context, tenantID uuid.UUID, collection string, write bool) (objecth.BucketMeta, error)
	// ExtendPendingPresign moves a PENDING row's presign_expires_at to at
	// least expiresAt. The reaper fails PENDING rows past that deadline, so a
	// regenerated URL that did not move it would be failed under a client
	// still holding a valid URL. ErrNotPending when the row is not PENDING.
	ExtendPendingPresign(ctx context.Context, tenantID, objectID uuid.UUID, expiresAt time.Time) error
}

type Handler struct {
	repo    Repository
	storage Storage
	policy  cedar.Authorizer
	cfg     Config
}

func NewHandler(repo Repository, storage Storage, policy cedar.Authorizer, cfg Config) *Handler {
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
		metrics.RecordPresign(ctx, metrics.PresignOpGet, metrics.PresignOutcome(err), time.Since(start).Seconds())
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
	disposition, err = objecth.NormalizeContentDisposition(disposition)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	obj, err := h.repo.LookupObject(ctx, tenantID, collection, objectID)
	if err != nil {
		return "", nil, time.Time{}, connect.NewError(connect.CodeNotFound, err)
	}
	collection, key, state := obj.Collection, obj.Key, obj.State
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
	meta, err := h.repo.LookupBucketMeta(ctx, tenantID, collection, false) // presign GET (read)
	if err != nil {
		return "", nil, time.Time{}, objecth.MapResolveErr(err)
	}
	backendID, bucket := meta.BackendID, meta.BucketName
	ttl, err = h.cfg.TTL.ResolveWithin(presignttl.OpGet, ttl,
		uploadpolicy.For(h.cfg.Limits, meta.Constraints).GetTTLCeiling())
	if err != nil {
		return "", nil, time.Time{}, err
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
	return h.storage.PresignGet(ctx, backendID, bucket, tenantID, collection, key, ttl, disposition)
}

// UploadURL is a regenerated PUT URL and how the upload completes.
type UploadURL struct {
	URL            string
	Headers        map[string]string
	ExpiresAt      time.Time
	CompletionMode objecth.CompletionMode
}

// RegenerateUploadURL issues a fresh PUT URL for an object still PENDING. The
// URL is signed with the object's stored Content-Type — the one the first URL
// bound — and the row's reaper deadline moves to the new URL's expiry.
func (h *Handler) RegenerateUploadURL(ctx context.Context, collection, objectIDStr string, ttl time.Duration) (out UploadURL, err error) {
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
		metrics.RecordPresign(ctx, metrics.PresignOpPut, metrics.PresignOutcome(err), time.Since(start).Seconds())
	}()

	tenantID, p, err := apiutil.CallerContext(ctx)
	if err != nil {
		return UploadURL{}, err
	}
	if collection == "" || objectIDStr == "" {
		return UploadURL{}, connect.NewError(connect.CodeInvalidArgument, errors.New("collection and object_id are required"))
	}
	objectID, err := uuid.Parse(objectIDStr)
	if err != nil {
		return UploadURL{}, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid object_id: %w", err))
	}
	obj, err := h.repo.LookupObject(ctx, tenantID, collection, objectID)
	if err != nil {
		return UploadURL{}, connect.NewError(connect.CodeNotFound, err)
	}
	collection, key := obj.Collection, obj.Key
	// Only PENDING objects may receive a fresh upload URL. AVAILABLE objects
	// would silently overwrite committed data; FAILED/DELETED rows are
	// terminal and presigning a PUT against them is meaningless.
	if obj.State != "PENDING" {
		return UploadURL{}, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("object state %s does not allow PUT", obj.State))
	}
	objectURI := "object://" + tenantID.String() + "/" + collection + "/" + key
	if err := auth.AssertCapabilityOp(ctx, capability.OpPresign, objectURI); err != nil {
		return UploadURL{}, err
	}
	if err := auth.AssertCapabilityOp(ctx, capability.OpPut, objectURI); err != nil {
		return UploadURL{}, err
	}
	// Resolve the (backend, bucket) BEFORE authz so a bucket:/collection:-
	// scoped write PAT enforces here; the same resolution routes the PUT.
	meta, err := h.repo.LookupBucketMeta(ctx, tenantID, collection, true) // presign PUT (mutation)
	if err != nil {
		return UploadURL{}, objecth.MapResolveErr(err)
	}
	ttl, err = h.cfg.TTL.ResolveWithin(presignttl.OpPut, ttl,
		uploadpolicy.For(h.cfg.Limits, meta.Constraints).PutTTLCeiling())
	if err != nil {
		return UploadURL{}, err
	}
	if err := h.authorize(ctx, p, tenantID, collection, key, meta.BackendID, meta.BucketName, cedar.ActionPresignPut); err != nil {
		return UploadURL{}, err
	}
	if err := auth.ChargeRequest(ctx); err != nil {
		return UploadURL{}, err
	}
	url, headers, expires, err := h.storage.PresignPut(ctx, meta.BackendID, meta.BucketName, tenantID, collection, key, obj.ContentType, "", ttl, 0)
	if err != nil {
		return UploadURL{}, connect.NewError(connect.CodeInternal, fmt.Errorf("presign PUT: %w", err))
	}
	if err := h.repo.ExtendPendingPresign(ctx, tenantID, objectID, expires); err != nil {
		if errors.Is(err, ErrNotPending) {
			return UploadURL{}, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		return UploadURL{}, apiutil.MapError(fmt.Errorf("extend presign deadline: %w", err))
	}
	mode := objecth.CompletionModeExplicit
	if meta.EventsEnabled {
		mode = objecth.CompletionModeImplicit
	}
	return UploadURL{URL: url, Headers: headers, ExpiresAt: expires, CompletionMode: mode}, nil
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
