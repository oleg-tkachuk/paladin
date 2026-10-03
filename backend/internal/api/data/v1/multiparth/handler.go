// Package multipart implements the MultipartService business logic.
//
// Lifecycle: Initiate → (PresignPart × N) → Complete|Abort. Session state
// lives in multipart_uploads; the parts themselves are known only to the
// storage backend, because clients PUT them there directly through presigned
// URLs and no part upload passes through Paladin.
package multiparth

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
	"github.com/oleg-tkachuk/paladin/backend/internal/metrics"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/internal/presignttl"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/capability"
	"go.uber.org/zap"
)

type Storage interface {
	InitiateMultipart(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, collection, key, contentType string) (storageUploadID string, err error)
	CompleteMultipart(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, storageUploadID, collection, key string, parts []PartETag) (etag string, sizeBytes int64, err error)
	AbortMultipart(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, storageUploadID, collection, key string) error
	// ListMultipartParts asks the backend which parts have actually landed.
	// It has to be the backend: clients PUT parts straight to the object
	// store through presigned URLs, so no part upload passes through Paladin
	// and no table here can know what arrived.
	ListMultipartParts(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, storageUploadID, collection, key string, maxParts, afterPartNumber int32) ([]Part, int32, error)
	PresignPart(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, storageUploadID, collection, key string, partNumber int32, ttl time.Duration) (url string, headers map[string]string, expiresAt time.Time, err error)
}

// Part is one part the storage backend reports as uploaded.
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

// SessionRef is the object a caller claims an upload session belongs to,
// parsed from the request's object_name. Every per-session RPC carries that
// name, and until this existed none of them checked it: the session was found
// by upload_id alone and the name was accepted unread. A mismatched pair —
// wrong name, right id — was answered as though it were right.
type SessionRef struct {
	Collection string
	ObjectID   uuid.UUID
}

// assertSessionMatches rejects a request whose object_name disagrees with the
// session it names. InvalidArgument rather than NotFound: the tenant check
// above has already established the caller may see this session, so the
// disagreement is the caller's bookkeeping, not a probe — and saying so is
// more useful than pretending the session is missing.
func assertSessionMatches(sess Session, want SessionRef) error {
	if want.ObjectID == uuid.Nil && want.Collection == "" {
		return nil // caller did not name an object (internal call sites)
	}
	if want.ObjectID != uuid.Nil && sess.ObjectID != want.ObjectID {
		return connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("object_name names object %s but upload %s belongs to %s",
				want.ObjectID, sess.UploadID, sess.ObjectID))
	}
	if want.Collection != "" && sess.Collection != want.Collection {
		return connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("object_name names collection %q but upload %s belongs to %q",
				want.Collection, sess.UploadID, sess.Collection))
	}
	return nil
}

type Session struct {
	UploadID        string
	ObjectID        uuid.UUID
	TenantID        uuid.UUID
	BackendID       string // storage backend the upload was initiated against
	Bucket          string // physical S3 bucket; anchored at initiate time
	Collection      string
	Key             string
	StorageUploadID string
	PartSizeBytes   int64
	TotalParts      int32
	CreatedAt       time.Time
}

type InitiateArgs struct {
	TenantID uuid.UUID
	// InitiatedBy* attribute the upload to the principal that started it.
	// Filled from the request context, never by the caller — see Initiate.
	InitiatedBySubject string
	InitiatedByKind    string
	Collection         string
	Key                string
	ContentType        string
	TotalParts         int32
	PartSizeBytes      int64
	SizeHint           int64
	ChecksumAlgo       string
	Metadata           map[string]string
	Tags               map[string]string
	// ExternalRef is the caller's own identifier for the object, stored on
	// the objects row. Accepted by InitiateMultipartUpload and dropped on the
	// floor until now: the field existed on the request, on this struct's
	// single-shot sibling, and as a column — just not on the path between
	// them.
	ExternalRef string
}

type CompleteArgs struct {
	TenantID uuid.UUID
	UploadID string
	Parts    []PartETag
}

type Repository interface {
	InitiateSession(ctx context.Context, args InitiateArgs, objectID uuid.UUID, storageUploadID, backendID, bucket string) (Session, error)
	GetSession(ctx context.Context, uploadID string) (Session, error)
	DeleteSession(ctx context.Context, uploadID string) error
	// LookupBucket returns the storage backend id and the physical S3 bucket
	// bound to the Collection. Used to route storage calls to the right
	// (backend, bucket); callers that don't route on backend yet may discard
	// backendID (docs/backend-registry.md).
	LookupBucket(ctx context.Context, tenantID uuid.UUID, collection string, write bool) (backendID, bucket string, err error)
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
	Collection   string
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
	ttl      presignttl.Policy
	versions VersionRecorder // optional
	quota    QuotaUpdater    // optional
}

