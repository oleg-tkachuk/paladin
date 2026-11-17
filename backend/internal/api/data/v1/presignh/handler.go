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
	"uuid"

	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
	"go.uber.org/zap"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/metrics"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/internal/presignttl"
	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/uploadpolicy"
)

// Config is how long URLs live (internal/presignttl) and the global upload
// limits a bucket's constraints narrow (internal/uploadpolicy).
type Config struct {
	TTL    presignttl.Policy
	Limits uploadpolicy.Limits
}

// Storage is the presigning half of objecth.Storage: the same arguments
// UploadObject and DownloadObject sign with, so a URL from either RPC is
// bound the same way.
type Storage interface {
	PresignGet(ctx context.Context, args objecth.PresignGetArgs) (url string, headers map[string]string, expiresAt time.Time, err error)
	PresignPut(ctx context.Context, args objecth.PresignPutArgs) (url string, headers map[string]string, expiresAt time.Time, err error)
}

// ObjectRef is the slice of an object row a presign needs.
type ObjectRef struct {
	Collection  string
	Key         string
	State       string
	ContentType string
	// SizeBytes is the size the object was registered with; nil when the row
	// records none.
	SizeBytes     *int64
	ChecksumAlgo  string
	ChecksumValue string // the checksum the object was registered with
	ETag          string
}

// ErrNotPending is returned by ExtendPendingPresign when the object left
// PENDING between the lookup and the update — it was completed, failed or
// deleted, and a new upload URL for it would be wrong.
var ErrNotPending = errors.New("object is no longer pending")

// ErrObjectNotFound is Repository.LookupObject's answer for an object not in
// the collection named: no such id, or one in another collection.
var ErrObjectNotFound = errors.New("object not found")

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
	log     *zap.Logger
}

// SetLogger sets where each presign says how it ended. Unset, it says
// nothing. No line carries a presigned URL.
func (h *Handler) SetLogger(l *zap.Logger) { h.log = l }

func NewHandler(repo Repository, storage Storage, policy cedar.Authorizer, cfg Config) *Handler {
	return &Handler{repo: repo, storage: storage, policy: policy, cfg: cfg, log: zap.NewNop()}
}

// PresignGet issues a download URL. requireETagMatch binds it to the
// object's current ETag (signed If-Match), so it serves only these bytes.
func (h *Handler) PresignGet(ctx context.Context, collection, objectIDStr string, ttl time.Duration, disposition string, requireETagMatch bool) (url string, hdrs map[string]string, exp time.Time, err error) {
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
		h.logOutcome(ctx, opPresignGet, collection, err)
	}()

	tenantID, p, err := apiutil.ActingContext(ctx)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	disposition, err = objecth.NormalizeContentDisposition(disposition)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	_, obj, err := h.lookupObject(ctx, tenantID, collection, objectIDStr)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	collection, key, state := obj.Collection, obj.Key, obj.State
	if state != string(statemachine.StateAvailable) {
		return "", nil, time.Time{}, connect.Errorf(connect.CodeFailedPrecondition,
			"object state %s does not allow GET", state)
	}
	// A presigned URL grants its op on the object: the capability must
	// allow both OpPresign (issuing the URL) and that op.
	objectURI := paladin.ObjectResource(tenantID.String(), collection, key)
	if err := auth.AssertCapabilityOp(ctx, auth.OpPresign, objectURI); err != nil {
		return "", nil, time.Time{}, err
	}
	if err := auth.AssertCapabilityOp(ctx, presignGet.capOp, objectURI); err != nil {
		return "", nil, time.Time{}, err
	}
	meta, ttl, err := h.grant(ctx, p, tenantID, collection, key, presignGet, ttl)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	backendID, bucket := meta.BackendID, meta.BucketName
	args := objecth.PresignGetArgs{
		BackendID: backendID, TenantID: tenantID, Bucket: bucket, Collection: collection, Key: key,
		TTL: ttl, ContentDisposition: disposition,
	}
	if requireETagMatch {
		if obj.ETag == "" {
			return "", nil, time.Time{}, connect.NewError(connect.CodeFailedPrecondition,
				"the object has no recorded ETag to bind the URL to")
		}
		args.IfMatch = obj.ETag
	}
	url, hdrs, exp, err = h.storage.PresignGet(ctx, args)
	if err != nil {
		return "", nil, time.Time{}, rpcerr.New(connect.CodeInternal, fmt.Errorf("presign GET: %w", err))
	}
	return url, hdrs, exp, nil
}