// SetQuotaUpdater attaches the optional usage hook called on
// CompleteMultipartUpload after a successful promotion. nil = no-op.
func (h *Handler) SetQuotaUpdater(q QuotaUpdater) { h.quota = q }

func NewHandler(repo Repository, storage Storage, policy cedar.Authorizer, sm *statemachine.Transitioner, ttl presignttl.Policy) *Handler {
	return &Handler{repo: repo, storage: storage, policy: policy, sm: sm, ttl: ttl}
}

// SetVersionRecorder attaches the optional recorder used after a successful
// CompleteMultipartUpload promote. Wired by main.
func (h *Handler) SetVersionRecorder(v VersionRecorder) { h.versions = v }

// InitiateMultipartUpload creates the PENDING object row and opens a storage
// multipart session. Handler contract: size_bytes is required here because
// part sizing needs it (unlike UploadObject where it's a hint).
// S3 semantics, which every supported backend follows: a part must be at
// least 5 MiB except the last, and an upload may have at most 10 000 parts.
// Together they cap a multipart upload at ~48.8 GiB with the minimum part
// size, so the part size grows with the object rather than the count.
const (
	minPartSizeBytes int64 = 5 * 1024 * 1024
	maxPartCount     int64 = 10000
)

// planParts picks a part size and count for an object of `size` bytes.
// Starts at the minimum and doubles until the count fits, so small uploads
// stay cheap to retry and large ones stay within the part limit.
func planParts(size int64) (partSize int64, totalParts int32) {
	partSize = minPartSizeBytes
	for (size+partSize-1)/partSize > maxPartCount {
		partSize *= 2
	}
	n := (size + partSize - 1) / partSize
	if n < 1 {
		n = 1
	}
	return partSize, int32(n)
}

func (h *Handler) InitiateMultipartUpload(ctx context.Context, args InitiateArgs) (*Session, error) {
	tenantID, p, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	args.TenantID = tenantID
	// Attribution comes from the caller's principal, never from the request:
	// a client must not be able to name someone else as the initiator.
	args.InitiatedBySubject = p.Subject
	args.InitiatedByKind = p.Kind.String()
	if args.SizeHint <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("size_bytes is required for multipart uploads"))
	}
	// The client does not choose the part size — the request has no field
	// for it, and the response promises a `recommended_part_size` plus a
	// `total_parts`. Computing them here is what makes those promises true:
	// they were left zero, so a caller had nothing to slice the file by and
	// PresignPart rejected every part number as out of range.
	args.PartSizeBytes, args.TotalParts = planParts(args.SizeHint)
	objectURI := "object://" + tenantID.String() + "/" + args.Collection + "/" + args.Key
	if err := auth.AssertCapabilityOp(ctx, capability.OpPut, objectURI); err != nil {
		return nil, err
	}
	// Resolve the (backend, bucket) BEFORE authz so a bucket:/collection:-
	// scoped write PAT enforces on multipart init; the same resolution routes
	// InitiateMultipart and anchors the session below.
	backendID, bucket, err := h.repo.LookupBucket(ctx, tenantID, args.Collection, true) // multipart init (mutation)
	if err != nil {
		return nil, objecth.MapResolveErr(err)
	}
	if err := h.authorize(ctx, p, tenantID, args.Collection, args.Key, backendID, bucket, cedar.ActionPutObject, args.SizeHint, args.ContentType); err != nil {
		return nil, err
	}

	storageUploadID, err := h.storage.InitiateMultipart(ctx, backendID, bucket, tenantID, args.Collection, args.Key, args.ContentType)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("storage initiate: %w", err))
	}

	objectID := uuid.Must(uuid.NewV7())
	session, err := h.repo.InitiateSession(ctx, args, objectID, storageUploadID, backendID, bucket)
	if err != nil {
		// Best-effort rollback: abort the orphan storage session. Log and
		// proceed — a background sweeper eventually cleans stragglers.
		_ = h.storage.AbortMultipart(ctx, backendID, bucket, tenantID, storageUploadID, args.Collection, args.Key)
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
	objectURI := "object://" + tenantID.String() + "/" + sess.Collection + "/" + sess.Key
	if err := auth.AssertCapabilityOp(ctx, capability.OpPut, objectURI); err != nil {
		return err
	}
	// The session anchored its (backend, bucket) at initiate time; pass it to
	// authz so a bucket:/collection:-scoped PAT enforces on complete.
	if err := h.authorize(ctx, principal, tenantID, sess.Collection, sess.Key, sess.BackendID, sess.Bucket, cedar.ActionPutObject, 0, ""); err != nil {
		return err
	}
	backendID, bucket := sess.BackendID, sess.Bucket
	if bucket == "" {
		backendID, bucket, err = h.repo.LookupBucket(ctx, tenantID, sess.Collection, true) // multipart complete (mutation)
		if err != nil {
			return objecth.MapResolveErr(err)
		}
	}
	etag, size, err := h.storage.CompleteMultipart(ctx, backendID, bucket, tenantID, sess.StorageUploadID, sess.Collection, sess.Key, args.Parts)
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
			Collection:  sess.Collection,
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
		if err := h.quota.OnObjectPromoted(ctx, sess.TenantID, size); err != nil {
			// Suppressed on purpose — a transient accounting failure must not
			// undo a successful state transition, and the nightly job corrects
			// drift. Logged because that correction is invisible otherwise:
			// without a line here, "usage looks wrong" has no trail back to
			// the writes that were never counted.
			logger.FromContext(ctx).Warn("quota not credited for promoted object",
				zap.String("tenant_id", sess.TenantID.String()),
				zap.Int64("size", size), zap.Error(err))
		}
	}
	if err := h.repo.DeleteSession(ctx, args.UploadID); err != nil {
		// Non-fatal — the object is AVAILABLE and the TTL sweep will collect
		// the row. Logged because "the sweep will get it" is an assumption:
		// if this fails for every upload, sessions accumulate until the sweep
		// is the only thing keeping the table finite, and nothing else would
		// say so.
		logger.FromContext(ctx).Warn("multipart session not deleted after complete",
			zap.String("upload_id", args.UploadID), zap.Error(err))
	}
	return nil
}