// UploadURL is a regenerated PUT URL and how the upload completes.
type UploadURL struct {
	URL            string
	Headers        map[string]string
	ExpiresAt      time.Time
	CompletionMode objecth.CompletionMode
}

// RegenerateUploadURL issues a fresh PUT URL for an object still PENDING. The
// URL is signed for the size, Content-Type and checksum the object was
// registered with — what the first URL bound — and the row's reaper deadline
// moves to the new URL's expiry.
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
		h.logOutcome(ctx, opPresignPut, collection, err)
	}()

	tenantID, p, err := apiutil.ActingContext(ctx)
	if err != nil {
		return UploadURL{}, err
	}
	objectID, obj, err := h.lookupObject(ctx, tenantID, collection, objectIDStr)
	if err != nil {
		return UploadURL{}, err
	}
	collection, key := obj.Collection, obj.Key
	// Only PENDING objects may receive a fresh upload URL. AVAILABLE objects
	// would silently overwrite committed data; FAILED/DELETED rows are
	// terminal and presigning a PUT against them is meaningless.
	if obj.State != string(statemachine.StatePending) {
		return UploadURL{}, connect.Errorf(connect.CodeFailedPrecondition,
			"object state %s does not allow PUT", obj.State)
	}
	// The new URL is bound to the size and checksum the object was
	// registered with. A row without them predates that binding; it cannot
	// get a bound URL, and an unbound one is what the binding exists to stop.
	if obj.SizeBytes == nil || obj.ChecksumValue == "" {
		return UploadURL{}, connect.NewError(connect.CodeFailedPrecondition,
			"object was registered without a size and checksum; start a new upload")
	}
	// A presigned URL grants its op on the object: the capability must
	// allow both OpPresign (issuing the URL) and that op.
	objectURI := paladin.ObjectResource(tenantID.String(), collection, key)
	if err := auth.AssertCapabilityOp(ctx, auth.OpPresign, objectURI); err != nil {
		return UploadURL{}, err
	}
	if err := auth.AssertCapabilityOp(ctx, presignPut.capOp, objectURI); err != nil {
		return UploadURL{}, err
	}
	meta, ttl, err := h.grant(ctx, p, tenantID, collection, key, presignPut, ttl)
	if err != nil {
		return UploadURL{}, err
	}
	url, headers, expires, err := h.storage.PresignPut(ctx, objecth.PresignPutArgs{
		BackendID: meta.BackendID, TenantID: tenantID, Bucket: meta.BucketName,
		Collection: collection, Key: key, ContentType: obj.ContentType,
		SizeBytes: *obj.SizeBytes, ChecksumAlgo: obj.ChecksumAlgo, ChecksumValue: obj.ChecksumValue,
		TTL: ttl,
	})
	if err != nil {
		return UploadURL{}, rpcerr.New(connect.CodeInternal, fmt.Errorf("presign PUT: %w", err))
	}
	if err := h.repo.ExtendPendingPresign(ctx, tenantID, objectID, expires); err != nil {
		if errors.Is(err, ErrNotPending) {
			return UploadURL{}, connect.NewError(connect.CodeFailedPrecondition, err.Error()).WithCause(err)
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
func (h *Handler) authorize(ctx context.Context, p *auth.Principal, tenantID uuid.UUID, collection, key, backendID, bucket string, action cedar.Action) error {
	return apiutil.Decide(ctx, h.policy, apiutil.CedarPrincipal(p), action,
		&cedar.Resource{TenantID: tenantID, Collection: collection, Key: key, BackendID: backendID, BucketName: bucket},
		cedar.RequestContext{})
}

// Operation names presign logs under.
const (
	opPresignGet = "presign get"
	opPresignPut = "presign put"
)

// lookupObject parses the object id and reads the object the request names.
// An object not in the collection answers NotFound; a store that fails is
// internal, never read as an absent object.
func (h *Handler) lookupObject(ctx context.Context, tenantID uuid.UUID, collection, objectIDStr string) (uuid.UUID, ObjectRef, error) {
	if collection == "" || objectIDStr == "" {
		return uuid.Nil(), ObjectRef{}, connect.NewError(connect.CodeInvalidArgument, "collection and object_id are required")
	}
	objectID, err := uuid.Parse(objectIDStr)
	if err != nil {
		return uuid.Nil(), ObjectRef{}, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("invalid object_id: %w", err))
	}
	obj, err := h.repo.LookupObject(ctx, tenantID, collection, objectID)
	switch {
	case errors.Is(err, ErrObjectNotFound):
		return uuid.Nil(), ObjectRef{}, connect.NewError(connect.CodeNotFound, err.Error()).WithCause(err)
	case err != nil:
		return uuid.Nil(), ObjectRef{}, rpcerr.New(connect.CodeInternal, fmt.Errorf("look up object: %w", err))
	}
	return objectID, obj, nil
}

// logOutcome records how a presign ended: issued at debug, refused at warn,
// failed at error. The URL is never logged; it is a bearer credential.
func (h *Handler) logOutcome(ctx context.Context, op, collection string, err error) {
	fields := []zap.Field{zap.String("op", op), zap.String("collection", collection)}
	if tenant, terr := auth.EffectiveTenant(ctx); terr == nil {
		fields = append(fields, zap.String("tenant_id", tenant.String()))
	}
	switch code := connect.CodeOf(err); {
	case err == nil:
		h.log.Debug("presigned URL issued", fields...)
	case code == connect.CodeInternal || code == connect.CodeUnknown || code == connect.CodeUnavailable:
		h.log.Error("presign failed", append(fields, zap.Error(err))...)
	default:
		h.log.Warn("presign refused", append(fields, zap.String("code", code.String()), zap.Error(err))...)
	}
}

// presignKind is what a presign of one direction checks: the capability op
// the URL grants, the Cedar action, whether it writes, and the TTL ceiling.
type presignKind struct {
	capOp   limes.Op
	ttlOp   presignttl.Op
	action  cedar.Action
	write   bool
	ceiling func(uploadpolicy.Policy) time.Duration
}

var (
	presignGet = presignKind{capOp: limes.OpGet, ttlOp: presignttl.OpGet, action: cedar.ActionPresignGet,
		ceiling: uploadpolicy.Policy.GetTTLCeiling}
	presignPut = presignKind{capOp: limes.OpPut, ttlOp: presignttl.OpPut, action: cedar.ActionPresignPut, write: true,
		ceiling: uploadpolicy.Policy.PutTTLCeiling}
)

// grant decides, once the capability allows it (assertPresignOps), whether
// the caller may have a URL of kind for the object, and for how long. The
// (backend, bucket) is resolved BEFORE authorization, so a bucket- or
// collection-scoped credential is enforced here; the same resolution routes
// the presign. The capability budget is charged last, gating issuance for an
// over-budget caller before a usable URL is handed over (a no-op for a JWT
// caller or a zero ChargePerRequest).
func (h *Handler) grant(ctx context.Context, p *auth.Principal, tenantID uuid.UUID, collection, key string, kind presignKind, ttl time.Duration) (objecth.BucketMeta, time.Duration, error) {
	meta, err := h.repo.LookupBucketMeta(ctx, tenantID, collection, kind.write)
	if err != nil {
		return objecth.BucketMeta{}, 0, objecth.MapResolveErr(err)
	}
	ttl, err = h.cfg.TTL.ResolveWithin(kind.ttlOp, ttl, kind.ceiling(uploadpolicy.For(h.cfg.Limits, meta.Constraints)))
	if err != nil {
		return objecth.BucketMeta{}, 0, err
	}
	if err := h.authorize(ctx, p, tenantID, collection, key, meta.BackendID, meta.BucketName, kind.action); err != nil {
		return objecth.BucketMeta{}, 0, err
	}
	if err := auth.ChargeRequest(ctx); err != nil {
		return objecth.BucketMeta{}, 0, err
	}
	return meta, ttl, nil
}