func (h *Handler) AbortMultipartUpload(ctx context.Context, uploadID string, want SessionRef) error {
	tenantID, principal, err := apiutil.CallerContext(ctx)
	if err != nil {
		return err
	}
	sess, err := h.repo.GetSession(ctx, uploadID)
	if err != nil {
		return connect.NewError(connect.CodeNotFound, err)
	}
	if err := assertSessionMatches(sess, want); err != nil {
		return err
	}
	objectURI := "object://" + tenantID.String() + "/" + sess.Collection + "/" + sess.Key
	if err := auth.AssertCapabilityOp(ctx, capability.OpDelete, objectURI); err != nil {
		return err
	}
	// Session-anchored (backend, bucket) → authz enforces bucket:/collection:
	// scopes on abort.
	if err := h.authorize(ctx, principal, tenantID, sess.Collection, sess.Key, sess.BackendID, sess.Bucket, cedar.ActionDeleteObject, 0, ""); err != nil {
		return err
	}
	backendID, bucket := sess.BackendID, sess.Bucket
	if bucket == "" {
		backendID, bucket, err = h.repo.LookupBucket(ctx, tenantID, sess.Collection, true) // abort multipart (mutation)
		if err != nil {
			return objecth.MapResolveErr(err)
		}
	}
	if err := h.storage.AbortMultipart(ctx, backendID, bucket, tenantID, sess.StorageUploadID, sess.Collection, sess.Key); err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	// Mark the underlying object FAILED so reconciler won't promote it.
	if err := h.sm.MarkFailed(ctx, sess.ObjectID, "multipart-aborted"); err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	if err := h.repo.DeleteSession(ctx, uploadID); err != nil {
		// Same as the complete path: the abort succeeded, so this must not
		// fail the RPC — but a systematic failure leaves rows behind, and
		// silence is what makes that invisible.
		logger.FromContext(ctx).Warn("multipart session not deleted after abort",
			zap.String("upload_id", uploadID), zap.Error(err))
	}
	return nil
}

// PresignPart issues a presigned URL for uploading a single part of an
// in-flight multipart session. Authorization is checked against the underlying
// object's (collection, key); the storage URL targets the bucket bound to that
// Collection.
func (h *Handler) PresignPart(ctx context.Context, uploadID string, partNumber int32, ttl time.Duration, want SessionRef) (url string, hdrs map[string]string, exp time.Time, err error) {
	// Counted like every other presign: Paladin never proxies bytes, so the
	// URL is the transfer as far as the control plane is concerned.
	start := time.Now()
	defer func() {
		metrics.RecordPresign(ctx, metrics.PresignOpPart, metrics.PresignOutcome(err), time.Since(start).Seconds())
	}()

	ttl, err = h.ttl.Resolve(presignttl.OpPart, ttl)
	if err != nil {
		return "", nil, time.Time{}, err
	}
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
	if err := assertSessionMatches(sess, want); err != nil {
		return "", nil, time.Time{}, err
	}
	if partNumber <= 0 || (sess.TotalParts > 0 && partNumber > sess.TotalParts) {
		return "", nil, time.Time{}, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("part_number %d out of range (1..%d)", partNumber, sess.TotalParts))
	}
	objectURI := "object://" + tenantID.String() + "/" + sess.Collection + "/" + sess.Key
	// Presigned part URL grants Put on the underlying object; gate on
	// both OpPresign (the act of issuing a URL) and OpPut (the op the
	// URL ultimately authorises). Either failure short-circuits.
	if err := auth.AssertCapabilityOp(ctx, capability.OpPresign, objectURI); err != nil {
		return "", nil, time.Time{}, err
	}
	if err := auth.AssertCapabilityOp(ctx, capability.OpPut, objectURI); err != nil {
		return "", nil, time.Time{}, err
	}
	// Session-anchored (backend, bucket) → authz enforces bucket:/collection:
	// scopes on the part presign.
	if err := h.authorize(ctx, p, tenantID, sess.Collection, sess.Key, sess.BackendID, sess.Bucket, cedar.ActionPresignPut, 0, ""); err != nil {
		return "", nil, time.Time{}, err
	}
	backendID, bucket := sess.BackendID, sess.Bucket
	if bucket == "" {
		backendID, bucket, err = h.repo.LookupBucket(ctx, tenantID, sess.Collection, true) // presign part (mutation)
		if err != nil {
			return "", nil, time.Time{}, objecth.MapResolveErr(err)
		}
	}
	return h.storage.PresignPart(ctx, backendID, bucket, tenantID, sess.StorageUploadID, sess.Collection, sess.Key, partNumber, ttl)
}

// ListParts reports which parts of an in-flight upload have actually landed
// in the object store, so a client resuming an interrupted upload knows what
// it still has to send.
//
// The answer comes from the backend, not from a table here. Parts are PUT
// directly to the object store through presigned URLs — Paladin hands out the
// URL and never sees the transfer — so a local journal could only record what
// was authorised, never what arrived, and those differ in exactly the case
// this endpoint exists to serve.
//
// The page token is the last part number seen, matching the S3 contract the
// call is a thin wrapper over.
func (h *Handler) ListParts(ctx context.Context, uploadID string, pageSize int32, pageToken string, want SessionRef) ([]Part, string, error) {
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
	if err := assertSessionMatches(sess, want); err != nil {
		return nil, "", err
	}
	// The parts all belong to the session's one object, so that object is
	// the resource the listing touches.
	if err := auth.AssertCapabilityOp(ctx, capability.OpList,
		"object://"+tenantID.String()+"/"+sess.Collection+"/"+sess.Key); err != nil {
		return nil, "", err
	}
	// Session-anchored (backend, bucket) → authz enforces bucket:/collection:
	// read scopes on listing parts.
	if err := h.authorize(ctx, principal, tenantID, sess.Collection, sess.Key, sess.BackendID, sess.Bucket, cedar.ActionGetObject, 0, ""); err != nil {
		return nil, "", err
	}
	if pageSize <= 0 || pageSize > 1000 {
		pageSize = 100
	}
	var after int32
	if pageToken != "" {
		n, perr := strconv.ParseInt(pageToken, 10, 32)
		if perr != nil || n < 0 {
			return nil, "", connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("page_token must be a part number: %q", pageToken))
		}
		after = int32(n)
	}

	parts, next, err := h.storage.ListMultipartParts(ctx,
		sess.BackendID, sess.Bucket, tenantID, sess.StorageUploadID,
		sess.Collection, sess.Key, pageSize, after)
	if err != nil {
		return nil, "", connect.NewError(connect.CodeInternal, err)
	}
	var nextToken string
	if next > 0 {
		nextToken = strconv.FormatInt(int64(next), 10)
	}
	return parts, nextToken, nil
}

// authorize runs the Cedar check for a multipart action. backendID/bucket
// carry the resolved physical binding (from the session, or a pre-authz
// LookupBucket on initiate) so the scope-enforcement built-in can confine a
// bucket:/collection:-scoped PAT to its own bucket. Empty backendID/bucket
// leaves the resource without those scope keys, which only ever denies a
// scoped principal — unscoped/roles-only callers are unaffected.
func (h *Handler) authorize(ctx context.Context, p *auth.Principal, tenantID uuid.UUID, collection, key, backendID, bucket, action string, sizeBytes int64, contentType string) error {
	decision, err := h.policy.IsAuthorized(ctx,
		apiutil.CedarPrincipalFor(p, tenantID),
		action,
		&cedar.Resource{TenantID: tenantID, Collection: collection, Key: key, BackendID: backendID, BucketName: bucket, SizeBytes: sizeBytes, ContentType: contentType},
		cedar.RequestContext{SizeBytes: sizeBytes, ContentType: contentType, Now: time.Now()},
	)
	if err != nil {
		return apiutil.MapError(fmt.Errorf("authz: %w", err))
	}
	if decision != cedar.DecisionAllow {
		return connect.NewError(connect.CodePermissionDenied, errors.New("denied by policy"))
	}
	return nil
}
